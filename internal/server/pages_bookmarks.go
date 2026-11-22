package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/bookmarks"
	"github.com/henry40408/bkd/internal/search"
	"github.com/henry40408/bkd/internal/views"
)

// Keeps page * page size far from overflowing; a page above it is the last page.
const maxListPage = 1_000_000

// listContext is everything the list page shows besides the bookmarks.
type listContext struct {
	view   views.ListView
	notice string
	add    views.AddForm
	bulk   views.BulkState
	// pick is the random bookmark; pickEmpty says a pick found nothing.
	pick      *bookmarks.Bookmark
	pickEmpty bool
}

// readListView reads the address of a list page. A page that is not a number is a
// 400, as it was from axum's Query extractor.
func readListView(r *http.Request) (views.ListView, *pageError) {
	query := r.URL.Query()
	page, failure := optionalUint32(r, "page")
	if failure != nil {
		return views.ListView{}, plainRefusal(failure)
	}
	number := uint32(1)
	if page != nil {
		number = min(max(*page, 1), maxListPage)
	}
	return views.ListView{
		Q:      query.Get("q"),
		Tag:    query.Get("tag"),
		Status: query.Get("status"),
		Page:   number,
		Select: query.Has("select"),
		All:    query.Has("all"),
	}, nil
}

func filterFor(view views.ListView) search.Filter {
	return search.NewFilter(view.Q, view.Tag).WithStatus(view.Status)
}

func siteOf(site *bookmarks.Site) *views.SiteInfo {
	if site == nil {
		return nil
	}
	return &views.SiteInfo{Title: site.Title, Description: site.Description}
}

func linkOf(link *bookmarks.LinkCheck) *views.LinkCheck {
	if link == nil {
		return nil
	}
	return &views.LinkCheck{Status: link.Status, CheckedAt: link.CheckedAt, Detail: link.Detail}
}

// rowOf marks the search terms in each part of a bookmark.
func rowOf(bookmark bookmarks.Bookmark, filter search.Filter) views.BookmarkRow {
	chips := make([]views.TagChip, len(bookmark.Tags))
	for i, tag := range bookmark.Tags {
		chips[i] = views.TagChip{
			Name:  tag,
			Terms: filter.Highlights(search.Highlight{Part: search.Tag, Tag: tag}),
		}
	}
	return views.BookmarkRow{
		ID: bookmark.ID, URL: bookmark.URL, Title: bookmark.Title, Note: bookmark.Note,
		Tags: chips, Link: linkOf(bookmark.Link), Site: siteOf(bookmark.Site), CreatedAt: bookmark.CreatedAt,
		TitleTerms:  filter.Highlights(search.Highlight{Part: search.Title}),
		DomainTerms: filter.Highlights(search.Highlight{Part: search.URL}),
		NoteTerms:   filter.Highlights(search.Highlight{Part: search.Note}),
	}
}

func (a *App) listPage(ctx context.Context, user auth.SessionUser, in listContext) (views.BookmarksView, *pageError) {
	view := in.view
	filter := filterFor(view)
	// Load more makes a page longer instead of moving on, so every page starts at the top.
	window := bookmarks.Page{Limit: view.Page * views.PageSize}
	found, err := a.bookmarks.List(ctx, user.UserID, filter, window)
	if err != nil {
		return views.BookmarksView{}, fromAPI(internal(err))
	}
	matching, err := a.bookmarks.Count(ctx, user.UserID, filter)
	if err != nil {
		return views.BookmarksView{}, fromAPI(internal(err))
	}
	shell, failure := a.loadShell(ctx, user, activeSection{
		section: views.SectionBookmarks, tag: view.Tag, status: view.Status, notice: in.notice,
	})
	if failure != nil {
		return views.BookmarksView{}, failure
	}
	page := views.BookmarksView{
		Shell: shell, List: view, Now: auth.UnixNow(), Add: in.add, Bulk: in.bulk,
		PickEmpty: in.pickEmpty, Matching: matching,
	}
	for _, bookmark := range found {
		page.Rows = append(page.Rows, rowOf(bookmark, filter))
	}
	if in.pick != nil {
		picked := rowOf(*in.pick, filter)
		page.Pick = &picked
	}
	return page, nil
}

