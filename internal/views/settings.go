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

	// Now is the time the token and session lists are shown relative to.
	Now    int64
	Tokens []TokenRow
	// NewToken is the secret of a token made by this request. It is shown once.
	NewToken string
	// TokenName is what was typed in the name box, kept when the token was refused.
	TokenName          string
	TokenError         string
	MaxTokenNameLength int

	// Sessions and PasswordError belong to the password sections, which a user
	// signed in by the reverse proxy does not get (Shell.Forwarded): there is no
	// password here to change and there are no sessions to end.
	Sessions      []SessionRow
	PasswordError string
}

// TokenRow is one API token on the settings page. LastUsedAt is nil until the
// token is first used.
type TokenRow struct {
	ID         string
	Name       string
	CreatedAt  int64
	LastUsedAt *int64
}

// SettingsPage renders the settings page. Its sections are one sheet, so the
// page has one heavy rule at the top; each form sits inside the section it
// belongs to, except the first, which spans two.
func SettingsPage(view SettingsView) string {
	content := group(
		el("h1", nil, text("Settings")),
		sheet(
			// One form for both sections: a post without the box would turn the
			// background check off, so the two settings are always saved together.
			el("form", []attr{a("method", "post"), a("action", "/settings")},
				linkCheckSection(view),
				userAgentSection(view),
				errorLine(view.Error),
				formButtons(submitButton("Save")),
			),
			tokensSection(view),
			maybe(!view.Shell.Forwarded, func() node {
				return group(sessionsSection(view), passwordSection(view))
			}),
		),
	)
	return framed("Settings · bkd", view.Shell, content)
}

func tokensSection(view SettingsView) node {
	rows := make([]node, len(view.Tokens))
	for i, token := range view.Tokens {
		rows[i] = tokenRow(token, view.Now)
	}
	return sheetSection("API tokens",
		el("p", nil,
			text("A program that cannot sign in through the browser, such as a browser extension or a script, "+
				"sends one of these as \"Authorization: Token <token>\" to call bkd's linkding-compatible API. "+
				"A token acts as you until you end it. Disabling your account or resetting its password ends them all.")),
		newTokenNotice(view.NewToken),
		maybe(len(rows) > 0, func() node { return el("ul", []attr{classes("sessions")}, rows...) }),
		el("form", []attr{classes("stack"), a("method", "post"), a("action", "/settings/tokens")},
			labelled("Name ", void("input", a("type", "text"), a("name", "name"), a("value", view.TokenName),
				a("placeholder", "Browser extension"), a("maxlength", strconv.Itoa(view.MaxTokenNameLength)),
				a("autocomplete", "off"), flag("required"))),
			errorLine(view.TokenError),
			formButtons(iconSubmitButton(iconKey, "Create token")),
		),
	)
}

const newTokenFieldID = "new-api-token"

// newTokenNotice shows a secret that was just made. It is the only time the page can.
// The Copy button needs the script, so the server hides it; the box stays selectable
// for a reader without the script.
func newTokenNotice(secret string) node {
	return maybe(secret != "", func() node {
		return el("div", nil,
			el("p", []attr{a("role", "status")}, text("Copy this token now. It is shown only once.")),
			el("div", []attr{classes("copy-row")},
				void("input", a("id", newTokenFieldID), a("type", "text"), a("aria-label", "New API token"),
					a("value", secret), a("autocomplete", "off"), a("spellcheck", "false"), flag("readonly")),
				el("button", []attr{classes("btn"), a("type", "button"), a("data-copy-from", newTokenFieldID),
					flag("data-script-only"), flag("hidden")}, iconLabel(iconCopy, "Copy")),
				// Apart from the button: a live region is read whole, so a label inside one
				// would be read with everything around it at each change.
				el("span", []attr{classes("sr-only"), a("role", "status"), flag("data-copy-status")}),
			),
		)
	})
}

func tokenRow(token TokenRow, now int64) node {
	lastUsed := text(" · never used")
	if token.LastUsedAt != nil {
		lastUsed = group(text(" · last used "), timeTag(NewSavedTime(now, *token.LastUsedAt)))
	}
	return el("li", nil,
		el("span", []attr{classes("who")},
			el("b", nil, text(token.Name)),
			el("span", []attr{classes("hint")},
				text("created "), timeTag(NewSavedTime(now, token.CreatedAt)), lastUsed),
		),
		el("form", []attr{a("method", "post"), a("action", "/settings/tokens/"+token.ID+"/revoke")},
			el("button", []attr{classes("btn", "ico"), a("type", "submit"), a("title", "Revoke"),
				a("aria-label", "Revoke "+token.Name)}, iconLabel(iconTrash, "Revoke"))),
	)
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
