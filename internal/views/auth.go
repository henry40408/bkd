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
	return field(label, input)
}

// authBrand is the mark above the title of a sign-in or set-up form. It is not
// a link: the home page would only send the visitor back here.
func authBrand() node {
	return el("span", []attr{classes("brand")}, text("bkd"))
}

// LoginPage renders the sign-in form.
func LoginPage(view LoginView) string {
	form := el("form", []attr{a("method", "post"), a("action", "/login")},
		authBrand(),
		el("h1", nil, text("Sign in")),
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
		iconSubmitButton(iconSignIn, "Sign in"),
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
	form := el("form", []attr{a("method", "post"), a("action", "/setup")},
		authBrand(),
		el("h1", nil, text("Set up bkd")),
		el("p", []attr{classes("hint")},
			text("Create the first admin. The setup token is in the server log from when bkd started.")),
		errorLine(view.Error),
		labelled("Setup token ", void("input", a("type", "text"), a("name", "token"), a("value", view.Token),
			a("autocomplete", "off"), a("autocapitalize", "characters"), a("spellcheck", "false"),
			flag("required"), flag("autofocus"))),
		labelled("Username ", void("input", a("type", "text"), a("name", "username"), a("value", view.Username),
			a("autocomplete", "username"), a("autocapitalize", "none"), a("spellcheck", "false"), flag("required"))),
		labelled("Password ", void("input", a("type", "password"), a("name", "password"),
			a("autocomplete", "new-password"), a("minlength", "12"), flag("required"))),
		iconSubmitButton(iconUserAdd, "Create admin"),
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
		sheet(el("section", nil,
			el("p", nil, text("Changing accounts needs your password again. It stays confirmed for a few minutes.")),
			el("form", []attr{classes("stack"), a("method", "post"), a("action", "/confirm")},
				errorLine(view.Error),
				labelled("Password ", void("input", a("type", "password"), a("name", "password"),
					a("autocomplete", "current-password"), flag("required"), flag("autofocus"))),
				formButtons(
					submitButton("Confirm"),
					el("a", []attr{classes("btn", "quiet-link"), a("href", "/users")}, text("Cancel")),
				),
			),
		)),
	)
	return framed("Confirm password · bkd", view.Shell, content)
}
