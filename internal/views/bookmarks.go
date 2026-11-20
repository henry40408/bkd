package views

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// PageSize is how many bookmarks a list page shows.
const PageSize = 50

// TagChip is one tag of a bookmark; Terms are the search terms to mark in it.
type TagChip struct {
	Name  string
	Terms []string
}

// LinkCheck is the stored result of a link check.
type LinkCheck struct {
	Status    string
	CheckedAt int64
	Detail    string
}

// BookmarkRow is one bookmark on a list page. The *Terms fields are the
// search terms to mark in that part of the bookmark.
type BookmarkRow struct {
	ID          int64
	URL         string
	Title       string
	Note        string
	Tags        []TagChip
	Link        *LinkCheck
	CreatedAt   int64
	TitleTerms  []string
	DomainTerms []string
	NoteTerms   []string
}

// AddForm is the add-bookmark form, open again when its last try was refused.
type AddForm struct {
	Open  bool
	URL   string
	Title string
	Note  string
	Tags  string
	Error string
}

// BulkState is what a refused bulk action shows again: the ticked rows, the
// tags typed and the reason.
type BulkState struct {
	Selected []int64
	Tags     string
	Error    string
}

// BookmarksView is the whole list page.
type BookmarksView struct {
	Shell Shell
	List  ListView
	Now   int64
	Add   AddForm
	Bulk  BulkState
	// Pick is the random bookmark; PickEmpty says a pick found nothing.
	Pick      *BookmarkRow
	PickEmpty bool
	Rows      []BookmarkRow
	HasNext   bool
}

type linkNote struct {
	status  string
	label   string
	text    string
	checked string
}

func linkLabel(status string) string {
	switch status {
	case "ok":
		return "Link OK"
	case "redirected":
		return "Link moved"
	case "broken":
		return "Broken link"
	}
	return "Link not confirmed"
}

func noteOf(link *LinkCheck) *linkNote {
	if link == nil {
		return nil
	}
	switch link.Status {
	case "ok", "redirected", "broken", "unknown":
	default:
		return nil
	}
	checked := NewSavedTime(0, link.CheckedAt).Exact
	note := &linkNote{status: link.Status, label: linkLabel(link.Status), checked: checked}
	switch link.Status {
	case "ok":
		note.text = "checked " + checked
	case "redirected":
		note.text = "now at " + link.Detail
	default:
		note.text = link.Detail
	}
	return note
}

func linkGlyph(status string) node {
	glyph := func(d string) node {
		return el("svg", []attr{a("viewBox", "0 0 16 16")}, el("path", []attr{a("d", d)}))
	}
	switch status {
	case "ok":
		return glyph("M3 8.5l3.2 3.2L13 4.5")
	case "redirected":
		return glyph("M2.5 8h10.5M9 4l4 4-4 4")
	case "broken":
		return glyph("M4 4l8 8M12 4l-8 8")
	}
	return text("?")
}

func linkNoteLine(note *linkNote) node {
	return el("p", []attr{classes("link-note", note.status), a("title", "Checked "+note.checked)},
		el("span", []attr{classes("glyph"), a("aria-hidden", "true")}, linkGlyph(note.status)),
		el("span", []attr{classes("sr-only")}, text(note.label+": ")),
		text(note.text),
	)
}

func tagChips(tags []TagChip) node {
	links := make([]node, len(tags))
	for i, tag := range tags {
		href := WithQuery("/", [][2]string{{"tag", tag.Name}})
		links[i] = el("a", []attr{classes("tag"), a("href", href)}, highlight(tag.Name, tag.Terms))
	}
	return el("div", []attr{classes("tags")}, links...)
}

func (row BookmarkRow) editHref(back string) string {
	return WithQuery(fmt.Sprintf("/bookmarks/%d/edit", row.ID), [][2]string{{"back", back}})
}

func (row BookmarkRow) deleteHref(back string) string {
	return WithQuery(fmt.Sprintf("/bookmarks/%d/delete", row.ID), [][2]string{{"back", back}})
}

func (row BookmarkRow) domain() node {
	return highlight(DomainOf(row.URL), row.DomainTerms)
}

func (row BookmarkRow) note() node {
	return maybe(row.Note != "", func() node {
		return el("p", []attr{classes("note")}, highlight(row.Note, row.NoteTerms))
	})
}

