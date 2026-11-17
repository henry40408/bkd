package server

import (
	"net/http"
	"net/netip"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/users"
)

// peerAddr is the address of the TCP peer: the proxy when there is one, not the end
// user. IPv4-mapped addresses become plain IPv4, so one client has one rate-limit
// bucket however the socket reports it.
func peerAddr(r *http.Request) netip.Addr {
	addrPort, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}
	}
	return addrPort.Addr().Unmap()
}

// userFromRequest reports who is signed in; ok is false when nobody is. A trusted
// proxy's word wins over a cookie: when the proxy signs the user out, bkd must not
// keep them in.
func (a *App) userFromRequest(r *http.Request) (user auth.SessionUser, ok bool, fault *apiError) {
	identity := auth.Identity{Kind: auth.IdentityAbsent}
	if a.forwardAuth != nil {
		identity = a.forwardAuth.Identify(peerAddr(r), r.Header)
	}
	switch identity.Kind {
	case auth.IdentityInvalid:
		return auth.SessionUser{}, false, nil
	case auth.IdentityUser:
		return a.userFromProxy(r, identity)
	default:
		return a.userFromCookie(r)
	}
}

func (a *App) userFromProxy(r *http.Request, identity auth.Identity) (auth.SessionUser, bool, *apiError) {
	account := users.ForwardedAccount{
		Username: identity.Username,
		IsAdmin:  identity.IsAdmin,
		Now:      auth.UnixNow(),
	}
	adopted, found, err := users.AdoptForwarded(r.Context(), a.db, a.logger, account)
	if err != nil {
		return auth.SessionUser{}, false, internal(err)
	}
	if !found {
		return auth.SessionUser{}, false, nil
	}
	if fault := a.closeSetupIfUsersExist(r); fault != nil {
		return auth.SessionUser{}, false, fault
	}
	user := auth.SessionUser{
		UserID:   adopted.ID,
		Username: identity.Username,
		IsAdmin:  adopted.IsAdmin,
		ViaProxy: true,
	}
	return user, true, nil
}

// closeSetupIfUsersExist: the first account may have just been made here, which
// closes setup.
func (a *App) closeSetupIfUsersExist(r *http.Request) *apiError {
	if a.currentSetupToken() == nil {
		return nil
	}
	exist, err := auth.UsersExist(r.Context(), a.db)
	if err != nil {
		return internal(err)
	}
	if exist {
		a.clearSetupToken()
	}
	return nil
}

func (a *App) userFromCookie(r *http.Request) (auth.SessionUser, bool, *apiError) {
	token, found := a.readToken(r)
	if !found {
		return auth.SessionUser{}, false, nil
	}
	user, ok, err := a.sessions.Lookup(r.Context(), token, auth.UnixNow())
	if err != nil {
		return auth.SessionUser{}, false, internal(err)
	}
	return user, ok, nil
}

// currentUser rejects anonymous requests.
func (a *App) currentUser(r *http.Request) (auth.SessionUser, *apiError) {
	user, ok, fault := a.userFromRequest(r)
	if fault != nil {
		return auth.SessionUser{}, fault
	}
	if !ok {
		return auth.SessionUser{}, unauthorized()
	}
	return user, nil
}

// adminUser refuses anyone else: 401 when anonymous, 403 when not an admin.
func (a *App) adminUser(r *http.Request) (auth.SessionUser, *apiError) {
	user, fault := a.currentUser(r)
	if fault != nil {
		return auth.SessionUser{}, fault
	}
	if !user.IsAdmin {
		return auth.SessionUser{}, forbidden("admin access required")
	}
	return user, nil
}

// confirmedAdmin is an admin who typed their password in the last few minutes.
// Changes to accounts take this, so a stolen admin session cannot promote, disable
// or reset anyone without the password.
func (a *App) confirmedAdmin(r *http.Request) (auth.SessionUser, *apiError) {
	user, fault := a.adminUser(r)
	if fault != nil {
		return auth.SessionUser{}, fault
	}
	// The proxy already proved who this is, and there is no password to ask for.
	if user.ViaProxy {
		return user, nil
	}
	token, found := a.readToken(r)
	if !found {
		return auth.SessionUser{}, reauthRequired()
	}
	confirmed, err := a.sessions.IsConfirmed(r.Context(), token, auth.UnixNow())
	if err != nil {
		return auth.SessionUser{}, internal(err)
	}
	if !confirmed {
		return auth.SessionUser{}, reauthRequired()
	}
	return user, nil
}
