package views

// SessionRow is one signed-in browser on the account page.
type SessionRow struct {
	ID         string
	Agent      string
	IP         string
	LastUsedAt int64
	IsCurrent  bool
}

// AccountView is the account page: sessions and password.
type AccountView struct {
	Shell    Shell
	Now      int64
	Sessions []SessionRow
	Error    string
}

func sessionRow(session SessionRow, now int64) node {
	here := maybe(session.IsCurrent, func() node {
		return el("span", []attr{classes("badge", "admin")}, text("This browser"))
	})
	signOut := maybe(!session.IsCurrent, func() node {
		return el("form", []attr{a("method", "post"), a("action", "/account/sessions/"+session.ID+"/revoke")},
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

func passwordField(label, name, autocomplete string, minLength bool) node {
	attrs := []attr{a("type", "password"), a("name", name), a("autocomplete", autocomplete),
		when(minLength, a("minlength", "12")), flag("required")}
	return labelled(label, void("input", attrs...))
}

// AccountPage renders the account page.
func AccountPage(view AccountView) string {
	rows := make([]node, len(view.Sessions))
	for i, session := range view.Sessions {
		rows[i] = sessionRow(session, view.Now)
	}
	content := group(
		el("h1", nil, text("Account")),
		sheet(
			sheetSection("Sessions",
				el("p", nil, text("Every browser that is signed in to your account. End the ones you do not recognize.")),
				el("ul", []attr{classes("sessions")}, rows...),
				maybe(len(view.Sessions) > 1, func() node {
					return el("form", []attr{a("method", "post"), a("action", "/account/sessions/revoke-others")},
						formButtons(el("button", []attr{classes("btn"), a("type", "submit")},
							iconLabel(iconSignOut, "Sign out all other browsers"))))
				}),
			),
			sheetSection("Change password",
				el("p", []attr{classes("hint")},
					text("At least 12 characters, without your username. Changing it signs you out everywhere else.")),
				el("form", []attr{classes("stack"), a("method", "post"), a("action", "/account/password")},
					passwordField("Current password ", "current", "current-password", false),
					passwordField("New password ", "new", "new-password", true),
					passwordField("Repeat new password ", "again", "new-password", false),
					errorLine(view.Error),
					formButtons(iconSubmitButton(iconKey, "Change password")),
				),
			),
		),
	)
	return framed("Account · bkd", view.Shell, content)
}
