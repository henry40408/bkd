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
	detail  string
	checked string
	ago     string
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

func noteOf(link *LinkCheck, now int64) *linkNote {
	if link == nil {
		return nil
	}
	switch link.Status {
	case "ok", "redirected", "broken", "unknown":
	default:
		return nil
	}
	checked := NewSavedTime(now, link.CheckedAt)
	return &linkNote{
		status: link.Status, label: linkLabel(link.Status), detail: link.Detail,
		checked: checked.Exact, ago: checked.Ago,
	}
}

// afterLabel is what the status line says after its bold label.
func (note *linkNote) afterLabel() string {
	checked := ", checked " + note.ago
	switch {
	case note.status == "ok":
		return checked
	case note.status == "redirected":
		return ", now at " + note.detail + checked
	case note.detail == "":
		return checked
	}
	return ": " + note.detail + checked
}

// linkNoteLine is the status line of a bookmark. The status is also the name
// of its icon.
func linkNoteLine(note *linkNote) node {
	return el("p", []attr{classes("link-note", note.status), a("title", "Checked "+note.checked)},
		icon(iconName(note.status)),
		el("span", nil, el("b", nil, text(note.label)), text(note.afterLabel())),
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

// titleLink opens the bookmark in a new tab.
func (row BookmarkRow) titleLink() node {
	return el("a", []attr{classes("title"), a("href", row.URL), a("target", "_blank"), a("rel", "noopener noreferrer")},
		highlight(row.Title, row.TitleTerms))
}

func (row BookmarkRow) linkLine(now int64) node {
	note := noteOf(row.Link, now)
	return maybe(note != nil, func() node { return linkNoteLine(note) })
}

func pickBox(row BookmarkRow, ticked bool) node {
	id := strconv.FormatInt(row.ID, 10)
	return el("label", []attr{classes("pick-box")},
		void("input", a("type", "checkbox"), a("name", "id-"+id), a("value", "1"),
			when(ticked, flag("checked")), a("aria-label", "Select "+row.Title)))
}

func rowActions(row BookmarkRow, back string) node {
	return el("div", []attr{classes("actions")},
		el("a", []attr{classes("btn", "quiet", "ico"), a("href", row.editHref(back)),
			a("aria-label", "Edit "+row.Title), a("title", "Edit")}, iconLabel(iconEdit, "Edit")),
		el("a", []attr{classes("btn", "quiet", "ico", "danger-text"), a("href", row.deleteHref(back)),
			a("aria-label", "Delete "+row.Title), a("title", "Delete")}, iconLabel(iconTrash, "Delete")),
	)
}

func rowBody(row BookmarkRow, now int64) node {
	tags := maybe(len(row.Tags) > 0, func() node { return tagChips(row.Tags) })
	return el("div", []attr{classes("body")},
		el("h3", nil, row.titleLink()),
		el("p", []attr{classes("url"), a("title", row.URL)}, highlight(row.URL, row.DomainTerms)),
		row.note(), tags, row.linkLine(now),
	)
}

// bookmarkItem is one list entry. ticked is nil outside select mode, else
// whether the row's box starts ticked.
func bookmarkItem(row BookmarkRow, now int64, back string, ticked *bool) node {
	box := maybe(ticked != nil, func() node { return pickBox(row, *ticked) })
	return el("li", []attr{classes("item"), a("data-id", strconv.FormatInt(row.ID, 10))},
		box,
		el("div", []attr{classes("site")}, el("span", nil, row.domain()), timeTag(NewSavedTime(now, row.CreatedAt))),
		rowBody(row, now),
		rowActions(row, back),
	)
}

func hidden(name, value string) node {
	return void("input", a("type", "hidden"), a("name", name), a("value", value))
}

// field is a labelled form control.
func field(label string, input node) node {
	return el("label", []attr{classes("field")}, text(label), input)
}

// searchTips explains the search syntax. The examples are plain text: nothing
// in the page acts on them.
func searchTips() node {
	tip := func(example, meaning string) node {
		return group(el("dt", nil, el("code", nil, text(example))), el("dd", nil, text(meaning)))
	}
	return el("details", []attr{classes("tips"), a("id", "search-hint")},
		el("summary", nil, text("Search tips")),
		el("dl", nil,
			tip(`"exact phrase"`, "Match the words together, in order."),
			tip("-word", "Leave out bookmarks that match."),
			tip("url:github.com", "Match one field only: title:, url: or note:."),
			tip(`tag:"to read"`, "Match a whole tag."),
			tip("status:broken", "Match the last link check: broken, ok, redirected, unknown or unchecked."),
		),
	)
}

// toolLink is a link that looks like an icon button.
func toolLink(href string, name iconName, label string, extra ...string) node {
	names := append([]string{"btn", "ico"}, extra...)
	return el("a", []attr{classes(names...), a("href", href), a("title", label)}, iconLabel(name, label))
}

// searchBar is the search form and, after it, the add form, so that "Add
// bookmark" ends the row of buttons.
func searchBar(view BookmarksView) node {
	list := view.List
	selectLink := toolLink(list.WithSelect().Href(), iconSelect, "Select")
	if list.Select {
		selectLink = toolLink(list.WithoutSelect().Href(), iconSelect, "Select", "on")
	}
	form := el("form", []attr{a("role", "search"), a("method", "get"), a("action", "/")},
		el("div", []attr{classes("q")},
			void("input", a("type", "search"), a("name", "q"), a("value", list.Q), a("aria-label", "Search bookmarks"),
				a("aria-describedby", "search-hint"), a("placeholder", "Search title, note, URL, or tag")),
		),
		el("button", []attr{classes("btn", "ico"), a("type", "submit"), a("title", "Search")}, iconLabel(iconSearch, "Search")),
		// After the field: the address is built from the form in this order (?q=..&tag=..).
		maybe(list.Tag != "", func() node { return hidden("tag", list.Tag) }),
		maybe(list.Status != "", func() node { return hidden("status", list.Status) }),
		toolLink(list.WithoutSelect().HrefTo("/random"), iconShuffle, "Random"),
		selectLink,
	)
	return group(el("div", []attr{classes("bar")}, form, addForm(view.Add)), searchTips())
}

func addForm(add AddForm) node {
	return el("details", []attr{classes("add"), a("id", "add"), when(add.Open, flag("open"))},
		el("summary", []attr{classes("btn", "primary", "ico"), a("title", "Add bookmark")}, iconLabel(iconPlus, "Add bookmark")),
		el("form", []attr{classes("stack", "panel"), a("method", "post"), a("action", "/bookmarks")},
			field("URL ", void("input", a("type", "url"), a("name", "url"), a("value", add.URL), a("placeholder", "https://"), flag("required"))),
			el("div", []attr{classes("grid2")},
				field("Title ", void("input", a("type", "text"), a("name", "title"), a("value", add.Title), a("placeholder", "Leave blank to use the domain"))),
				field("Tags, separated by commas ", void("input", a("type", "text"), a("name", "tags"), a("value", add.Tags), a("placeholder", "rust, reading"))),
			),
			field("Note ", el("textarea", []attr{a("name", "note"), a("rows", "2")}, text(add.Note))),
			errorLine(add.Error),
			formButtons(el("button", []attr{classes("btn", "primary"), a("type", "submit")}, iconLabel(iconOK, "Save bookmark"))),
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
	return el("section", []attr{classes("pick", "panel"), a("aria-label", "Random bookmark")},
		el("p", []attr{classes("hint")}, pick.domain()),
		el("h2", nil, pick.titleLink()),
		pick.note(),
		pick.linkLine(view.Now),
		el("div", []attr{classes("btns")},
			toolLink(randomHref, iconShuffle, "Another", "primary"),
			toolLink(pick.editHref(back), iconEdit, "Edit", "quiet"),
			toolLink(view.List.Href(), iconBroken, "Close"),
		),
	)
}

func bulkSubmit(action string, name iconName, label string, extra ...string) node {
	names := append([]string{"btn", "ico"}, extra...)
	return el("button", []attr{classes(names...), a("type", "submit"), a("name", "action"), a("value", action), a("title", label)},
		iconLabel(name, label))
}

func bulkPanel(view BookmarksView) node {
	list := view.List
	toggle := "Select all on this page"
	if list.All {
		toggle = "Clear selection"
	}
	return group(
		hidden("q", list.Q), hidden("tag", list.Tag), hidden("status", list.Status),
		hidden("page", strconv.FormatUint(uint64(list.Page), 10)),
		el("div", []attr{classes("bulk"), a("role", "region"), a("aria-label", "Bulk actions")},
			el("div", []attr{classes("line")},
				el("p", []attr{classes("bulk-count"), a("id", "bulk-count"), a("aria-live", "polite")}),
				toolLink(list.WithAll(!list.All).Href(), iconAll, toggle, "quiet"),
				toolLink(list.WithoutSelect().Href(), iconOK, "Done", "quiet"),
			),
			el("div", []attr{classes("line")},
				void("input", a("type", "text"), a("name", "tags"), a("value", view.Bulk.Tags),
					a("placeholder", "Tags, separated by commas"), a("aria-label", "Tags for adding or removing")),
				bulkSubmit("add_tags", iconTagAdd, "Add tags"),
				bulkSubmit("remove_tags", iconTagRemove, "Remove tags"),
				bulkSubmit("check", iconLink, "Check links"),
				bulkSubmit("delete", iconTrash, "Delete", "danger"),
			),
			errorLine(view.Bulk.Error),
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
		return el("a", []attr{classes("btn"), a("href", view.List.HrefForPage(page)), a("rel", rel)}, text(label))
	}
	return el("nav", []attr{classes("pager"), a("aria-label", "Pages")},
		maybe(hasPrev, func() node { return link(view.List.Page-1, "prev", "Newer") }),
		maybe(view.HasNext, func() node { return link(view.List.Page+1, "next", "Older") }),
	)
}

// BookmarksPage renders the bookmark list.
func BookmarksPage(view BookmarksView) string {
	back := view.List.Href()
	body := rowList(view, back)
	if view.List.Select {
		// The bulk bar follows the list so that it can stick to the foot of the window.
		body = el("form", []attr{classes("bulk-form"), a("method", "post"), a("action", "/bookmarks/bulk")},
			body, bulkPanel(view))
	}
	content := group(searchBar(view), pickSection(view, back), body, pager(view))
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
	content := group(
		el("h1", nil, text("Edit bookmark")),
		el("form", []attr{classes("stack", "edit-form"), a("method", "post"), a("action", fmt.Sprintf("/bookmarks/%d/edit", view.ID))},
			hidden("back", view.Back),
			field("URL ", void("input", a("type", "url"), a("name", "url"), a("value", view.URL), a("placeholder", "https://"), flag("required"), flag("autofocus"))),
			el("div", []attr{classes("grid2")},
				field("Title ", void("input", a("type", "text"), a("name", "title"), a("value", view.Title), a("placeholder", "Leave blank to use the domain"))),
				field("Tags, separated by commas ", void("input", a("type", "text"), a("name", "tags"), a("value", view.Tags))),
			),
			field("Note ", el("textarea", []attr{a("name", "note"), a("rows", "3")}, text(view.Note))),
			errorLine(view.Error),
			formButtons(
				el("button", []attr{classes("btn", "primary"), a("type", "submit")}, iconLabel(iconOK, "Save")),
				el("a", []attr{classes("btn", "quiet-link"), a("href", view.Back)}, iconLabel(iconBroken, "Cancel")),
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
	form = append(form, formButtons(
		el("button", []attr{classes("btn", "danger", "danger-button"), a("type", "submit")}, iconLabel(iconTrash, "Delete")),
		el("a", []attr{classes("btn", "quiet-link"), a("href", view.CancelHref), flag("autofocus")}, iconLabel(iconBroken, "Keep")),
	))
	content := group(
		el("h1", nil, text(view.Heading)),
		el("p", []attr{classes("summary")}, text(view.Detail)),
		el("form", []attr{classes("stack"), a("method", "post"), a("action", view.Action)}, form...),
	)
	return framed("Delete · bkd", view.Shell, content)
}
