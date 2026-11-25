package server

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// apiError is the one failure type of every handler, so each failure maps to one
// status code and a JSON body of the form {"error": "..."}. The page routes show
// the same message.
type apiError struct {
	status  int
	message string
	// code lets the front end tell failures with one status apart.
	code string
	// field names the request field a 400 is about. Only the linkding-compatible
	// routes use it: they answer {"<field>": ["<message>"]}, as linkding does.
	field string
	// retryAfter is set for 429.
	retryAfter time.Duration
	// cause is logged for 500 and never shown.
	cause error
}

const codeReauthRequired = "reauth_required"

func badRequest(message string) *apiError {
	return &apiError{status: http.StatusBadRequest, message: message}
}

func forbidden(message string) *apiError {
	return &apiError{status: http.StatusForbidden, message: message}
}

// badGateway is for a site that did not give what was asked of it.
func badGateway(message string) *apiError {
	return &apiError{status: http.StatusBadGateway, message: message}
}

func conflict(message string) *apiError {
	return &apiError{status: http.StatusConflict, message: message}
}

// invalidCredentials is one message for every cause, so a caller cannot tell a
// missing user from a wrong password or a disabled account.
func invalidCredentials() *apiError {
	return &apiError{status: http.StatusUnauthorized, message: "invalid username or password"}
}

const notSignedInMessage = "not signed in"

func unauthorized() *apiError {
	return &apiError{status: http.StatusUnauthorized, message: notSignedInMessage}
}

// isNotSignedIn tells a missing session from a wrong password, which is a 401 too.
func (e *apiError) isNotSignedIn() bool {
	return e.status == http.StatusUnauthorized && e.message == notSignedInMessage
}

// reauthRequired is an admin change that needs the password typed again. The front
// end looks for the code, asks for the password and repeats the request.
func reauthRequired() *apiError {
	return &apiError{
		status:  http.StatusForbidden,
		message: "confirm your password to continue",
		code:    codeReauthRequired,
	}
}

func notFound() *apiError {
	return &apiError{status: http.StatusNotFound, message: "not found"}
}

func unsupportedMediaType(message string) *apiError {
	return &apiError{status: http.StatusUnsupportedMediaType, message: message}
}

func tooManyRequests(wait time.Duration) *apiError {
	return &apiError{
		status:     http.StatusTooManyRequests,
		message:    "too many attempts, try again later",
		retryAfter: wait,
	}
}

func internal(cause error) *apiError {
	return &apiError{status: http.StatusInternalServerError, message: "internal error", cause: cause}
}

// retryAfterSeconds rounds up, so a client never retries too early.
func (e *apiError) retryAfterSeconds() int64 {
	seconds := int64(e.retryAfter / time.Second)
	if e.retryAfter%time.Second > 0 {
		seconds++
	}
	return seconds
}

// writeAPIError answers with the JSON error body and logs internal faults.
func (a *App) writeAPIError(w http.ResponseWriter, e *apiError) {
	if e.cause != nil {
		a.logger.Error("internal error", slog.Any("error", e.cause))
	}
	if e.status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", strconv.FormatInt(e.retryAfterSeconds(), 10))
	}
	writeJSON(w, e.status, errorBody{Error: e.message, Code: e.code})
}

// errorBody keeps "error" before "code", as clients saw it from the Rust server.
type errorBody struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}