// writeListPage answers with the list and the given status.
func (a *App) writeListPage(w http.ResponseWriter, r *http.Request, status int, user auth.SessionUser, in listContext) *pageError {
	page, failure := a.listPage(r.Context(), user, in)
	if failure != nil {
		return failure
	}
	writeHTML(w, status, views.BookmarksPage(page))
	return nil
}

func (a *App) pageList(w http.ResponseWriter, r *http.Request, user auth.SessionUser) *pageError {
	view, failure := readListView(r)
	if failure != nil {
		return failure
	}
	in := listContext{view: view, notice: r.URL.Query().Get("notice")}
	return a.writeListPage(w, r, http.StatusOK, user, in)
}

func (a *App) pageRandom(w http.ResponseWriter, r *http.Request, user auth.SessionUser) *pageError {
	view, failure := readListView(r)
	if failure != nil {
		return failure
	}
	picked, found, err := a.bookmarks.Random(r.Context(), user.UserID, filterFor(view))
	if err != nil {
		return fromAPI(internal(err))
	}
	in := listContext{view: view, pickEmpty: !found}
	if found {
		in.pick = &picked
	}
	return a.writeListPage(w, r, http.StatusOK, user, in)
}

// bookmarkForm is what the add and edit forms send.
type bookmarkForm struct {
	url, title, note, tags string
	// back is the list address to return to after editing; ignored when adding.
	back string
}

func readBookmarkForm(r *http.Request) (bookmarkForm, *pageError) {
	fields, values, failure := requiredFields(r, "url")
	if failure != nil {
		return bookmarkForm{}, failure
	}
	return bookmarkForm{
		url: values[0], title: fields.text("title"), note: fields.text("note"),
		tags: fields.text("tags"), back: fields.text("back"),
	}, nil
}

// input trims what the person typed. A blank title falls back to the domain, as
// the old front end did.
func (f bookmarkForm) input() bookmarks.Input {
	address := strings.TrimSpace(f.url)
	title := strings.TrimSpace(f.title)
	if title == "" {
		title = views.DomainOf(address)
	}
	return bookmarks.Input{
		URL: address, Title: title, Note: strings.TrimSpace(f.note),
		Tags: strings.Split(f.tags, ","),
	}
}

func (a *App) pageCreateBookmark(w http.ResponseWriter, r *http.Request, user auth.SessionUser) *pageError {
	form, failure := readBookmarkForm(r)
	if failure != nil {
		return failure
	}
	_, fault := a.createBookmark(r.Context(), user, form.input())
	if fault == nil {
		redirectWithNotice(w, "/", "bookmark-saved")
		return nil
	}
	status, message, other := formRefusal(fault)
	if other != nil {
		return other
	}
	add := views.AddForm{Open: true, URL: form.url, Title: form.title, Note: form.note, Tags: form.tags, Error: message}
	return a.writeListPage(w, r, status, user, listContext{view: views.ListView{Page: 1}, add: add})
}

func (a *App) editShell(ctx context.Context, user auth.SessionUser) (views.Shell, *pageError) {
	return a.shellFor(ctx, user, views.SectionBookmarks, "")
}

// ownBookmark is the bookmark of the path, or the 404 page.
func (a *App) ownBookmark(r *http.Request, user auth.SessionUser) (bookmarks.Bookmark, int64, *pageError) {
	id, failure := pagePathID(r)
	if failure != nil {
		return bookmarks.Bookmark{}, 0, failure
	}
	bookmark, found, err := a.bookmarks.Get(r.Context(), user.UserID, id)
	if err != nil {
		return bookmarks.Bookmark{}, 0, fromAPI(internal(err))
	}
	if !found {
		return bookmarks.Bookmark{}, 0, pageNotFound()
	}
	return bookmark, id, nil
}

// movedTo is where a moved link now answers from: the detail of a redirected
// check. Empty for any other bookmark.
func movedTo(bookmark bookmarks.Bookmark) string {
	if bookmark.Link == nil || bookmark.Link.Status != "redirected" {
		return ""
	}
	return bookmark.Link.Detail
}

