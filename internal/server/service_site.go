package server

import (
	"bytes"
	"context"
	"errors"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/bookmarks"
	"github.com/henry40408/bkd/internal/linkcheck"
	"github.com/henry40408/bkd/internal/sitemeta"
)

const unreadablePageMessage = "could not read the page: "

// refreshBookmark reads the bookmark's page again and keeps what it says about
// itself. It shares the per-user slot of the link check, so one user has one
// outgoing read at a time, whichever feature asked for it.
func (a *App) refreshBookmark(ctx context.Context, user auth.SessionUser, id int64) (bookmarks.Bookmark, *apiError) {
	bookmark, found, err := a.bookmarks.Get(ctx, user.UserID, id)
	if err != nil {
		return bookmarks.Bookmark{}, internal(err)
	}
	if !found {
		return bookmarks.Bookmark{}, notFound()
	}
	release, ok := a.activeChecks.Start(user.UserID)
	if !ok {
		return bookmarks.Bookmark{}, tooManyRequests(checkRetryAfter)
	}
	defer release()
	meta, fault := a.readSite(ctx, bookmark.URL)
	if fault != nil {
		return bookmarks.Bookmark{}, fault
	}
	updated, found, err := a.bookmarks.SetSite(ctx, bookmarks.SiteRequest{
		UserID: user.UserID, ID: id, URL: bookmark.URL,
		Site: bookmarks.Site{Title: meta.Title, Description: meta.Description, FetchedAt: auth.UnixNow()},
	})
	if err != nil {
		return bookmarks.Bookmark{}, internal(err)
	}
	if !found {
		return bookmarks.Bookmark{}, conflict("the bookmark changed while its page was read")
	}
	return updated, nil
}

func (a *App) readSite(ctx context.Context, address string) (sitemeta.Meta, *apiError) {
	page, err := a.linkChecker.ReadPage(ctx, address)
	var unreadable linkcheck.PageError
	if errors.As(err, &unreadable) {
		return sitemeta.Meta{}, badGateway(unreadablePageMessage + unreadable.Error())
	}
	if err != nil {
		return sitemeta.Meta{}, internal(err)
	}
	meta := sitemeta.Parse(bytes.NewReader(page.Body))
	if meta.IsEmpty() {
		return sitemeta.Meta{}, badGateway("the page has no title or description")
	}
	return meta, nil
}