func pickBox(row BookmarkRow, ticked bool) node {
	id := strconv.FormatInt(row.ID, 10)
	return el("label", []attr{classes("pick-box")},
		void("input", a("type", "checkbox"), a("name", "id-"+id), a("value", "1"),
			when(ticked, flag("checked")), a("aria-label", "Select "+row.Title)))
}

// bookmarkItem is one list entry. ticked is nil outside select mode, else
// whether the row's box starts ticked.
func bookmarkItem(row BookmarkRow, now int64, back string, ticked *bool) node {
	note := noteOf(row.Link)
	itemClass := "item"
	if note != nil {
		itemClass += " link-" + note.status
	}
	box := maybe(ticked != nil, func() node { return pickBox(row, *ticked) })
	linkLine := maybe(note != nil, func() node { return linkNoteLine(note) })
	tags := maybe(len(row.Tags) > 0, func() node { return tagChips(row.Tags) })
	return el("li", []attr{a("class", itemClass), a("data-id", strconv.FormatInt(row.ID, 10))},
		box,
		el("span", []attr{classes("domain")}, row.domain()),
		el("div", nil,
			el("a", []attr{classes("title"), a("href", row.URL), a("target", "_blank"), a("rel", "noopener noreferrer")},
				highlight(row.Title, row.TitleTerms)),
			row.note(), linkLine, tags,
		),
		el("div", []attr{classes("when")},
			timeTag(NewSavedTime(now, row.CreatedAt)),
			el("div", []attr{classes("actions")},
				el("a", []attr{classes("quiet"), a("href", row.editHref(back)), a("aria-label", "Edit "+row.Title)}, text("Edit")),
				el("a", []attr{classes("quiet", "danger"), a("href", row.deleteHref(back)), a("aria-label", "Delete "+row.Title)}, text("Delete")),
			),
		),
	)
}

func hidden(name, value string) node {
	return void("input", a("type", "hidden"), a("name", name), a("value", value))
}

func searchHint() node {
	code := func(s string) node { return el("code", nil, text(s)) }
	return el("p", []attr{classes("search-hint"), a("id", "search-hint")},
		text("Use "), code(`"exact phrase"`), text(", "), code("-exclude"), text(", "), code("tag:name"), text(", "),
		code("title:"), text(", "), code("url:"), text(", "), code("note:"), text(" or "), code("status:"), text("."),
	)
}

func searchBar(view BookmarksView) node {
	list := view.List
	quiet := func(extra string, href, label string) node {
		names := []string{"button", "quiet-button"}
		if extra != "" {
			names = append(names, extra)
		}
		return el("a", []attr{classes(names...), a("href", href)}, text(label))
	}
	selectLink := quiet("", list.WithSelect().Href(), "Select")
	if list.Select {
		selectLink = quiet("on", list.WithoutSelect().Href(), "Done")
	}
	bar := el("form", []attr{classes("bar"), a("role", "search"), a("method", "get"), a("action", "/")},
		el("div", []attr{classes("bar-search")},
			void("input", a("type", "search"), a("name", "q"), a("value", list.Q), a("aria-label", "Search bookmarks"),
				a("aria-describedby", "search-hint"), a("placeholder", "Search title, note, URL, or tag")),
			el("button", []attr{a("type", "submit")}, text("Search")),
		),
		// After the field: the address is built from the form in this order (?q=..&tag=..).
		maybe(list.Tag != "", func() node { return hidden("tag", list.Tag) }),
		maybe(list.Status != "", func() node { return hidden("status", list.Status) }),
		quiet("", list.WithoutSelect().HrefTo("/random"), "Random"),
		selectLink,
	)
	return group(bar, searchHint())
}

func addForm(add AddForm) node {
	field := func(label string, input node) node { return el("label", nil, text(label), input) }
	return el("details", []attr{classes("add"), a("id", "add"), when(add.Open, flag("open"))},
		el("summary", nil, text("Add bookmark")),
		el("form", []attr{classes("stack"), a("method", "post"), a("action", "/bookmarks")},
			field("URL ", void("input", a("type", "url"), a("name", "url"), a("value", add.URL), a("placeholder", "https://"), flag("required"))),
			field("Title ", void("input", a("type", "text"), a("name", "title"), a("value", add.Title), a("placeholder", "Leave blank to use the domain"))),
			field("Note ", el("textarea", []attr{a("name", "note"), a("rows", "2")}, text(add.Note))),
			field("Tags, separated by commas ", void("input", a("type", "text"), a("name", "tags"), a("value", add.Tags), a("placeholder", "rust, reading"))),
			errorLine(add.Error),
			el("div", nil, el("button", []attr{a("type", "submit")}, text("Save bookmark"))),
		),
	)
}

