package views

import (
	"fmt"
	"strconv"
)

// UserRow is one account on the users page.
type UserRow struct {
	ID         int64
	Username   string
	IsAdmin    bool
	IsDisabled bool
	CreatedAt  int64
	// CanChange is false for your own row: the server would allow a change,
	// but a slip there locks you out.
	CanChange bool
}

// UsersView is the user management page.
type UsersView struct {
	Shell      Shell
	Now        int64
	Users      []UserRow
	NewName    string
	NewIsAdmin bool
	Error      string
}

// quietIconAttrs is the look of a link-like control that shows only its icon on a narrow screen.
func quietIconAttrs(label, ariaLabel string, extra ...string) []attr {
	return []attr{classes(append([]string{"btn", "quiet", "ico"}, extra...)...), a("title", label), a("aria-label", ariaLabel)}
}

func userActions(user UserRow) node {
	role, state, stateIcon := "Promote", "Disable", iconBan
	if user.IsAdmin {
		role = "Demote"
	}
	if user.IsDisabled {
		state, stateIcon = "Enable", iconOK
	}
	id := strconv.FormatInt(user.ID, 10)
	return group(
		el("form", []attr{a("method", "post"), a("action", "/users/"+id+"/role")},
			hidden("is_admin", strconv.FormatBool(!user.IsAdmin)),
			el("button", []attr{classes("btn", "quiet"), a("type", "submit"), a("aria-label", role+" "+user.Username)}, text(role))),
		el("form", []attr{a("method", "post"), a("action", "/users/"+id+"/disabled")},
			hidden("is_disabled", strconv.FormatBool(!user.IsDisabled)),
			el("button", append(quietIconAttrs(state, state+" "+user.Username), a("type", "submit")),
				iconLabel(stateIcon, state))),
		el("a", append(quietIconAttrs("Reset password", "Reset password of "+user.Username),
			a("href", "/users/"+id+"/password")), iconLabel(iconKey, "Reset password")),
		el("a", append(quietIconAttrs("Delete", "Delete "+user.Username, "danger-text"),
			a("href", "/users/"+id+"/delete")), iconLabel(iconTrash, "Delete")),
	)
}

func userRow(user UserRow, now int64) node {
	return el("li", nil,
		el("span", []attr{classes("who")},
			text(user.Username),
			el("span", []attr{classes("hint")}, text("Created "), timeTag(NewSavedTime(now, user.CreatedAt))),
		),
		maybe(user.IsAdmin, func() node { return el("span", []attr{classes("badge", "admin")}, text("Admin")) }),
		maybe(user.IsDisabled, func() node { return el("span", []attr{classes("badge")}, text("Disabled")) }),
		maybe(user.CanChange, func() node { return userActions(user) }),
	)
}

// UsersPage renders the user management page.
func UsersPage(view UsersView) string {
	rows := make([]node, len(view.Users))
	for i, user := range view.Users {
		rows[i] = userRow(user, view.Now)
	}
	content := group(
		el("h1", nil, text("Users")),
		sheet(
			el("section", nil,
				el("ul", []attr{classes("people")}, rows...),
				el("p", []attr{classes("hint")}, text("Deleting a user also deletes all of their bookmarks.")),
			),
			sheetSection("Create user",
				el("form", []attr{classes("stack"), a("method", "post"), a("action", "/users")},
					labelled("Username ", void("input", a("type", "text"), a("name", "username"), a("value", view.NewName), a("autocomplete", "off"))),
					labelled("Temporary password ", void("input", a("type", "password"), a("name", "password"), a("autocomplete", "new-password"))),
					el("label", []attr{classes("inline")},
						void("input", a("type", "checkbox"), a("name", "is_admin"), when(view.NewIsAdmin, flag("checked"))),
						text(" Admin (can manage users)")),
					errorLine(view.Error),
					formButtons(iconSubmitButton(iconUserAdd, "Create user")),
				),
			),
		),
	)
	return framed("Users · bkd", view.Shell, content)
}

// PasswordView is the reset-password form for another user.
type PasswordView struct {
	Shell    Shell
	ID       int64
	Username string
	Error    string
}

// PasswordPage renders the reset-password form.
func PasswordPage(view PasswordView) string {
	content := group(
		el("h1", nil, text("Reset password for "+view.Username)),
		sheet(el("section", nil,
			el("p", nil, text("Their other browsers are signed out when you save.")),
			el("form", []attr{classes("stack"), a("method", "post"), a("action", fmt.Sprintf("/users/%d/password", view.ID))},
				labelled("New password ", void("input", a("type", "password"), a("name", "password"),
					a("autocomplete", "new-password"), a("minlength", "12"), flag("required"), flag("autofocus"))),
				errorLine(view.Error),
				formButtons(
					iconSubmitButton(iconKey, "Reset password"),
					el("a", []attr{classes("btn", "quiet-link"), a("href", "/users")}, text("Cancel")),
				),
			),
		)),
	)
	return framed("Reset password · bkd", view.Shell, content)
}
