package server

import (
	"context"
	"errors"
	"log/slog"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/bookmarks"
	"github.com/henry40408/bkd/internal/importer"
	"github.com/henry40408/bkd/internal/users"
)

// validationFault turns a rejected input into a 400 with its reason. Anything
// else is a fault of the server.
func validationFault(err error) *apiError {
	var reason bookmarks.ValidationError
	if errors.As(err, &reason) {
		return badRequest(reason.Error())
	}
	return internal(err)
}

func (a *App) createBookmark(ctx context.Context, user auth.SessionUser, input bookmarks.Input) (bookmarks.Bookmark, *apiError) {
	valid, err := input.Validate()
	if err != nil {
		return bookmarks.Bookmark{}, validationFault(err)
	}
	created, err := a.bookmarks.Create(ctx, user.UserID, valid, auth.UnixNow())
	if err != nil {
		return bookmarks.Bookmark{}, internal(err)
	}
	return created, nil
}

func (a *App) updateBookmark(ctx context.Context, user auth.SessionUser, id int64, input bookmarks.Input) (bookmarks.Bookmark, *apiError) {
	valid, err := input.Validate()
	if err != nil {
		return bookmarks.Bookmark{}, validationFault(err)
	}
	updated, found, err := a.bookmarks.Update(ctx, bookmarks.UpdateRequest{
		UserID: user.UserID, ID: id, Bookmark: valid, Now: auth.UnixNow(),
	})
	if err != nil {
		return bookmarks.Bookmark{}, internal(err)
	}
	if !found {
		return bookmarks.Bookmark{}, notFound()
	}
	return updated, nil
}

func (a *App) deleteBookmark(ctx context.Context, user auth.SessionUser, id int64) *apiError {
	deleted, err := a.bookmarks.Delete(ctx, user.UserID, id)
	if err != nil {
		return internal(err)
	}
	if !deleted {
		return notFound()
	}
	return nil
}

// bulkBookmarks returns how many bookmarks changed.
func (a *App) bulkBookmarks(ctx context.Context, user auth.SessionUser, input bookmarks.BulkInput) (int, *apiError) {
	valid, err := input.Validate()
	if err != nil {
		return 0, validationFault(err)
	}
	changed, err := a.bookmarks.ApplyBulk(ctx, user.UserID, valid, auth.UnixNow())
	switch {
	case errors.Is(err, bookmarks.ErrBulkNotFound):
		return 0, notFound()
	case errors.Is(err, bookmarks.ErrBulkTooManyTags):
		return 0, badRequest("a bookmark would get too many tags")
	case err != nil:
		return 0, internal(err)
	}
	return changed, nil
}

func (a *App) setLinkCheckEnabled(ctx context.Context, user auth.SessionUser, enabled bool) *apiError {
	if err := users.SetLinkCheckEnabled(ctx, a.db, user.UserID, enabled); err != nil {
		return internal(err)
	}
	a.logger.Info("background link check setting changed",
		slog.Int64("user_id", user.UserID), slog.Bool("enabled", enabled))
	return nil
}

func (a *App) linkCheckEnabled(ctx context.Context, user auth.SessionUser) (bool, *apiError) {
	enabled, err := users.LinkCheckEnabled(ctx, a.db, user.UserID)
	if err != nil {
		return false, internal(err)
	}
	return enabled, nil
}

func (a *App) importBookmarks(ctx context.Context, user auth.SessionUser, html string) (importer.Report, *apiError) {
	report, err := importer.ImportHTML(ctx, a.bookmarks, importer.Request{
		UserID: user.UserID, HTML: html, Now: auth.UnixNow(),
	})
	if err != nil {
		return importer.Report{}, internal(err)
	}
	return report, nil
}
