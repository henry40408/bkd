package server

import (
	"bytes"
	"context"
	"errors"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/bookmarks"
	"github.com/henry40408/bkd/internal/linkcheck"
	"github.com/henry40408/bkd/internal/sitemeta"
	"github.com/henry40408/bkd/internal/users"
)

const unreadablePageMessage = "could not read the page: "

// sitePreview reads the page at address and says what it tells about itself. The
// site is told the User-Agent the user chose for their link checks. It shares the
// per-user slot of the link check, so one user has one outgoing read at a time,
// whichever feature asked for it. A page that says nothing is not an error.
func (a *App) sitePreview(ctx context.Context, user auth.SessionUser, address string) (bookmarks.Site, *apiError) {
	release, ok := a.activeChecks.Start(user.UserID)
	if !ok {
		return bookmarks.Site{}, tooManyRequests(checkRetryAfter)
	}
	defer release()
	agent, err := users.LinkCheckUserAgent(ctx, a.db, user.UserID)
	if err != nil {
		return bookmarks.Site{}, internal(err)
	}
	page, err := a.linkChecker.ReadPage(ctx, address, agent)
	var unreadable linkcheck.PageError
	if errors.As(err, &unreadable) {
		return bookmarks.Site{}, badGateway(unreadablePageMessage + unreadable.Error())
	}
	if err != nil {
		return bookmarks.Site{}, internal(err)
	}
	meta := sitemeta.Parse(bytes.NewReader(page.Body))
	// Cut to what a bookmark keeps, so that the placeholders show the text that is saved.
	return bookmarks.Site{Title: meta.Title, Description: meta.Description}.Fitted(), nil
}
