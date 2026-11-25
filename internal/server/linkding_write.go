package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/bookmarks"
)

// The write half of the linkding-compatible API. A request names only the fields
// it wants to change, in linkding's words (description is bkd's site description,
// notes is bkd's note, tag_names its tags); a field left out keeps its value, and
// that is the one rule POST on a known address, PUT and PATCH share.
//
// What bkd has no counterpart for (is_archived, unread, shared, and the query
// parameter disable_scraping, because bkd never reads a page while it saves a
// bookmark) is accepted and ignored, so a client that always sends them works.

const (
	messageRequired  = "This field is required."
	messageBlank     = "This field may not be blank."
	messageBadURL    = "Enter a valid URL."
	messageTooLong   = "This field is too long."
	messageDuplicate = "A bookmark with this URL already exists."
)

// linkdingChange is a request body. A pointer tells a field that was sent from
// one that was not; null counts as not sent.
type linkdingChange struct {
	URL         *string   `json:"url"`
	Title       *string   `json:"title"`
	Description *string   `json:"description"`
	Notes       *string   `json:"notes"`
	TagNames    *[]string `json:"tag_names"`
}

func fieldError(field, message string) *apiError {
	return &apiError{status: http.StatusBadRequest, field: field, message: message}
}

func decodeLinkdingChange(r *http.Request) (linkdingChange, *apiError) {
	var change linkdingChange
	if failure := decodeJSON(r, &change); failure != nil {
		return linkdingChange{}, &apiError{status: failure.status, message: failure.message}
	}
	return change, nil
}

// address is the url of a request that has to name one.
func (c linkdingChange) address() (string, *apiError) {
	if c.URL == nil {
		return "", fieldError("url", messageRequired)
	}
	address := strings.TrimSpace(*c.URL)
	if address == "" {
		return "", fieldError("url", messageBlank)
	}
	return address, nil
}

// linkdingFaultOf puts a failure of the shared bookmark rules in linkding's words:
// the field a 400 is about, and "Not found." for a bookmark that is not there.
func linkdingFaultOf(fault *apiError) *apiError {
	if fault.status == http.StatusNotFound {
		return detailNotFound()
	}
	if fault.status != http.StatusBadRequest {
		return fault
	}
	switch fault.message {
	case "url is too long":
		return fieldError("url", messageTooLong)
	case "url must start with http:// or https://", "url is not valid":
		return fieldError("url", messageBadURL)
	case "title is too long":
		return fieldError("title", messageTooLong)
	case "note is too long":
		return fieldError("notes", messageTooLong)
	case "tag is too long":
		return fieldError("tag_names", "A tag name is too long.")
	case "tag contains a control character":
		return fieldError("tag_names", "A tag name may not contain control characters.")
	case "too many tags":
		return fieldError("tag_names", "Too many tags.")
	}
	return fault
}

func linkdingIDOf(r *http.Request) (int64, *apiError) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return 0, detailNotFound()
	}
	return id, nil
}

// linkdingCreateBookmark saves a bookmark, or, when the address is already
// bookmarked, changes that bookmark and answers it, as linkding does.
func (a *App) linkdingCreateBookmark(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	change, fault := decodeLinkdingChange(r)
	if fault != nil {
		a.writeDetailError(w, fault)
		return
	}
	address, fault := change.address()
	if fault != nil {
		a.writeDetailError(w, fault)
		return
	}
	existing, found, err := a.bookmarks.FindByURL(r.Context(), user.UserID, address)
	if err != nil {
		a.writeDetailError(w, internal(err))
		return
	}
	var saved bookmarks.Bookmark
	if found {
		change.URL = nil // the stored spelling stays
		saved, fault = a.changeLinkdingBookmark(r.Context(), user, existing, change)
	} else {
		saved, fault = a.createLinkdingBookmark(r.Context(), user, address, change)
	}
	if fault != nil {
		a.writeDetailError(w, linkdingFaultOf(fault))
		return
	}
	writeJSON(w, http.StatusCreated, linkdingBookmarkOf(saved))
}

func (a *App) createLinkdingBookmark(ctx context.Context, user auth.SessionUser, address string, change linkdingChange) (bookmarks.Bookmark, *apiError) {
	return a.createBookmark(ctx, user, bookmarks.Input{
		URL:   address,
		Title: textOr(change.Title, bookmarks.TitleFromURL(address)),
		Note:  sentOr(change.Notes, ""),
		Tags:  tagsOr(change.TagNames, nil),
		Site:  bookmarks.Site{Description: sentOr(change.Description, "")},
	})
}

