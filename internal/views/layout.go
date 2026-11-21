package views

// Section is the part of the site a signed-in page belongs to; the sidebar
// marks its link as current.
type Section int

// The sections of the sidebar.
const (
	SectionBookmarks Section = iota
	SectionTransfer
	SectionSettings
	SectionAccount
	SectionUsers
)

// FilterLink is one entry of the status or tag list in the sidebar.
type FilterLink struct {
	Label   string
	Count   int64
	Href    string
	Current bool
}

// Shell is the page frame around every signed-in screen: the sidebar and the
// account links.
type Shell struct {
	Username string
	IsAdmin  bool
	// Forwarded means the reverse proxy signed the user in: no password and no
	// Sign out here.
	Forwarded  bool
	Section    Section
	AllCurrent bool
	Statuses   []FilterLink
	Tags       []FilterLink
	// Notice is the fixed text to announce, or empty.
	Notice string
}

func document(title string, body node) string {
	head := el("head", nil,
		void("meta", a("charset", "utf-8")),
		void("meta", a("name", "viewport"), a("content", "width=device-width, initial-scale=1")),
		el("title", nil, text(title)),
		void("link", a("rel", "icon"), a("href", "/favicon.svg"), a("type", "image/svg+xml")),
		void("link", a("rel", "icon"), a("href", "/favicon.ico"), a("sizes", "48x48")),
		void("link", a("rel", "apple-touch-icon"), a("href", "/apple-touch-icon.png")),
		void("link", a("rel", "stylesheet"), a("href", "/app.css")),
		el("script", []attr{a("src", "/app.js"), flag("defer")}),
	)
	page := el("html", []attr{a("lang", "en")}, head, el("body", nil, body))
	return "<!DOCTYPE html>" + string(page)
}

// bare is a page with no sidebar: sign-in, set-up and errors.
func bare(title string, content node) string {
	return document(title, el("main", []attr{classes("login"), a("id", "main")}, content))
}

// framed is a signed-in page: the sidebar around content.
func framed(title string, shell Shell, content node) string {
	notice := maybe(shell.Notice != "", func() node {
		return el("p", []attr{classes("notice"), a("role", "status")}, text(shell.Notice))
	})
	body := el("div", []attr{classes("app")},
		void("input", classes("sr-only"), a("id", "menu-toggle"), a("type", "checkbox")),
		el("header", []attr{classes("topbar")},
			el("label", []attr{classes("btn", "menu"), a("for", "menu-toggle"), a("aria-label", "Open menu")}, icon(iconMenu)),
			brand(),
		),
		el("label", []attr{classes("backdrop"), a("for", "menu-toggle")}),
		sidebar(shell),
		el("main", []attr{a("id", "main"), a("tabindex", "-1")}, notice, content),
	)
	return document(title, body)
}

func brand() node {
	return el("a", []attr{classes("brand"), a("href", "/")}, text("bkd"))
}

func current(isCurrent bool) attr {
	return when(isCurrent, a("aria-current", "page"))
}

func filterList(filters []FilterLink) node {
	items := make([]node, len(filters))
	for i, filter := range filters {
		items[i] = el("li", nil, el("a", []attr{a("href", filter.Href), current(filter.Current)},
			text(filter.Label),
			el("span", []attr{classes("count")}, number(filter.Count)),
		))
	}
	return el("ul", nil, items...)
}

func sectionLink(href, label string, isCurrent bool) node {
	return el("li", nil, el("a", []attr{a("href", href), current(isCurrent)}, text(label)))
}

func sectionList(shell Shell) node {
	on := func(section Section) bool { return shell.Section == section }
	return el("ul", nil,
		sectionLink("/", "All bookmarks", shell.AllCurrent),
		sectionLink("/transfer", "Import and export", on(SectionTransfer)),
		sectionLink("/settings", "Settings", on(SectionSettings)),
		maybe(!shell.Forwarded, func() node { return sectionLink("/account", "Account", on(SectionAccount)) }),
		maybe(shell.IsAdmin, func() node { return sectionLink("/users", "Manage users", on(SectionUsers)) }),
	)
}

func filterGroup(heading string, filters []FilterLink) node {
	return el("div", nil, el("h2", nil, text(heading)), filterList(filters))
}

func signedInAs(shell Shell) node {
	signOut := maybe(!shell.Forwarded, func() node {
		return el("form", []attr{a("method", "post"), a("action", "/logout")},
			el("button", []attr{classes("btn", "quiet"), a("type", "submit")}, text("Sign out")))
	})
	return el("div", []attr{classes("me")},
		el("p", nil, text("Signed in as "), el("strong", nil, text(shell.Username))),
		signOut,
	)
}

func sidebar(shell Shell) node {
	statuses := maybe(len(shell.Statuses) > 0, func() node { return filterGroup("Status", shell.Statuses) })
	return el("nav", []attr{classes("side"), a("id", "sidebar"), a("aria-label", "Sidebar")},
		brand(),
		sectionList(shell),
		statuses,
		filterGroup("Tags", shell.Tags),
		signedInAs(shell),
	)
}

// submitButton is the main button of a form.
func submitButton(label string) node {
	return el("button", []attr{classes("btn", "primary"), a("type", "submit")}, text(label))
}

// formButtons is the row of buttons under a form.
func formButtons(buttons ...node) node {
	return el("div", []attr{classes("btns")}, buttons...)
}

// errorLine is a form's refusal, shown above its button.
func errorLine(message string) node {
	return maybe(message != "", func() node {
		return el("p", []attr{classes("err"), a("role", "alert")}, text(message))
	})
}

// timeTag is a time with the exact UTC time as its tooltip; the script may
// show local time.
func timeTag(t SavedTime) node {
	return el("time", []attr{a("datetime", t.ISO), a("title", t.Exact)}, text(t.Ago))
}
