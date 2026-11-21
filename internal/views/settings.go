package views

// SettingsView is the settings page.
type SettingsView struct {
	Shell            Shell
	LinkCheckEnabled bool
	// ServerChecksLinks is true when the administrator has turned the
	// background check on for this server.
	ServerChecksLinks bool
}

// SettingsPage renders the settings page.
func SettingsPage(view SettingsView) string {
	offNote := maybe(!view.ServerChecksLinks, func() node {
		return el("p", []attr{classes("hint"), a("role", "status")},
			text("Background checks are turned off on this server, so this setting has no effect "+
				"until the administrator turns them on."))
	})
	content := group(
		el("h1", nil, text("Settings")),
		sheet(sheetSection("Link check",
			el("p", nil,
				text("bkd can check your bookmarks in the background and show the result under each one, "+
					"as if you had pressed \"Check links\". It visits each address about once a week, "+
					"waits between requests to the same site, and leaves a site alone when it asks to.")),
			offNote,
			el("form", []attr{classes("stack"), a("method", "post"), a("action", "/settings")},
				el("label", []attr{classes("inline")},
					void("input", a("type", "checkbox"), a("name", "link_check_enabled"), a("value", "on"),
						when(view.LinkCheckEnabled, flag("checked"))),
					text("Check my bookmarks in the background")),
				formButtons(submitButton("Save")),
			),
		)),
	)
	return framed("Settings · bkd", view.Shell, content)
}
