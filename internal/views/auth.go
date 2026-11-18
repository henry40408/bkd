package views

// SessionEndedText is shown on the sign-in page after a session ended.
const SessionEndedText = "Your session ended. Sign in again."

// LoginView is the sign-in form.
type LoginView struct {
	Error    string
	Username string
	// Next is where to go after signing in; the caller has checked it is a
	// path on this site.
	Next         string
	SessionEnded bool
}

func labelled(label string, input node) node {
	return el("label", nil, text(label), input)
}

// LoginPage renders the sign-in form.
func LoginPage(view LoginView) string {
	form := el("form", []attr{classes("card"), a("method", "post"), a("action", "/login")},
		el("h1", nil, text("bkd")),
		maybe(view.SessionEnded, func() node {
			return el("p", []attr{classes("notice"), a("role", "status")}, text(SessionEndedText))
		}),
		errorLine(view.Error),
		hidden("next", view.Next),
		labelled("Username ", void("input", a("type", "text"), a("name", "username"), a("value", view.Username),
			a("autocomplete", "username"), a("autocapitalize", "none"), a("spellcheck", "false"),
			flag("required"), flag("autofocus"))),
		labelled("Password ", void("input", a("type", "password"), a("name", "password"),
			a("autocomplete", "current-password"), flag("required"))),
		el("button", []attr{a("type", "submit")}, text("Sign in")),
	)
	return bare("Sign in · bkd", form)
}

// SetupView is the first-admin form.
type SetupView struct {
	Error    string
	Token    string
	Username string
}

// SetupPage renders the first-admin form.
func SetupPage(view SetupView) string {
	form := el("form", []attr{classes("card"), a("method", "post"), a("action", "/setup")},
		el("h1", nil, text("Set up bkd")),
		el("p", []attr{classes("ok-hint")},
			text("Create the first admin. The setup token is in the server log from when bkd started.")),
		errorLine(view.Error),
		labelled("Setup token ", void("input", a("type", "text"), a("name", "token"), a("value", view.Token),
			a("autocomplete", "off"), a("autocapitalize", "characters"), a("spellcheck", "false"),
			flag("required"), flag("autofocus"))),
		labelled("Username ", void("input", a("type", "text"), a("name", "username"), a("value", view.Username),
			a("autocomplete", "username"), a("autocapitalize", "none"), a("spellcheck", "false"), flag("required"))),
		labelled("Password ", void("input", a("type", "password"), a("name", "password"),
			a("autocomplete", "new-password"), a("minlength", "12"), flag("required"))),
		el("button", []attr{a("type", "submit")}, text("Create admin")),
	)
	return bare("Set up · bkd", form)
}

// ConfirmView is the password confirmation form.
type ConfirmView struct {
	Shell Shell
	Error string
}

// ConfirmPage renders the password confirmation form.
func ConfirmPage(view ConfirmView) string {
	content := group(
		el("h1", nil, text("Confirm your password")),
		el("p", []attr{classes("summary")},
			text("Changing accounts needs your password again. It stays confirmed for a few minutes.")),
		el("form", []attr{classes("stack", "password-form"), a("method", "post"), a("action", "/confirm")},
			errorLine(view.Error),
			labelled("Password ", void("input", a("type", "password"), a("name", "password"),
				a("autocomplete", "current-password"), flag("required"), flag("autofocus"))),
			el("div", nil,
				el("button", []attr{a("type", "submit")}, text("Confirm")),
				text(" "),
				el("a", []attr{classes("quiet-link"), a("href", "/users")}, text("Cancel")),
			),
		),
	)
	return framed("Confirm password · bkd", view.Shell, content)
}