func siteTitleOf(bookmark bookmarks.Bookmark) string {
	if bookmark.Site == nil {
		return ""
	}
	return bookmark.Site.Title
}

func (a *App) pageEditForm(w http.ResponseWriter, r *http.Request, user auth.SessionUser) *pageError {
	bookmark, id, failure := a.ownBookmark(r, user)
	if failure != nil {
		return failure
	}
	shell, failure := a.editShell(r.Context(), user)
	if failure != nil {
		return failure
	}
	writeHTML(w, http.StatusOK, views.EditPage(views.EditView{
		Shell: shell, ID: id, URL: bookmark.URL, Title: bookmark.Title, Note: bookmark.Note,
		Tags: strings.Join(bookmark.Tags, ", "), Back: pathOrHome(r.URL.Query().Get("back")),
		MovedTo: movedTo(bookmark), SiteTitle: siteTitleOf(bookmark),
	}))
	return nil
}

func (a *App) pageEditSubmit(w http.ResponseWriter, r *http.Request, user auth.SessionUser) *pageError {
	id, failure := pagePathID(r)
	if failure != nil {
		return failure
	}
	form, failure := readBookmarkForm(r)
	if failure != nil {
		return failure
	}
	back := pathOrHome(form.back)
	_, fault := a.updateBookmark(r.Context(), user, id, form.input())
	if fault == nil {
		redirectWithNotice(w, back, "bookmark-saved")
		return nil
	}
	if fault.status == http.StatusNotFound {
		return pageNotFound()
	}
	status, message, other := formRefusal(fault)
	if other != nil {
		return other
	}
	shell, failure := a.editShell(r.Context(), user)
	if failure != nil {
		return failure
	}
	writeHTML(w, status, views.EditPage(views.EditView{
		Shell: shell, ID: id, URL: form.url, Title: form.title, Note: form.note,
		Tags: form.tags, Error: message, Back: back,
	}))
	return nil
}

func (a *App) pageDeleteForm(w http.ResponseWriter, r *http.Request, user auth.SessionUser) *pageError {
	bookmark, id, failure := a.ownBookmark(r, user)
	if failure != nil {
		return failure
	}
	shell, failure := a.editShell(r.Context(), user)
	if failure != nil {
		return failure
	}
	back := pathOrHome(r.URL.Query().Get("back"))
	writeHTML(w, http.StatusOK, views.ConfirmDeletePage(views.ConfirmDeleteView{
		Shell: shell, Heading: "Delete this bookmark?", Detail: bookmark.Title,
		Warning:    "This cannot be undone.",
		Action:     "/bookmarks/delete",
		Hidden:     []views.HiddenField{{Name: "id", Value: strconv.FormatInt(id, 10)}, {Name: "back", Value: back}},
		CancelHref: back,
	}))
	return nil
}

func (a *App) pageDeleteSubmit(w http.ResponseWriter, r *http.Request, user auth.SessionUser) *pageError {
	fields, failure := formOf(r)
	if failure != nil {
		return failure
	}
	id, refusal := fields.integer("id")
	if refusal != nil {
		return plainRefusal(refusal)
	}
	if fault := a.deleteBookmark(r.Context(), user, id); fault != nil {
		return fromAPI(fault)
	}
	redirectWithNotice(w, pathOrHome(fields.text("back")), "bookmark-deleted")
	return nil
}

// pageRefreshBookmark reads the page again, then returns to where the person
// came from. A page that cannot be read is an error page that says why.
func (a *App) pageRefreshBookmark(w http.ResponseWriter, r *http.Request, user auth.SessionUser) *pageError {
	id, failure := pagePathID(r)
	if failure != nil {
		return failure
	}
	fields, failure := formOf(r)
	if failure != nil {
		return failure
	}
	if _, fault := a.refreshBookmark(r.Context(), user, id); fault != nil {
		return fromAPI(fault)
	}
	redirectWithNotice(w, pathOrHome(fields.text("back")), "site-refreshed")
	return nil
}
