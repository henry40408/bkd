package server

import (
	"context"
	"net/http"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/bookmarks"
	"github.com/henry40408/bkd/internal/linkcheck"
)

// readPage is a page that was read. Site is null when the page says nothing
// about itself, in which case nothing was stored.
type readPage struct {
	ID         int64           `json:"id"`
	Site       *bookmarks.Site `json:"site"`
	NoteFilled bool            `json:"note_filled"`
}

type unreadablePage struct {
	ID     int64  `json:"id"`
	Detail string `json:"detail"`
}

// handleReadPages answers like handleCheck, with a stream of events: result when a
// page was read (its details are stored, and a blank note takes the description),
// failed when it could not be, skipped when its site asked to be left alone or
// stopped answering, then done. It uses the same ids, limits and one slot per
// user as the link check.
func (a *App) handleReadPages(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	targets, release, ok := a.claimTargets(w, r, user)
	if !ok {
		return
	}
	defer release()
	a.streamEvents(r.Context(), w, eventStream{
		start: func(ctx context.Context) *linkcheck.RunningCheck { return a.linkChecker.StartReads(ctx, targets, 0) },
		send: func(ctx context.Context, stream *sseWriter, event linkcheck.Event) bool {
			return a.sendReadEvent(ctx, stream, user, event)
		},
	})
}

// sendReadEvent reports false when the stream must end.
func (a *App) sendReadEvent(ctx context.Context, stream *sseWriter, user auth.SessionUser, event linkcheck.Event) bool {
	if event.Skipped != nil {
		skipped := event.Skipped
		return stream.event("skipped", skippedLink{ID: skipped.Target.ID, Reason: skipped.Reason, Host: skipped.Host}) == nil
	}
	checked := *event.Checked
	if checked.Meta == nil {
		return stream.event("failed", unreadablePage{ID: checked.Target.ID, Detail: checked.Verdict.Detail}) == nil
	}
	stored, err := a.storeRead(ctx, user, checked)
	if err != nil {
		if ctx.Err() == nil {
			a.logger.Error("storing page details failed", "error", err)
			_ = stream.event("error", struct{}{})
		}
		return false
	}
	// A bookmark edited during the read was not stored, so no result is sent.
	if !stored.found {
		return true
	}
	return stream.event("result", readPage{ID: checked.Target.ID, Site: stored.site, NoteFilled: stored.noteFilled}) == nil
}

// storedRead is what became of a page that was read.
type storedRead struct {
	// found is false when the bookmark was edited or deleted during the read.
	found bool
	// site is null when the page said nothing.
	site       *bookmarks.Site
	noteFilled bool
}

// storeRead saves the details of one page that was read, and does nothing for one
// that could not be. The request's cancellation does not reach it: the site was
// already asked, so a client that leaves must not make the database throw that
// answer away.
func (a *App) storeRead(ctx context.Context, user auth.SessionUser, checked linkcheck.Checked) (storedRead, error) {
	if checked.Meta == nil {
		return storedRead{found: true}, nil
	}
	site := bookmarks.Site{Title: checked.Meta.Title, Description: checked.Meta.Description}.Fitted()
	if site == (bookmarks.Site{}) {
		return storedRead{found: true}, nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), storeResultTimeout)
	defer cancel()
	stored, err := a.bookmarks.SetSite(ctx, bookmarks.SetSiteRequest{
		UserID: user.UserID, ID: checked.Target.ID, URL: checked.Target.URL, Site: site,
	})
	if err != nil {
		return storedRead{}, err
	}
	return storedRead{found: stored.Stored, site: &site, noteFilled: stored.NoteFilled}, nil
}