func pickSection(view BookmarksView, back string) node {
	if view.Pick == nil {
		return maybe(view.PickEmpty, func() node {
			return el("p", []attr{classes("notice"), a("role", "status")}, text("Nothing to pick from here."))
		})
	}
	pick := *view.Pick
	randomHref := view.List.WithoutSelect().HrefTo("/random")
	return el("section", []attr{classes("pick"), a("aria-label", "Random bookmark")},
		el("a", []attr{classes("title"), a("href", pick.URL), a("target", "_blank"), a("rel", "noopener noreferrer")},
			highlight(pick.Title, pick.TitleTerms)),
		el("span", []attr{classes("domain")}, pick.domain()),
		pick.note(),
		el("div", []attr{classes("actions")},
			el("a", []attr{classes("quiet"), a("href", randomHref)}, text("Another")),
			el("a", []attr{classes("quiet"), a("href", pick.editHref(back))}, text("Edit")),
			el("a", []attr{classes("quiet"), a("href", view.List.Href())}, text("Close")),
		),
	)
}

func bulkPanel(view BookmarksView) node {
	list := view.List
	toggle := "Select all on this page"
	if list.All {
		toggle = "Clear selection"
	}
	submit := func(names []string, action, label string) node {
		return el("button", []attr{classes(names...), a("type", "submit"), a("name", "action"), a("value", action)}, text(label))
	}
	return group(
		hidden("q", list.Q), hidden("tag", list.Tag), hidden("status", list.Status),
		hidden("page", strconv.FormatUint(uint64(list.Page), 10)),
		el("div", []attr{classes("bulk"), a("role", "region"), a("aria-label", "Bulk actions")},
			el("div", []attr{classes("bulk-head")},
				el("p", []attr{classes("bulk-count"), a("id", "bulk-count"), a("aria-live", "polite")}),
				el("p", []attr{classes("bulk-all")},
					el("a", []attr{classes("quiet"), a("href", list.WithAll(!list.All).Href())}, text(toggle))),
			),
			// Shows the tags row on a narrow screen; a refused action keeps it open.
			void("input", a("id", "bulk-tags-toggle"), a("type", "checkbox"),
				when(view.Bulk.Error != "" || view.Bulk.Tags != "", flag("checked"))),
			el("div", []attr{classes("bulk-panel")},
				el("div", []attr{classes("bulk-tags")},
					void("input", a("type", "text"), a("name", "tags"), a("value", view.Bulk.Tags),
						a("placeholder", "Tags, separated by commas"), a("aria-label", "Tags for adding or removing")),
					el("button", []attr{a("type", "submit"), a("name", "action"), a("value", "add_tags")}, text("Add tags")),
					submit([]string{"quiet"}, "remove_tags", "Remove tags"),
				),
				el("label", []attr{classes("bulk-tags-toggle"), a("for", "bulk-tags-toggle")}, text("Tags")),
				submit([]string{"quiet"}, "check", "Check links"),
				submit([]string{"quiet", "danger"}, "delete", "Delete"),
				errorLine(view.Bulk.Error),
			),
			el("p", []attr{classes("hint")}, text("Tick the bookmarks you want, then choose an action.")),
		),
	)
}

func emptyText(list ListView) string {
	if strings.TrimSpace(list.Q) != "" || list.Tag != "" || list.Status != "" {
		return "No bookmarks match."
	}
	return "No bookmarks yet. Add one above, or import a file."
}

func rowList(view BookmarksView, back string) node {
	if len(view.Rows) == 0 {
		return el("div", []attr{classes("empty")}, text(emptyText(view.List)))
	}
	offset := (int64(view.List.Page) - 1) * PageSize
	summary := fmt.Sprintf("Showing %d–%d", offset+1, offset+int64(len(view.Rows)))
	items := make([]node, len(view.Rows))
	for i, row := range view.Rows {
		var ticked *bool
		if view.List.Select {
			isTicked := view.List.All || slices.Contains(view.Bulk.Selected, row.ID)
			ticked = &isTicked
		}
		items[i] = bookmarkItem(row, view.Now, back, ticked)
	}
	listClass := []string{"list"}
	if view.List.Select {
		listClass = append(listClass, "selecting")
	}
	return group(
		el("p", []attr{classes("summary")}, text(summary)),
		el("ul", []attr{classes(listClass...)}, items...),
	)
}

