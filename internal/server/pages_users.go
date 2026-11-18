package server

import (
	"context"
	"net/http"
	"strconv"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/users"
	"github.com/henry40408/bkd/internal/views"
)

// usersView is the account list. The form fields start empty.
func (a *App) usersView(ctx context.Context, me auth.SessionUser, notice string) (views.UsersView, *pageError) {
	records, err := users.List(ctx, a.db)
	if err != nil {
		return views.UsersView{}, fromAPI(internal(err))
	}
	shell, failure := a.shellFor(ctx, me, views.SectionUsers, notice)
	if failure != nil {
		return views.UsersView{}, failure
	}
	rows := make([]views.UserRow, len(records))
	for i, record := range records {
		rows[i] = views.UserRow{
			ID: record.ID, Username: record.Username, IsAdmin: record.IsAdmin,
			IsDisabled: record.IsDisabled, CreatedAt: record.CreatedAt, CanChange: record.ID != me.UserID,
		}
	}
	return views.UsersView{Shell: shell, Now: auth.UnixNow(), Users: rows}, nil
}

func (a *App) pageUsers(w http.ResponseWriter, r *http.Request, me auth.SessionUser) *pageError {
	view, failure := a.usersView(r.Context(), me, r.URL.Query().Get("notice"))
	if failure != nil {
		return failure
	}
	writeHTML(w, http.StatusOK, views.UsersPage(view))
	return nil
}

func (a *App) pageCreateUser(w http.ResponseWriter, r *http.Request, me auth.SessionUser) *pageError {
	fields, values, failure := requiredFields(r, "username", "password")
	if failure != nil {
		return failure
	}
	_, makeAdmin := fields.lookup("is_admin")
	_, fault := a.createUser(r.Context(), me, newUser{Username: values[0], Password: values[1], IsAdmin: makeAdmin})
	if fault == nil {
		redirectWithNotice(w, "/users", "user-changed")
		return nil
	}
	status, message, other := formRefusal(fault)
	if other != nil {
		return other
	}
	view, failure := a.usersView(r.Context(), me, "")
	if failure != nil {
		return failure
	}
	view.NewName, view.NewIsAdmin, view.Error = values[0], makeAdmin, message
	writeHTML(w, status, views.UsersPage(view))
	return nil
}

func (a *App) pageSetRole(w http.ResponseWriter, r *http.Request, me auth.SessionUser) *pageError {
	id, failure := pagePathID(r)
	if failure != nil {
		return failure
	}
	fields, failure := formOf(r)
	if failure != nil {
		return failure
	}
	isAdmin, refusal := fields.boolean("is_admin")
	if refusal != nil {
		return plainRefusal(refusal)
	}
	return a.finishUserChange(w, a.setRole(r.Context(), me, id, isAdmin))
}

func (a *App) pageSetDisabled(w http.ResponseWriter, r *http.Request, me auth.SessionUser) *pageError {
	id, failure := pagePathID(r)
	if failure != nil {
		return failure
	}
	fields, failure := formOf(r)
	if failure != nil {
		return failure
	}
	disabled, refusal := fields.boolean("is_disabled")
	if refusal != nil {
		return plainRefusal(refusal)
	}
	return a.finishUserChange(w, a.setDisabled(r.Context(), me, id, disabled))
}

func (a *App) finishUserChange(w http.ResponseWriter, fault *apiError) *pageError {
	if fault != nil {
		return fromAPI(fault)
	}
	redirectWithNotice(w, "/users", "user-changed")
	return nil
}

// usernameOrNotFound is the name of the account in the path, or the 404 page.
func (a *App) usernameOrNotFound(ctx context.Context, id int64) (string, *pageError) {
	username, found, err := users.UsernameOf(ctx, a.db, id)
	if err != nil {
		return "", fromAPI(internal(err))
	}
	if !found {
		return "", pageNotFound()
	}
	return username, nil
}

func (a *App) writePasswordPage(w http.ResponseWriter, r *http.Request, status int, view passwordPage) *pageError {
	username, failure := a.usernameOrNotFound(r.Context(), view.id)
	if failure != nil {
		return failure
	}
	shell, failure := a.shellFor(r.Context(), view.admin, views.SectionUsers, "")
	if failure != nil {
		return failure
	}
	writeHTML(w, status, views.PasswordPage(views.PasswordView{
		Shell: shell, ID: view.id, Username: username, Error: view.message,
	}))
	return nil
}

type passwordPage struct {
	admin   auth.SessionUser
	id      int64
	message string
}

func (a *App) pagePasswordForm(w http.ResponseWriter, r *http.Request, me auth.SessionUser) *pageError {
	id, failure := pagePathID(r)
	if failure != nil {
		return failure
	}
	return a.writePasswordPage(w, r, http.StatusOK, passwordPage{admin: me, id: id})
}

func (a *App) pageResetPassword(w http.ResponseWriter, r *http.Request, me auth.SessionUser) *pageError {
	id, failure := pagePathID(r)
	if failure != nil {
		return failure
	}
	_, values, failure := requiredFields(r, "password")
	if failure != nil {
		return failure
	}
	fault := a.resetPassword(r.Context(), me, id, values[0])
	if fault == nil {
		redirectWithNotice(w, "/users", "user-changed")
		return nil
	}
	status, message, other := formRefusal(fault)
	if other != nil {
		return other
	}
	return a.writePasswordPage(w, r, status, passwordPage{admin: me, id: id, message: message})
}

func (a *App) pageDeleteUserForm(w http.ResponseWriter, r *http.Request, me auth.SessionUser) *pageError {
	id, failure := pagePathID(r)
	if failure != nil {
		return failure
	}
	username, failure := a.usernameOrNotFound(r.Context(), id)
	if failure != nil {
		return failure
	}
	shell, failure := a.shellFor(r.Context(), me, views.SectionUsers, "")
	if failure != nil {
		return failure
	}
	writeHTML(w, http.StatusOK, views.ConfirmDeletePage(views.ConfirmDeleteView{
		Shell: shell, Heading: "Delete " + username + "?",
		Detail:     "Their bookmarks and sessions are deleted with the account.",
		Action:     "/users/" + strconv.FormatInt(id, 10) + "/delete",
		CancelHref: "/users",
	}))
	return nil
}

func (a *App) pageDeleteUser(w http.ResponseWriter, r *http.Request, me auth.SessionUser) *pageError {
	id, failure := pagePathID(r)
	if failure != nil {
		return failure
	}
	return a.finishUserChange(w, a.deleteUser(r.Context(), me, id))
}
