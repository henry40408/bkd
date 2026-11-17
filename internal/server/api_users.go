package server

import (
	"net/http"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/users"
)

type managedUser struct {
	ID         int64  `json:"id"`
	Username   string `json:"username"`
	IsAdmin    bool   `json:"is_admin"`
	IsDisabled bool   `json:"is_disabled"`
	CreatedAt  int64  `json:"created_at"`
}

func managedUserOf(record users.UserRecord) managedUser {
	return managedUser{
		ID: record.ID, Username: record.Username, IsAdmin: record.IsAdmin,
		IsDisabled: record.IsDisabled, CreatedAt: record.CreatedAt,
	}
}

func (a *App) handleListUsers(w http.ResponseWriter, r *http.Request, _ auth.SessionUser) {
	records, err := users.List(r.Context(), a.db)
	if err != nil {
		a.writeAPIError(w, internal(err))
		return
	}
	managed := make([]managedUser, 0, len(records))
	for _, record := range records {
		managed = append(managed, managedUserOf(record))
	}
	writeJSON(w, http.StatusOK, struct {
		Users []managedUser `json:"users"`
	}{managed})
}

func (a *App) handleCreateUser(w http.ResponseWriter, r *http.Request, admin auth.SessionUser) {
	var request struct {
		Username *string `json:"username"`
		Password *string `json:"password"`
		IsAdmin  bool    `json:"is_admin"`
	}
	if failure := decodeJSON(r, &request); failure != nil {
		failure.write(w)
		return
	}
	if failure := requireFields(
		field{"username", request.Username != nil},
		field{"password", request.Password != nil},
	); failure != nil {
		failure.write(w)
		return
	}
	created, fault := a.createUser(r.Context(), admin, newUser{
		Username: *request.Username, Password: *request.Password, IsAdmin: request.IsAdmin,
	})
	if fault != nil {
		a.writeAPIError(w, fault)
		return
	}
	writeJSON(w, http.StatusCreated, managedUserOf(created))
}

func (a *App) handleSetRole(w http.ResponseWriter, r *http.Request, admin auth.SessionUser) {
	id, failure := pathID(r)
	if failure != nil {
		failure.write(w)
		return
	}
	var request struct {
		IsAdmin *bool `json:"is_admin"`
	}
	if failure := decodeJSON(r, &request); failure != nil {
		failure.write(w)
		return
	}
	if failure := requireFields(field{"is_admin", request.IsAdmin != nil}); failure != nil {
		failure.write(w)
		return
	}
	a.writeNoContent(w, a.setRole(r.Context(), admin, id, *request.IsAdmin))
}

func (a *App) handleSetDisabled(w http.ResponseWriter, r *http.Request, admin auth.SessionUser) {
	id, failure := pathID(r)
	if failure != nil {
		failure.write(w)
		return
	}
	var request struct {
		IsDisabled *bool `json:"is_disabled"`
	}
	if failure := decodeJSON(r, &request); failure != nil {
		failure.write(w)
		return
	}
	if failure := requireFields(field{"is_disabled", request.IsDisabled != nil}); failure != nil {
		failure.write(w)
		return
	}
	a.writeNoContent(w, a.setDisabled(r.Context(), admin, id, *request.IsDisabled))
}

func (a *App) handleResetPassword(w http.ResponseWriter, r *http.Request, admin auth.SessionUser) {
	id, failure := pathID(r)
	if failure != nil {
		failure.write(w)
		return
	}
	var request struct {
		Password *string `json:"password"`
	}
	if failure := decodeJSON(r, &request); failure != nil {
		failure.write(w)
		return
	}
	if failure := requireFields(field{"password", request.Password != nil}); failure != nil {
		failure.write(w)
		return
	}
	a.writeNoContent(w, a.resetPassword(r.Context(), admin, id, *request.Password))
}

func (a *App) handleDeleteUser(w http.ResponseWriter, r *http.Request, admin auth.SessionUser) {
	id, failure := pathID(r)
	if failure != nil {
		failure.write(w)
		return
	}
	a.writeNoContent(w, a.deleteUser(r.Context(), admin, id))
}

// writeNoContent answers 204, or the fault when there is one.
func (a *App) writeNoContent(w http.ResponseWriter, fault *apiError) {
	if fault != nil {
		a.writeAPIError(w, fault)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
