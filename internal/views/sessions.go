package views

// SessionRow is one signed-in browser on the settings page.
type SessionRow struct {
	ID         string
	Agent      string
	IP         string
	LastUsedAt int64
	IsCurrent  bool
}

func sessionRow(session SessionRow, now int64) node {
	here := maybe(session.IsCurrent, func() node {
		return el("span", []attr{classes("badge", "admin")}, text("This browser"))
	})
	signOut := maybe(!session.IsCurrent, func() node {
		return el("form", []attr{a("method", "post"), a("action", "/settings/sessions/"+session.ID+"/revoke")},
			el("button", []attr{classes("btn", "ico"), a("type", "submit"), a("title", "Sign out"),
				a("aria-label", "Sign out "+session.Agent)}, iconLabel(iconSignOut, "Sign out")))
	})
	return el("li", nil,
		el("span", []attr{classes("who")},
			el("b", nil, text(session.Agent)),
			el("span", []attr{classes("hint")},
				text(session.IP+" · last used "), timeTag(NewSavedTime(now, session.LastUsedAt))),
		),
		here,
		signOut,
	)
}

func sessionsSection(view SettingsView) node {
	rows := make([]node, len(view.Sessions))
	for i, session := range view.Sessions {
		rows[i] = sessionRow(session, view.Now)
	}
	return sheetSection("Sessions",
		el("p", nil, text("Every browser that is signed in to your account. End the ones you do not recognize.")),
		el("ul", []attr{classes("sessions")}, rows...),
		maybe(len(view.Sessions) > 1, func() node {
			return el("form", []attr{a("method", "post"), a("action", "/settings/sessions/revoke-others")},
				formButtons(el("button", []attr{classes("btn"), a("type", "submit")},
					iconLabel(iconSignOut, "Sign out all other browsers"))))
		}),
	)
}
