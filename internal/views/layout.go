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
			el("label", []attr{classes("menu"), a("for", "menu-toggle"), a("aria-label", "Open menu")}, text("☰")),
			el("p", []attr{classes("brand")}, el("a", []attr{a("href", "/")}, text("bkd"))),
		),
		el("label", []attr{classes("backdrop"), a("for", "menu-toggle")}),
		sidebar(shell),
		el("main", []attr{a("id", "main"), a("tabindex", "-1")}, notice, content),
	)
	return document(title, body)
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

func sidebar(shell Shell) node {
	on := func(section Section) attr { return current(shell.Section == section) }
	statuses := maybe(len(shell.Statuses) > 0, func() node {
		return group(el("h2", nil, text("Status")), filterList(shell.Statuses))
	})
	account := maybe(!shell.Forwarded, func() node {
		return el("a", []attr{a("href", "/account"), on(SectionAccount)}, text("Account"))
	})
	users := maybe(shell.IsAdmin, func() node {
		return el("a", []attr{a("href", "/users"), on(SectionUsers)}, text("Manage users"))
	})
	signOut := maybe(!shell.Forwarded, func() node {
		return el("form", []attr{a("method", "post"), a("action", "/logout")},
			el("button", []attr{classes("link"), a("type", "submit")}, text("Sign out")))
	})
	return el("nav", []attr{classes("side"), a("id", "sidebar"), a("aria-label", "Filters")},
		el("p", []attr{classes("brand")}, el("a", []attr{a("href", "/")}, text("bkd"))),
		el("ul", nil, el("li", nil, el("a", []attr{a("href", "/"), current(shell.AllCurrent)}, text("All bookmarks")))),
		statuses,
		el("h2", nil, text("Tags")),
		filterList(shell.Tags),
		el("div", []attr{classes("me")},
			el("span", nil, text("Signed in as "), el("strong", nil, text(shell.Username))),
			el("a", []attr{a("href", "/transfer"), on(SectionTransfer)}, text("Import and export")),
			el("a", []attr{a("href", "/settings"), on(SectionSettings)}, text("Settings")),
			account, users, signOut,
		),
	)
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
