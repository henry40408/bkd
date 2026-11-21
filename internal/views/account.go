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
		return el("span", []attr{classes("here")}, text("This browser"))
	})
	signOut := maybe(!session.IsCurrent, func() node {
		return el("form", []attr{a("method", "post"), a("action", "/account/sessions/"+session.ID+"/revoke")},
			el("button", []attr{classes("btn", "quiet", "danger-text"), a("type", "submit"), a("aria-label", "Sign out "+session.Agent)},
				text("Sign out")))
	})
	return el("div", []attr{classes("user-row")},
		el("span", []attr{classes("name")}, text(session.Agent), here),
		el("span", []attr{classes("meta")},
			text(session.IP+" · last used "), timeTag(NewSavedTime(now, session.LastUsedAt))),
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
		el("h2", nil, text("Sessions")),
		el("p", []attr{classes("summary")},
			text("Every browser that is signed in to your account. End the ones you do not recognize.")),
		el("div", []attr{classes("session-list")}, rows...),
		maybe(len(view.Sessions) > 1, func() node {
			return el("form", []attr{classes("downloads"), a("method", "post"), a("action", "/account/sessions/revoke-others")},
				el("button", []attr{classes("btn"), a("type", "submit")}, text("Sign out all other browsers")))
		}),
		el("h2", nil, text("Change password")),
		el("p", []attr{classes("summary")},
			text("Use at least 12 characters, and do not put your username in it. Changing it signs you out everywhere else.")),
		el("form", []attr{classes("stack", "password-form"), a("method", "post"), a("action", "/account/password")},
			passwordField("Current password ", "current", "current-password", false),
			passwordField("New password ", "new", "new-password", true),
			passwordField("Repeat new password ", "again", "new-password", false),
			errorLine(view.Error),
			formButtons(submitButton("Change password")),
		),
	)
	return framed("Account · bkd", view.Shell, content)
}