// linkdingChangeBookmark is PUT, which needs the address, and PATCH, which does
// not. Both leave out what the request leaves out.
func (a *App) linkdingChangeBookmark(requireURL bool) userHandler {
	return func(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
		id, fault := linkdingIDOf(r)
		if fault != nil {
			a.writeDetailError(w, fault)
			return
		}
		existing, found, err := a.bookmarks.Get(r.Context(), user.UserID, id)
		if err != nil {
			a.writeDetailError(w, internal(err))
			return
		}
		if !found {
			a.writeDetailError(w, detailNotFound())
			return
		}
		change, fault := decodeLinkdingChange(r)
		if fault != nil {
			a.writeDetailError(w, fault)
			return
		}
		if requireURL || change.URL != nil {
			address, fault := change.address()
			if fault != nil {
				a.writeDetailError(w, fault)
				return
			}
			change.URL = &address
		}
		saved, fault := a.changeLinkdingBookmark(r.Context(), user, existing, change)
		if fault != nil {
			a.writeDetailError(w, linkdingFaultOf(fault))
			return
		}
		writeJSON(w, http.StatusOK, linkdingBookmarkOf(saved))
	}
}

// changeLinkdingBookmark stores the stored bookmark with the fields of the request
// over it. Two things differ from a plain bkd update:
//   - A blank title keeps the stored one: a bkd bookmark always has a title.
//   - A description in the request replaces the page's description even when it is
//     empty, which clears it; the page's title is kept unless the address changed,
//     because the old title describes another page.
func (a *App) changeLinkdingBookmark(ctx context.Context, user auth.SessionUser, existing bookmarks.Bookmark, change linkdingChange) (bookmarks.Bookmark, *apiError) {
	input := bookmarks.Input{
		URL:   textOr(change.URL, existing.URL),
		Title: textOr(change.Title, existing.Title),
		Note:  sentOr(change.Notes, existing.Note),
		Tags:  tagsOr(change.TagNames, existing.Tags),
	}
	moved := input.URL != existing.URL
	if moved {
		if fault := a.refuseDuplicate(ctx, user, existing.ID, input.URL); fault != nil {
			return bookmarks.Bookmark{}, fault
		}
	}
	if change.Description != nil {
		input.Site = bookmarks.Site{Description: *change.Description}
		if !moved {
			input.Site.Title = existing.Site.Value().Title
		}
	}
	valid, err := input.Validate()
	if err != nil {
		return bookmarks.Bookmark{}, validationFault(err)
	}
	updated, found, err := a.bookmarks.Update(ctx, bookmarks.UpdateRequest{
		UserID: user.UserID, ID: existing.ID, Bookmark: valid, Now: auth.UnixNow(),
		ReplaceSite: change.Description != nil,
	})
	if err != nil {
		return bookmarks.Bookmark{}, internal(err)
	}
	if !found {
		return bookmarks.Bookmark{}, notFound()
	}
	return updated, nil
}

// refuseDuplicate is linkding's rule that a change cannot give a bookmark the
// address of another one. bkd itself allows it, so a bookmark that got there
// another way is left alone until its address is changed.
func (a *App) refuseDuplicate(ctx context.Context, user auth.SessionUser, id int64, address string) *apiError {
	other, found, err := a.bookmarks.FindByURL(ctx, user.UserID, address)
	if err != nil {
		return internal(err)
	}
	if found && other.ID != id {
		return fieldError("url", messageDuplicate)
	}
	return nil
}

func (a *App) linkdingDeleteBookmark(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	id, fault := linkdingIDOf(r)
	if fault != nil {
		a.writeDetailError(w, fault)
		return
	}
	if fault := a.deleteBookmark(r.Context(), user, id); fault != nil {
		a.writeDetailError(w, linkdingFaultOf(fault))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sentOr is the text of a field that was sent, even an empty one, or the fallback
// for one that was not.
func sentOr(sent *string, fallback string) string {
	if sent == nil {
		return fallback
	}
	return *sent
}

// textOr is the same, but a blank text counts as not sent, for a field that
// cannot be empty.
func textOr(sent *string, fallback string) string {
	if sent == nil || strings.TrimSpace(*sent) == "" {
		return fallback
	}
	return *sent
}

func tagsOr(sent *[]string, fallback []string) []string {
	if sent == nil {
		return fallback
	}
	return *sent
}
