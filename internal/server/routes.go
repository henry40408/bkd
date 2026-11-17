package server

import (
	"net/http"
	"strings"

	"github.com/henry40408/bkd/internal/assets"
	"github.com/henry40408/bkd/internal/auth"
)

// methodHandler is one method of a route.
type methodHandler struct {
	method  string
	handler http.Handler
}

func onGet(handler http.Handler) methodHandler    { return methodHandler{http.MethodGet, handler} }
func onPost(handler http.Handler) methodHandler   { return methodHandler{http.MethodPost, handler} }
func onPut(handler http.Handler) methodHandler    { return methodHandler{http.MethodPut, handler} }
func onDelete(handler http.Handler) methodHandler { return methodHandler{http.MethodDelete, handler} }

// byMethod dispatches on the request method itself instead of ServeMux patterns,
// because axum answered a wrong method with 405, an empty body and an Allow header
// that lists the methods in the order they were declared, HEAD right after GET.
// The middleware around it runs first, as it did there.
func byMethod(handlers ...methodHandler) http.Handler {
	allowed := make([]string, 0, len(handlers)+1)
	for _, entry := range handlers {
		allowed = append(allowed, entry.method)
		if entry.method == http.MethodGet {
			allowed = append(allowed, http.MethodHead)
		}
	}
	allow := strings.Join(allowed, ",")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.Method
		if method == http.MethodHead {
			method = http.MethodGet
		}
		for _, entry := range handlers {
			if entry.method == method {
				entry.handler.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Allow", allow)
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
}

// Handler returns every route of the server.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	a.mountAPI(mux)
	for _, route := range assets.Routes {
		mux.Handle(route.Path, byMethod(onGet(assets.Handler(route))))
	}
	mux.HandleFunc("/", http.NotFound)
	return a.securityHeaders(mux)
}

// api wraps one JSON route. The cross-site check sits inside the headers, so its
// refusal is not cached either.
func (a *App) api(limit int64, route http.Handler) http.Handler {
	return limitBody(limit, apiHeaders(a.sameOriginOnly(route)))
}

func (a *App) mountAPI(mux *http.ServeMux) {
	route := func(path string, handlers ...methodHandler) {
		mux.Handle("/api"+path, a.api(maxBodyBytes, byMethod(handlers...)))
	}
	signedIn := func(handle func(http.ResponseWriter, *http.Request, auth.SessionUser)) http.Handler {
		return a.withUser(handle)
	}
	admin := func(handle func(http.ResponseWriter, *http.Request, auth.SessionUser)) http.Handler {
		return a.withAdmin(handle)
	}
	confirmed := func(handle func(http.ResponseWriter, *http.Request, auth.SessionUser)) http.Handler {
		return a.withConfirmedAdmin(handle)
	}

	route("/state", onGet(http.HandlerFunc(a.handleState)))
	route("/setup", onPost(http.HandlerFunc(a.handleSetup)))
	route("/login", onPost(http.HandlerFunc(a.handleLogin)))
	route("/logout", onPost(http.HandlerFunc(a.handleLogout)))
	route("/settings", onGet(signedIn(a.handleGetSettings)), onPut(signedIn(a.handlePutSettings)))
	route("/account/password", onPut(signedIn(a.handleChangePassword)))
	route("/account/confirm", onPost(signedIn(a.handleConfirmPassword)))
	route("/account/sessions",
		onGet(signedIn(a.handleListSessions)), onDelete(signedIn(a.handleRevokeOtherSessions)))
	route("/account/sessions/{id}", onDelete(signedIn(a.handleRevokeSession)))
	route("/bookmarks", onGet(signedIn(a.handleListBookmarks)), onPost(signedIn(a.handleCreateBookmark)))
	route("/bookmarks/random", onGet(signedIn(a.handleRandomBookmark)))
	route("/bookmarks/bulk", onPost(signedIn(a.handleBulkBookmarks)))
	route("/bookmarks/{id}", onPut(signedIn(a.handleUpdateBookmark)), onDelete(signedIn(a.handleDeleteBookmark)))
	route("/tags", onGet(signedIn(a.handleTags)))
	mux.Handle("/api/import", a.api(maxImportBytes, byMethod(onPost(signedIn(a.handleImport)))))
	route("/export", onGet(signedIn(a.handleExport)))
	route("/users", onGet(admin(a.handleListUsers)), onPost(confirmed(a.handleCreateUser)))
	route("/users/{id}", onDelete(confirmed(a.handleDeleteUser)))
	route("/users/{id}/role", onPut(confirmed(a.handleSetRole)))
	route("/users/{id}/disabled", onPut(confirmed(a.handleSetDisabled)))
	route("/users/{id}/password", onPut(confirmed(a.handleResetPassword)))
}

type userHandler func(http.ResponseWriter, *http.Request, auth.SessionUser)

// withUser runs the handler for a signed-in caller and answers 401 otherwise,
// before the body is read.
func (a *App) withUser(handle userHandler) http.Handler {
	return a.guarded(a.currentUser, handle)
}

func (a *App) withAdmin(handle userHandler) http.Handler {
	return a.guarded(a.adminUser, handle)
}

func (a *App) withConfirmedAdmin(handle userHandler) http.Handler {
	return a.guarded(a.confirmedAdmin, handle)
}

func (a *App) guarded(check func(*http.Request) (auth.SessionUser, *apiError), handle userHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, fault := check(r)
		if fault != nil {
			a.writeAPIError(w, fault)
			return
		}
		handle(w, r, user)
	})
}
