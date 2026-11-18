package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/backgroundcheck"
	"github.com/henry40408/bkd/internal/db"
	"github.com/henry40408/bkd/internal/linkcheck"
	"github.com/henry40408/bkd/internal/server"
)

const (
	shutdownGrace          = 10 * time.Second
	readHeaderTimeout      = 10 * time.Second
	expiredSessionSweepGap = time.Hour
)

// serve follows a fixed order: database, password warm-up, setup token, link
// checker, background check, listener. The setup token is logged before the
// listener binds, so whoever sees the port open can already read it.
func serve(opts options, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, opts.db)
	if err != nil {
		return err
	}
	// Closing checkpoints the WAL, so the -wal and -shm files do not outlive the process.
	defer func() {
		if err := db.Close(pool); err != nil {
			logger.Error("cannot close the database", "err", err)
		}
	}()

	hasher := auth.NewHasher()
	if err := hasher.WarmUp(ctx); err != nil {
		return fmt.Errorf("password warm-up: %w", err)
	}
	setupToken, err := announceSetupToken(ctx, pool, logger)
	if err != nil {
		return err
	}
	forward, err := opts.forwardAuth(logger)
	if err != nil {
		return err
	}
	// Never with Allow: production checks public addresses only.
	checker := linkcheck.New(linkcheck.Config{Logger: logger})
	app := server.New(server.Config{
		DB:                  pool,
		SetupToken:          setupToken,
		SecureCookies:       opts.secureCookies,
		ForwardAuth:         forward,
		LinkChecker:         checker,
		BackgroundLinkCheck: opts.linkCheckInterval > 0,
		Hasher:              hasher,
		Logger:              logger,
	})

	background, stopBackground := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	defer func() {
		stopBackground()
		workers.Wait()
	}()
	startBackgroundCheck(background, &workers, opts, pool, checker, logger)
	workers.Go(func() { sweepExpiredSessions(background, pool, logger) })

	listener, err := net.Listen("tcp", opts.addr.String())
	if err != nil {
		return fmt.Errorf("cannot listen on %s: %w", opts.addr, err)
	}
	logger.Info("listening on http://" + listener.Addr().String())
	return serveUntilSignal(ctx, listener, app.Handler(), logger)
}

// announceSetupToken writes the token to the log, where only someone with
// access to the server can read it. It returns nil once a user exists.
func announceSetupToken(ctx context.Context, pool *sql.DB, logger *slog.Logger) (*auth.SetupToken, error) {
	exist, err := auth.UsersExist(ctx, pool)
	if err != nil {
		return nil, err
	}
	if exist {
		return nil, nil
	}
	token, text := auth.NewSetupToken()
	logger.Warn("no users yet: open the site and create the first admin with setup token " + text)
	return token, nil
}

func startBackgroundCheck(ctx context.Context, workers *sync.WaitGroup, opts options, pool *sql.DB,
	checker *linkcheck.Checker, logger *slog.Logger) {
	if opts.linkCheckInterval <= 0 {
		return
	}
	workers.Go(func() {
		backgroundcheck.Run(ctx, backgroundcheck.Config{
			DB:       pool,
			Checker:  checker,
			Interval: opts.linkCheckInterval,
			Batch:    opts.linkCheckBatch,
			Logger:   logger,
		})
	})
}

// sweepExpiredSessions runs at once, then every hour.
func sweepExpiredSessions(ctx context.Context, pool *sql.DB, logger *slog.Logger) {
	sessions := auth.NewSessions(pool, logger)
	ticker := time.NewTicker(expiredSessionSweepGap)
	defer ticker.Stop()
	for {
		count, err := sessions.DeleteExpired(ctx, auth.UnixNow())
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			logger.Error("cannot delete expired sessions", "err", err)
		default:
			logger.Info("deleted expired sessions", "count", count)
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

// serveUntilSignal serves until ctx ends, then drains for shutdownGrace and
// closes what is left. WriteTimeout stays 0: the check stream is long-lived.
func serveUntilSignal(ctx context.Context, listener net.Listener, handler http.Handler, logger *slog.Logger) error {
	httpServer := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.Serve(listener) }()

	select {
	case err := <-serveErr:
		return fmt.Errorf("server failed: %w", err)
	case <-ctx.Done():
	}
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Warn("shutdown timed out, closing connections")
		if closeErr := httpServer.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			logger.Error("cannot close the server", "err", closeErr)
		}
	}
	return nil
}
