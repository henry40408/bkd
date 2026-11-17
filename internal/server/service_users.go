package server

import (
	"context"
	"log/slog"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/users"
)

type newUser struct {
	Username string
	Password string
	IsAdmin  bool
}

func (a *App) createUser(ctx context.Context, admin auth.SessionUser, input newUser) (users.UserRecord, *apiError) {
	username, err := auth.NormalizeUsername(input.Username)
	if err != nil {
		return users.UserRecord{}, badRequest(err.Error())
	}
	if err := auth.ValidatePassword(input.Password, username); err != nil {
		return users.UserRecord{}, badRequest(err.Error())
	}
	hash, err := a.hasher.Hash(ctx, input.Password)
	if err != nil {
		return users.UserRecord{}, internal(err)
	}
	created, change, err := users.Create(ctx, a.db, admin.UserID, users.NewAccount{
		Username: username, PasswordHash: hash, IsAdmin: input.IsAdmin, Now: auth.UnixNow(),
	})
	if err != nil {
		return users.UserRecord{}, internal(err)
	}
	if fault := changeFault(change); fault != nil {
		return users.UserRecord{}, fault
	}
	a.logger.Info("user created", slog.Int64("admin", admin.UserID), slog.Int64("user_id", created.ID))
	return created, nil
}

func (a *App) setRole(ctx context.Context, admin auth.SessionUser, id int64, isAdmin bool) *apiError {
	change, err := users.SetAdmin(ctx, a.db, admin.UserID, id, isAdmin)
	return a.finishChange(admin, id, "role changed", change, err)
}

func (a *App) setDisabled(ctx context.Context, admin auth.SessionUser, id int64, disabled bool) *apiError {
	var disabledAt *int64
	if disabled {
		now := auth.UnixNow()
		disabledAt = &now
	}
	change, err := users.SetDisabled(ctx, a.db, admin.UserID, id, disabledAt)
	return a.finishChange(admin, id, "disabled flag changed", change, err)
}

func (a *App) resetPassword(ctx context.Context, admin auth.SessionUser, id int64, password string) *apiError {
	username, found, err := users.UsernameOf(ctx, a.db, id)
	if err != nil {
		return internal(err)
	}
	if !found {
		return notFound()
	}
	if err := auth.ValidatePassword(password, username); err != nil {
		return badRequest(err.Error())
	}
	hash, err := a.hasher.Hash(ctx, password)
	if err != nil {
		return internal(err)
	}
	change, err := users.ResetPassword(ctx, a.db, admin.UserID, id, hash)
	return a.finishChange(admin, id, "password reset", change, err)
}

func (a *App) deleteUser(ctx context.Context, admin auth.SessionUser, id int64) *apiError {
	change, err := users.Delete(ctx, a.db, admin.UserID, id)
	return a.finishChange(admin, id, "user deleted", change, err)
}

func (a *App) finishChange(admin auth.SessionUser, id int64, action string, change users.Change, err error) *apiError {
	if err != nil {
		return internal(err)
	}
	if fault := changeFault(change); fault != nil {
		return fault
	}
	a.logger.Info(action, slog.Int64("admin", admin.UserID), slog.Int64("user_id", id))
	return nil
}

// changeFault maps what an account change reported to the failure the client sees.
func changeFault(change users.Change) *apiError {
	switch change {
	case users.ChangeNotFound:
		return notFound()
	case users.ChangeCallerNotAdmin:
		return forbidden("admin access required")
	case users.ChangeLastAdmin:
		return conflict("at least one active admin must remain")
	case users.ChangeUsernameTaken:
		return conflict("username is already taken")
	default:
		return nil
	}
}
