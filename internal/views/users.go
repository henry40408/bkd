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

func userActions(user UserRow) node {
	role, state := "Promote", "Disable"
	if user.IsAdmin {
		role = "Demote"
	}
	if user.IsDisabled {
		state = "Enable"
	}
	id := strconv.FormatInt(user.ID, 10)
	return el("div", []attr{classes("user-actions")},
		el("form", []attr{a("method", "post"), a("action", "/users/"+id+"/role")},
			hidden("is_admin", strconv.FormatBool(!user.IsAdmin)),
			el("button", []attr{classes("btn", "quiet"), a("type", "submit"), a("aria-label", role+" "+user.Username)}, text(role))),
		el("form", []attr{a("method", "post"), a("action", "/users/"+id+"/disabled")},
			hidden("is_disabled", strconv.FormatBool(!user.IsDisabled)),
			el("button", []attr{classes("btn", "quiet"), a("type", "submit"), a("aria-label", state+" "+user.Username)}, text(state))),
		el("a", []attr{classes("btn", "quiet"), a("href", "/users/"+id+"/password"),
			a("aria-label", "Reset password of "+user.Username)}, text("Reset password")),
		el("a", []attr{classes("btn", "quiet", "danger-text"), a("href", "/users/"+id+"/delete"),
			a("aria-label", "Delete "+user.Username)}, text("Delete")),
	)
}

func userRow(user UserRow, now int64) node {
	rowClass := []string{"user-row"}
	if user.IsDisabled {
		rowClass = append(rowClass, "off")
	}
	meta := group(
		maybe(user.IsAdmin, func() node { return text("Admin · ") }),
		maybe(user.IsDisabled, func() node { return text("Disabled · ") }),
		text("Created "),
		timeTag(NewSavedTime(now, user.CreatedAt)),
	)
	return el("div", []attr{classes(rowClass...)},
		el("span", []attr{classes("name")}, text(user.Username)),
		el("span", []attr{classes("meta")}, meta),
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
		el("div", []attr{classes("user-list")}, rows...),
		el("p", []attr{classes("summary")}, text("Deleting a user also deletes all of their bookmarks.")),
		el("form", []attr{classes("stack", "new-user"), a("method", "post"), a("action", "/users")},
			labelled("Username ", void("input", a("type", "text"), a("name", "username"), a("value", view.NewName), a("autocomplete", "off"))),
			labelled("Temporary password ", void("input", a("type", "password"), a("name", "password"), a("autocomplete", "new-password"))),
			el("label", []attr{classes("inline")},
				void("input", a("type", "checkbox"), a("name", "is_admin"), when(view.NewIsAdmin, flag("checked"))),
				text(" Admin (can manage users)")),
			errorLine(view.Error),
			formButtons(submitButton("Create user")),
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
		el("p", []attr{classes("summary")}, text("Their other browsers are signed out when you save.")),
		el("form", []attr{classes("stack", "password-form"), a("method", "post"), a("action", fmt.Sprintf("/users/%d/password", view.ID))},
			labelled("New password ", void("input", a("type", "password"), a("name", "password"),
				a("autocomplete", "new-password"), a("minlength", "12"), flag("required"), flag("autofocus"))),
			errorLine(view.Error),
			formButtons(
				submitButton("Reset password"),
				el("a", []attr{classes("btn", "quiet-link"), a("href", "/users")}, text("Cancel")),
			),
		),
	)
	return framed("Reset password · bkd", view.Shell, content)
}