func pager(view BookmarksView) node {
	hasPrev := view.List.Page > 1
	if !hasPrev && !view.HasNext {
		return ""
	}
	link := func(page uint32, rel, label string) node {
		return el("a", []attr{classes("button", "quiet-button"), a("href", view.List.HrefForPage(page)), a("rel", rel)}, text(label))
	}
	return el("nav", []attr{classes("pager"), a("aria-label", "Pages")},
		maybe(hasPrev, func() node { return link(view.List.Page-1, "prev", "Newer") }),
		maybe(view.HasNext, func() node { return link(view.List.Page+1, "next", "Older") }),
	)
}

// BookmarksPage renders the bookmark list.
func BookmarksPage(view BookmarksView) string {
	back := view.List.Href()
	list := rowList(view, back)
	body := list
	if view.List.Select {
		body = el("form", []attr{classes("bulk-form"), a("method", "post"), a("action", "/bookmarks/bulk")},
			bulkPanel(view), list)
	}
	content := group(searchBar(view), addForm(view.Add), pickSection(view, back), body, pager(view))
	return framed("bkd", view.Shell, content)
}

// EditView is the edit-bookmark form.
type EditView struct {
	Shell Shell
	ID    int64
	URL   string
	Title string
	Note  string
	Tags  string
	Error string
	// Back is the list address to return to.
	Back string
}

// EditPage renders the edit form.
func EditPage(view EditView) string {
	field := func(label string, input node) node { return el("label", nil, text(label), input) }
	content := group(
		el("h1", nil, text("Edit bookmark")),
		el("form", []attr{classes("stack", "edit-form"), a("method", "post"), a("action", fmt.Sprintf("/bookmarks/%d/edit", view.ID))},
			hidden("back", view.Back),
			field("URL ", void("input", a("type", "url"), a("name", "url"), a("value", view.URL), a("placeholder", "https://"), flag("required"), flag("autofocus"))),
			field("Title ", void("input", a("type", "text"), a("name", "title"), a("value", view.Title), a("placeholder", "Leave blank to use the domain"))),
			field("Note ", el("textarea", []attr{a("name", "note"), a("rows", "3")}, text(view.Note))),
			field("Tags, separated by commas ", void("input", a("type", "text"), a("name", "tags"), a("value", view.Tags))),
			errorLine(view.Error),
			el("div", nil,
				el("button", []attr{a("type", "submit")}, text("Save")),
				text(" "),
				el("a", []attr{classes("quiet-link"), a("href", view.Back)}, text("Cancel")),
			),
		),
	)
	return framed("Edit bookmark · bkd", view.Shell, content)
}

// HiddenField is a name and value a confirmation form sends again.
type HiddenField struct {
	Name  string
	Value string
}

// ConfirmDeleteView asks before deleting one or more bookmarks.
type ConfirmDeleteView struct {
	Shell      Shell
	Heading    string
	Detail     string
	Action     string
	Hidden     []HiddenField
	CancelHref string
}

// ConfirmDeletePage renders the delete confirmation.
func ConfirmDeletePage(view ConfirmDeleteView) string {
	fields := make([]node, len(view.Hidden))
	for i, field := range view.Hidden {
		fields[i] = hidden(field.Name, field.Value)
	}
	form := []node{}
	form = append(form, fields...)
	form = append(form, el("div", nil,
		el("button", []attr{classes("danger-button"), a("type", "submit")}, text("Delete")),
		text(" "),
		el("a", []attr{classes("quiet-link"), a("href", view.CancelHref), flag("autofocus")}, text("Keep")),
	))
	content := group(
		el("h1", nil, text(view.Heading)),
		el("p", []attr{classes("summary")}, text(view.Detail)),
		el("form", []attr{classes("stack"), a("method", "post"), a("action", view.Action)}, form...),
	)
	return framed("Delete · bkd", view.Shell, content)
}
