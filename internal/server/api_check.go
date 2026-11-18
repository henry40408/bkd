package server

import (
	"context"
	"net/http"
	"time"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/bookmarks"
	"github.com/henry40408/bkd/internal/linkcheck"
)

// keepAliveInterval is axum's default for the Rust server.
const keepAliveInterval = 15 * time.Second

// checkRequest has a pointer because a missing "ids" is a 422, not an empty list.
type checkRequest struct {
	IDs *[]int64 `json:"ids"`
}

type checkedLink struct {
	ID   int64               `json:"id"`
	Link bookmarks.LinkCheck `json:"link"`
}

type skippedLink struct {
	ID     int64                `json:"id"`
	Reason linkcheck.SkipReason `json:"reason"`
	Host   string               `json:"host"`
}

// handleCheck answers with a server-sent event stream: one result event as each
// link is checked (or skipped, when its site asked to be left alone or stopped
// answering), then done. Each result is saved as it arrives, so a client that
// stops early keeps what was already found.
func (a *App) handleCheck(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	var request checkRequest
	if failure := decodeJSON(r, &request); failure != nil {
		failure.write(w)
		return
	}
	if request.IDs == nil {
		missingField("ids").write(w)
		return
	}
	valid, err := linkcheck.CheckInput{IDs: *request.IDs}.Validate()
	if err != nil {
		a.writeAPIError(w, validationFault(err))
		return
	}
	release, ok := a.activeChecks.Start(user.UserID)
	if !ok {
		a.writeAPIError(w, tooManyRequests(checkRetryAfter))
		return
	}
	defer release()
	targets, found, err := a.linkStore.Targets(r.Context(), user.UserID, valid)
	if err != nil {
		a.writeAPIError(w, internal(err))
		return
	}
	if !found {
		a.writeAPIError(w, notFound())
		return
	}
	a.streamCheck(r.Context(), w, user, targets)
}

// streamCheck returns when the check is over or the client left. Either way the
// check is cancelled, so no request outlives the stream.
func (a *App) streamCheck(ctx context.Context, w http.ResponseWriter, user auth.SessionUser, targets []linkcheck.Target) {
	stream, err := startSSE(w)
	if err != nil {
		return
	}
	running := a.linkChecker.Start(ctx, targets)
	defer running.Cancel()
	ticker := time.NewTicker(a.keepAlive)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if stream.keepAlive() != nil {
				return
			}
		case event, open := <-running.Events:
			if !open {
				if ctx.Err() == nil {
					_ = stream.event("done", struct{}{})
				}
				return
			}
			if !a.sendCheckEvent(ctx, stream, user, event) {
				return
			}
		}
	}
}

// sendCheckEvent reports false when the stream must end.
func (a *App) sendCheckEvent(ctx context.Context, stream *sseWriter, user auth.SessionUser, event linkcheck.Event) bool {
	if event.Skipped != nil {
		skipped := event.Skipped
		return stream.event("skipped", skippedLink{ID: skipped.Target.ID, Reason: skipped.Reason, Host: skipped.Host}) == nil
	}
	stored, err := a.storeChecked(ctx, user, *event.Checked)
	if err != nil {
		if ctx.Err() == nil {
			a.logger.Error("storing a link check failed", "error", err)
			_ = stream.event("error", struct{}{})
		}
		return false
	}
	// A bookmark edited during the check was not stored, so no result is sent.
	if len(stored) == 0 {
		return true
	}
	return stream.event("result", checkedLink{ID: stored[0].ID, Link: stored[0].Check}) == nil
}

// storeResultTimeout bounds a save that no longer follows the request.
const storeResultTimeout = 5 * time.Second

// storeChecked saves one finished check. The request's cancellation does not
// reach it: the site was already asked, so a client that leaves must not make
// the database throw that answer away.
func (a *App) storeChecked(ctx context.Context, user auth.SessionUser, checked linkcheck.Checked) ([]linkcheck.Stored, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), storeResultTimeout)
	defer cancel()
	return a.linkStore.Store(ctx, linkcheck.StoreRequest{
		UserID: user.UserID, Checked: []linkcheck.Checked{checked}, Now: auth.UnixNow(),
	})
}
