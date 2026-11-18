// Package server is the HTTP layer: a JSON API under /api, server-rendered pages
// and the front-end files.
package server

import (
	"database/sql"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/bookmarks"
	"github.com/henry40408/bkd/internal/linkcheck"
)

const (
	// A bookmark is a few kilobytes at most.
	maxBodyBytes = 64 * 1024
	// A bookmark file from a browser: tens of thousands of links.
	maxImportBytes = 20 * 1024 * 1024

	loginAttemptsPerWindow = 10
	setupAttemptsPerWindow = 5
	rateLimitWindow        = time.Minute
)

// Config is everything App needs from the outside.
type Config struct {
	DB *sql.DB
	// SetupToken is set only while no user exists. It stays in memory, never stored.
	SetupToken *auth.SetupToken
	// SecureCookies marks the session cookie Secure. Turn it on when the site is
	// served over HTTPS.
	SecureCookies bool
	// ForwardAuth is set when a trusted proxy may sign users in.
	ForwardAuth *auth.ForwardAuth
	LinkChecker *linkcheck.Checker
	// BackgroundLinkCheck tells the settings page that the administrator started
	// the server with the background link check on.
	BackgroundLinkCheck bool
	// Hasher is shared with the start-up warm-up. A zero value makes a new one.
	Hasher *auth.Hasher
	// Logger may be nil.
	Logger *slog.Logger
}

// App holds the state every request shares.
type App struct {
	db            *sql.DB
	logger        *slog.Logger
	hasher        *auth.Hasher
	sessions      *auth.Sessions
	bookmarks     *bookmarks.Store
	linkStore     *linkcheck.Store
	linkChecker   *linkcheck.Checker
	activeChecks  *linkcheck.ActiveChecks
	forwardAuth   *auth.ForwardAuth
	loginLimiter  *auth.Limiter
	setupLimiter  *auth.Limiter
	secureCookies bool
	backgroundOn  bool
	// checkLimit is how long a page waits for a link check (tests shorten it).
	checkLimit time.Duration
	// bodyTimeout is how long a request has to deliver its body (tests shorten it).
	bodyTimeout time.Duration
	// keepAlive is how often a quiet check stream writes a comment (tests shorten it).
	keepAlive time.Duration

	setupMu    sync.Mutex
	setupToken *auth.SetupToken
}

// New builds the App. Call Handler for the routes.
func New(config Config) *App {
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	hasher := config.Hasher
	if hasher == nil {
		hasher = auth.NewHasher()
	}
	return &App{
		db:            config.DB,
		logger:        logger,
		hasher:        hasher,
		sessions:      auth.NewSessions(config.DB, logger),
		bookmarks:     bookmarks.NewStore(config.DB),
		linkStore:     linkcheck.NewStore(config.DB),
		linkChecker:   config.LinkChecker,
		activeChecks:  &linkcheck.ActiveChecks{},
		forwardAuth:   config.ForwardAuth,
		loginLimiter:  auth.NewLimiter(loginAttemptsPerWindow, rateLimitWindow),
		setupLimiter:  auth.NewLimiter(setupAttemptsPerWindow, rateLimitWindow),
		secureCookies: config.SecureCookies,
		backgroundOn:  config.BackgroundLinkCheck,
		checkLimit:    noScriptCheckLimit,
		bodyTimeout:   requestBodyTimeout,
		keepAlive:     keepAliveInterval,
		setupToken:    config.SetupToken,
	}
}

// currentSetupToken is nil once setup is over. The token itself is immutable,
// so no lock is held while it is used.
func (a *App) currentSetupToken() *auth.SetupToken {
	a.setupMu.Lock()
	defer a.setupMu.Unlock()
	return a.setupToken
}

func (a *App) clearSetupToken() {
	a.setupMu.Lock()
	defer a.setupMu.Unlock()
	a.setupToken = nil
}
