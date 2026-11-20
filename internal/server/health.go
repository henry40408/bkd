package server

import (
	"context"
	"net/http"
	"time"
)

const healthCheckTimeout = 3 * time.Second

// handleHealthz answers 200 while the database responds, 503 otherwise. It needs
// no session so a proxy or orchestrator can probe it, and it reveals nothing but
// the status.
func (a *App) handleHealthz(w http.ResponseWriter, r *http.Request) {
	header := w.Header()
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Type", "text/plain; charset=utf-8")
	header.Set("X-Content-Type-Options", "nosniff")
	ctx, cancel := context.WithTimeout(r.Context(), healthCheckTimeout)
	defer cancel()
	if err := a.db.PingContext(ctx); err != nil {
		a.logger.Warn("health check failed", "error", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("unavailable\n"))
		return
	}
	_, _ = w.Write([]byte("ok\n"))
}
