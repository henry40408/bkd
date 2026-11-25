package server

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/bookmarks"
	"github.com/henry40408/bkd/internal/search"
)

// mountLinkding routes the linkding-compatible API. A route here is answered only
// to a token (see linkding_auth.go), and the paths keep linkding's trailing slash.
func (a *App) mountLinkding(mux *http.ServeMux) {
	route := func(path string, handlers ...methodHandler) {
		mux.Handle(path, limitBody(maxBodyBytes, a.bodyTimeout, apiHeaders(byMethod(handlers...))))
	}
	token := a.tokenGuarded
	route("/api/bookmarks/{$}", onGet(token(a.linkdingListBookmarks)), onPost(token(a.linkdingCreateBookmark)))
	route("/api/bookmarks/check/{$}", onGet(token(a.linkdingCheckBookmark)))
	route("/api/bookmarks/archived/{$}", onGet(token(a.linkdingListNothing)))
	route("/api/bookmarks/shared/{$}", onGet(token(a.linkdingListNothing)))
	route("/api/bookmarks/{id}/{$}",
		onGet(token(a.linkdingGetBookmark)),
		onPut(token(a.linkdingChangeBookmark(true))),
		onPatch(token(a.linkdingChangeBookmark(false))),
		onDelete(token(a.linkdingDeleteBookmark)))
	route("/api/tags/{$}", onGet(token(a.linkdingListTags)))
}

// linkdingListNothing answers the lists bkd has nothing for: it has no archive,
// and no bookmark is shared with other users. An empty list is true, and a client
// with an "archived" tab shows an empty tab instead of an error.
func (a *App) linkdingListNothing(w http.ResponseWriter, r *http.Request, _ auth.SessionUser) {
	paging := linkdingPagingOf(r, linkdingMaxBookmarkLimit)
	writeJSON(w, http.StatusOK, newLinkdingList(r, a.publicScheme(r), paging, 0, []linkdingBookmark{}))
}

func (a *App) linkdingListBookmarks(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	filter, fault := linkdingFilterOf(r)
	if fault != nil {
		a.writeDetailError(w, fault)
		return
	}
	paging := linkdingPagingOf(r, linkdingMaxBookmarkLimit)
	count, err := a.bookmarks.Count(r.Context(), user.UserID, filter)
	if err != nil {
		a.writeDetailError(w, internal(err))
		return
	}
	found, err := a.bookmarks.List(r.Context(), user.UserID, filter, paging.page())
	if err != nil {
		a.writeDetailError(w, internal(err))
		return
	}
	results := make([]linkdingBookmark, len(found))
	for i, bookmark := range found {
		results[i] = linkdingBookmarkOf(bookmark)
	}
	writeJSON(w, http.StatusOK, newLinkdingList(r, a.publicScheme(r), paging, count, results))
}

// linkdingFilterOf reads q, in bkd's search syntax, and the two time bounds.
func linkdingFilterOf(r *http.Request) (bookmarks.Filter, *apiError) {
	added, fault := linkdingTimeParameter(r, "added_since")
	if fault != nil {
		return nil, fault
	}
	modified, fault := linkdingTimeParameter(r, "modified_since")
	if fault != nil {
		return nil, fault
	}
	inner := search.NewFilter(r.URL.Query().Get("q"), "")
	return bookmarks.Since{Inner: inner, AddedAfter: added, ModifiedAfter: modified}, nil
}

// linkdingTimeLayouts are the spellings of a time that clients send: the one
// linkding writes, the same without a zone (taken as UTC), without seconds, with
// an offset that has no colon, and a plain date.
var linkdingTimeLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04",
	"2006-01-02T15:04:05-0700",
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02",
}

// unencodedPlus finds an offset whose "+" a client did not encode, so the query
// string turned it into a space: "2024-01-01T00:00:00 02:00".
var unencodedPlus = regexp.MustCompile(`^(\d{4}-\d\d-\d\d[T ]\d\d:\d\d:\d\d(?:\.\d+)?) (\d\d:\d\d)$`)

// linkdingTimeParameter reads a time bound as whole Unix seconds, rounded down,
// which is how finely bkd keeps times. nil is no bound.
func linkdingTimeParameter(r *http.Request, name string) (*int64, *apiError) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return nil, nil
	}
	raw = unencodedPlus.ReplaceAllString(raw, "$1+$2")
	for _, layout := range linkdingTimeLayouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			seconds := parsed.Unix()
			return &seconds, nil
		}
	}
	return nil, badRequest(name + ": Enter a valid date/time.")
}

func (a *App) linkdingGetBookmark(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	id, fault := linkdingIDOf(r)
	if fault != nil {
		a.writeDetailError(w, fault)
		return
	}
	bookmark, found, err := a.bookmarks.Get(r.Context(), user.UserID, id)
	if err != nil {
		a.writeDetailError(w, internal(err))
		return
	}
	if !found {
		a.writeDetailError(w, detailNotFound())
		return
	}
	writeJSON(w, http.StatusOK, linkdingBookmarkOf(bookmark))
}

func (a *App) linkdingListTags(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	paging := linkdingPagingOf(r, linkdingMaxTagLimit)
	entries, count, err := a.bookmarks.TagEntries(r.Context(), user.UserID, paging.page())
	if err != nil {
		a.writeDetailError(w, internal(err))
		return
	}
	results := make([]linkdingTag, len(entries))
	for i, entry := range entries {
		results[i] = linkdingTagOf(entry)
	}
	writeJSON(w, http.StatusOK, newLinkdingList(r, a.publicScheme(r), paging, count, results))
}

type linkdingCheck struct {
	Bookmark *linkdingBookmark `json:"bookmark"`
	Metadata linkdingMetadata  `json:"metadata"`
	AutoTags []string          `json:"auto_tags"`
}

type linkdingMetadata struct {
	URL          string  `json:"url"`
	Title        *string `json:"title"`
	Description  *string `json:"description"`
	PreviewImage *string `json:"preview_image"`
}

// linkdingCheckBookmark says whether an address is bookmarked, for an extension
// that opens on a page. linkding also reads the page to offer a title; bkd does
// not contact the address here, so the metadata is what is already stored for a
// bookmarked page and empty for any other.
func (a *App) linkdingCheckBookmark(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	address := strings.TrimSpace(r.URL.Query().Get("url"))
	if address == "" {
		a.writeDetailError(w, badRequest("url: This field is required."))
		return
	}
	found, ok, err := a.bookmarks.FindByURL(r.Context(), user.UserID, address)
	if err != nil {
		a.writeDetailError(w, internal(err))
		return
	}
	result := linkdingCheck{Metadata: linkdingMetadata{URL: address}, AutoTags: []string{}}
	if ok {
		view := linkdingBookmarkOf(found)
		result.Bookmark = &view
		result.Metadata.Title = view.WebsiteTitle
		result.Metadata.Description = view.WebsiteDescription
	}
	writeJSON(w, http.StatusOK, result)
}
