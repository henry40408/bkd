package views

import "strconv"

// SettingsView is the settings page.
type SettingsView struct {
	Shell            Shell
	LinkCheckEnabled bool
	// LinkCheckUserAgent is the value in the box; empty means the built-in one.
	LinkCheckUserAgent string
	// DefaultUserAgent is shown in the empty box, so people see what they replace.
	DefaultUserAgent   string
	MaxUserAgentLength int
	Error              string
	// ServerChecksLinks is true when the administrator has turned the
	// background check on for this server.
	ServerChecksLinks bool
}

// SettingsPage renders the settings page.
func SettingsPage(view SettingsView) string {
	content := group(
		el("h1", nil, text("Settings")),
		// One form for both sections: a post without the box would turn the
		// background check off, so the two settings are always saved together.
		el("form", []attr{classes("sheet"), a("method", "post"), a("action", "/settings")},
			linkCheckSection(view),
			userAgentSection(view),
			errorLine(view.Error),
			formButtons(submitButton("Save")),
		),
	)
	return framed("Settings · bkd", view.Shell, content)
}

func linkCheckSection(view SettingsView) node {
	offNote := maybe(!view.ServerChecksLinks, func() node {
		return el("p", []attr{classes("hint"), a("role", "status")},
			text("Background checks are turned off on this server, so the box above has no effect "+
				"until the administrator turns them on. The User-Agent below still applies to the \"Check links\" button and to reading a page for the add and edit forms."))
	})
	return sheetSection("Link check",
		el("p", nil,
			text("bkd can check your bookmarks in the background and show the result under each one, "+
				"as if you had pressed \"Check links\". It visits each address about once a week, "+
				"waits between requests to the same site, and leaves a site alone when it asks to.")),
		el("label", []attr{classes("inline")},
			void("input", a("type", "checkbox"), a("name", "link_check_enabled"), a("value", "on"),
				when(view.LinkCheckEnabled, flag("checked"))),
			text("Check my bookmarks in the background")),
		offNote,
	)
}

// userAgentSection has a heading of its own, level with "Link check": the
// User-Agent is also sent when the add and edit forms read a page.
func userAgentSection(view SettingsView) node {
	return sheetSection("User-Agent",
		el("p", nil,
			text("The name bkd gives the sites your bookmarks point at. It is sent by the \"Check links\" button, "+
				"by the background check, and when the add and edit forms read a page for its title and description.")),
		void("input", a("type", "text"), a("name", "link_check_user_agent"), a("aria-label", "User-Agent"),
			a("value", view.LinkCheckUserAgent), a("placeholder", view.DefaultUserAgent),
			a("maxlength", strconv.Itoa(view.MaxUserAgentLength)), a("autocomplete", "off"), a("spellcheck", "false")),
		el("p", []attr{classes("hint")},
			text("Leave it empty to say that bkd is asking. Some sites turn robots away, so you may prefer a browser's "+
				"User-Agent, but then the site cannot tell who is asking. Printable ASCII only.")),
	)
}
