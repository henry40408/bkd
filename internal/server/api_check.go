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
	targets, release, ok := a.claimTargets(w, r, user)
	if !ok {
		return
	}
	defer release()
	a.streamEvents(r.Context(), w, eventStream{
		start: func(ctx context.Context) *linkcheck.RunningCheck { return a.linkChecker.Start(ctx, targets) },
		send: func(ctx context.Context, stream *sseWriter, event linkcheck.Event) bool {
			return a.sendCheckEvent(ctx, stream, user, event)
		},
	})
}

// claimTargets reads the ids the client sent, takes the user's one slot for
// outgoing requests and looks the bookmarks up. When ok is false the answer was
// written; otherwise the caller must call release when the work is over.
func (a *App) claimTargets(
	w http.ResponseWriter, r *http.Request, user auth.SessionUser,
) (targets []linkcheck.Target, release func(), ok bool) {
	var request checkRequest
	if failure := decodeJSON(r, &request); failure != nil {
		failure.write(w)
		return nil, nil, false
	}
	if request.IDs == nil {
		missingField("ids").write(w)
		return nil, nil, false
	}
	valid, err := linkcheck.CheckInput{IDs: *request.IDs}.Validate()
	if err != nil {
		a.writeAPIError(w, validationFault(err))
		return nil, nil, false
	}
	release, taken := a.activeChecks.Start(user.UserID)
	if !taken {
		a.writeAPIError(w, tooManyRequests(checkRetryAfter))
		return nil, nil, false
	}
	targets, found, err := a.linkStore.Targets(r.Context(), user.UserID, valid)
	if err != nil {
		release()
		a.writeAPIError(w, internal(err))
		return nil, nil, false
	}
	if !found {
		release()
		a.writeAPIError(w, notFound())
		return nil, nil, false
	}
	return targets, release, true
}

// eventStream is what differs between the stream of a link check and that of a
// page read: how the run starts and what becomes of each event.
type eventStream struct {
	start func(context.Context) *linkcheck.RunningCheck
	// send reports false when the stream must end.
	send func(context.Context, *sseWriter, linkcheck.Event) bool
}

// streamEvents returns when the run is over or the client left. Either way the
// run is cancelled, so no request outlives the stream.
func (a *App) streamEvents(ctx context.Context, w http.ResponseWriter, source eventStream) {
	stream, err := startSSE(w)
	if err != nil {
		return
	}
	ctx, endRun := a.endsWhenStopping(ctx)
	defer endRun()
	running := source.start(ctx)
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
			if !source.send(ctx, stream, event) {
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

// endsWhenStopping lets a long check end when the process is asked to stop, so
// that a graceful shutdown does not wait for it.
func (a *App) endsWhenStopping(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	stopWatching := context.AfterFunc(a.stopping, cancel)
	return ctx, func() {
		stopWatching()
		cancel()
	}
}
