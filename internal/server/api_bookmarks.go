package server

import (
	"net/http"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/bookmarks"
)

func (a *App) handleListBookmarks(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	limit, failure := optionalUint32(r, "limit")
	if failure != nil {
		failure.write(w)
		return
	}
	offset, failure := optionalUint32(r, "offset")
	if failure != nil {
		failure.write(w)
		return
	}
	list, err := a.bookmarks.List(r.Context(), user.UserID, filterOf(r), bookmarks.NewPage(limit, offset))
	if err != nil {
		a.writeAPIError(w, internal(err))
		return
	}
	if list == nil {
		list = []bookmarks.Bookmark{}
	}
	writeJSON(w, http.StatusOK, struct {
		Bookmarks []bookmarks.Bookmark `json:"bookmarks"`
	}{list})
}

func (a *App) handleRandomBookmark(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	picked, found, err := a.bookmarks.Random(r.Context(), user.UserID, filterOf(r))
	if err != nil {
		a.writeAPIError(w, internal(err))
		return
	}
	if !found {
		a.writeAPIError(w, notFound())
		return
	}
	writeJSON(w, http.StatusOK, picked)
}

// bookmarkRequest is what a client sends. A missing url or title is a 422, a
// missing note or tag list is empty.
type bookmarkRequest struct {
	URL   *string  `json:"url"`
	Title *string  `json:"title"`
	Note  string   `json:"note"`
	Tags  []string `json:"tags"`
}

func decodeBookmark(r *http.Request) (bookmarks.Input, *textError) {
	var request bookmarkRequest
	if failure := decodeJSON(r, &request); failure != nil {
		return bookmarks.Input{}, failure
	}
	if failure := requireFields(field{"url", request.URL != nil}, field{"title", request.Title != nil}); failure != nil {
		return bookmarks.Input{}, failure
	}
	return bookmarks.Input{URL: *request.URL, Title: *request.Title, Note: request.Note, Tags: request.Tags}, nil
}

func (a *App) handleCreateBookmark(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	input, failure := decodeBookmark(r)
	if failure != nil {
		failure.write(w)
		return
	}
	created, fault := a.createBookmark(r.Context(), user, input)
	if fault != nil {
		a.writeAPIError(w, fault)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (a *App) handleUpdateBookmark(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	id, failure := pathID(r)
	if failure != nil {
		failure.write(w)
		return
	}
	input, failure := decodeBookmark(r)
	if failure != nil {
		failure.write(w)
		return
	}
	updated, fault := a.updateBookmark(r.Context(), user, id, input)
	if fault != nil {
		a.writeAPIError(w, fault)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (a *App) handleDeleteBookmark(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	id, failure := pathID(r)
	if failure != nil {
		failure.write(w)
		return
	}
	if fault := a.deleteBookmark(r.Context(), user, id); fault != nil {
		a.writeAPIError(w, fault)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleTags(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	tags, err := a.bookmarks.TagCounts(r.Context(), user.UserID)
	if err != nil {
		a.writeAPIError(w, internal(err))
		return
	}
	links, err := a.bookmarks.LinkCounts(r.Context(), user.UserID)
	if err != nil {
		a.writeAPIError(w, internal(err))
		return
	}
	if tags == nil {
		tags = []bookmarks.TagCount{}
	}
	if links == nil {
		links = []bookmarks.LinkCount{}
	}
	writeJSON(w, http.StatusOK, struct {
		Tags  []bookmarks.TagCount  `json:"tags"`
		Links []bookmarks.LinkCount `json:"links"`
	}{tags, links})
}

func (a *App) handleBulkBookmarks(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	var input bookmarks.BulkInput
	if failure := decodeJSON(r, &input); failure != nil {
		failure.write(w)
		return
	}
	changed, fault := a.bulkBookmarks(r.Context(), user, input)
	if fault != nil {
		a.writeAPIError(w, fault)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Changed int `json:"changed"`
	}{changed})
}

func (a *App) handleGetSettings(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	enabled, fault := a.linkCheckEnabled(r.Context(), user)
	if fault != nil {
		a.writeAPIError(w, fault)
		return
	}
	writeJSON(w, http.StatusOK, settingsView{LinkCheckEnabled: enabled})
}

func (a *App) handlePutSettings(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	var request struct {
		LinkCheckEnabled *bool `json:"link_check_enabled"`
	}
	if failure := decodeJSON(r, &request); failure != nil {
		failure.write(w)
		return
	}
	if failure := requireFields(field{"link_check_enabled", request.LinkCheckEnabled != nil}); failure != nil {
		failure.write(w)
		return
	}
	if fault := a.setLinkCheckEnabled(r.Context(), user, *request.LinkCheckEnabled); fault != nil {
		a.writeAPIError(w, fault)
		return
	}
	writeJSON(w, http.StatusOK, settingsView{LinkCheckEnabled: *request.LinkCheckEnabled})
}
