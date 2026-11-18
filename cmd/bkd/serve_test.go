package main

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/henry40408/bkd/internal/dbtest"
	"github.com/henry40408/bkd/internal/linkcheck"
)

// syncBuffer lets a test read the log while the code under test still writes.
type syncBuffer struct {
	mu   sync.Mutex
	text bytes.Buffer
}

func (b *syncBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text.Write(data)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text.String()
}

func bufferLogger() (*slog.Logger, *syncBuffer) {
	log := &syncBuffer{}
	return slog.New(slog.NewTextHandler(log, nil)), log
}

func TestSetupTokenIsNotAnnouncedWhenTheUserCheckFails(t *testing.T) {
	pool := dbtest.Open(t)
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	logger, log := bufferLogger()

	token, err := announceSetupToken(context.Background(), pool, logger)

	if err == nil || token != nil {
		t.Fatalf("token = %v, err = %v, want no token and an error", token, err)
	}
	if strings.Contains(log.String(), "setup token") {
		t.Fatalf("a token was logged although it could not be used: %s", log.String())
	}
}

func TestExpiredSessionSweepLogsAFailureAndStopsWithItsContext(t *testing.T) {
	pool := dbtest.Open(t)
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	logger, log := bufferLogger()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sweepExpiredSessions(ctx, pool, logger)
		close(done)
	}()

	waitUntil(t, "the failure is logged", func() bool {
		return strings.Contains(log.String(), "cannot delete expired sessions")
	})
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the sweep did not stop when its context ended")
	}
}

func TestBackgroundCheckStartsOnlyWhenAnIntervalIsSet(t *testing.T) {
	pool := dbtest.Open(t)
	checker := linkcheck.New(linkcheck.Config{})
	logger, _ := bufferLogger()
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup

	startBackgroundCheck(ctx, &workers, options{}, pool, checker, logger)
	workers.Wait() // returns at once: no worker was started

	startBackgroundCheck(ctx, &workers, options{linkCheckInterval: time.Hour, linkCheckBatch: 1}, pool, checker, logger)
	cancel()
	finished := make(chan struct{})
	go func() {
		workers.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the background check did not stop when its context ended")
	}
}

func TestAServerThatCannotServeReportsItAndDoesNotWaitForASignal(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	logger, _ := bufferLogger()

	err = serveUntilSignal(context.Background(), listener, http.NewServeMux(), logger)

	if err == nil || !strings.Contains(err.Error(), "server failed") {
		t.Fatalf("err = %v, want one that says the server failed", err)
	}
}

func waitUntil(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
