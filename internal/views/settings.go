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
	offNote := maybe(!view.ServerChecksLinks, func() node {
		return el("p", []attr{classes("hint"), a("role", "status")},
			text("Background checks are turned off on this server, so the box above has no effect "+
				"until the administrator turns them on. The User-Agent below still applies to the \"Check links\" button."))
	})
	content := group(
		el("h1", nil, text("Settings")),
		sheet(sheetSection("Link check",
			el("p", nil,
				text("bkd can check your bookmarks in the background and show the result under each one, "+
					"as if you had pressed \"Check links\". It visits each address about once a week, "+
					"waits between requests to the same site, and leaves a site alone when it asks to.")),
			el("form", []attr{classes("stack"), a("method", "post"), a("action", "/settings")},
				el("label", []attr{classes("inline")},
					void("input", a("type", "checkbox"), a("name", "link_check_enabled"), a("value", "on"),
						when(view.LinkCheckEnabled, flag("checked"))),
					text("Check my bookmarks in the background")),
				offNote,
				labelled("User-Agent for link checks ", void("input", a("type", "text"), a("name", "link_check_user_agent"),
					a("value", view.LinkCheckUserAgent), a("placeholder", view.DefaultUserAgent),
					a("maxlength", strconv.Itoa(view.MaxUserAgentLength)), a("autocomplete", "off"), a("spellcheck", "false"))),
				el("p", []attr{classes("hint")},
					text("Sent to the sites your bookmarks point at, by the \"Check links\" button and by the background check. "+
						"Leave it empty to say that bkd is asking. Some sites turn robots away, so you may prefer a browser's "+
						"User-Agent, but then the site cannot tell who is asking. Printable ASCII only.")),
				errorLine(view.Error),
				formButtons(submitButton("Save")),
			),
		)),
	)
	return framed("Settings · bkd", view.Shell, content)
}
