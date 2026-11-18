package server

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/henry40408/bkd/internal/views"
)

// pageError is why a page could not be shown. Each one is a redirect, an error
// page or, for what axum's extractors refuse, a plain-text answer.
type pageError struct {
	// redirect, when set, is the 303 target.
	redirect string
	// text, when set, is a plain-text rejection.
	text *textError
	// status and message make the error page.
	status  int
	message string
	// retryAfter is set for 429.
	retryAfter time.Duration
	// cause is logged for 500 and never shown.
	cause error
}

func redirectTo(location string) *pageError { return &pageError{redirect: location} }

func failedPage(status int, message string) *pageError {
	return &pageError{status: status, message: message}
}

func pageNotFound() *pageError {
	return failedPage(http.StatusNotFound, "That page does not exist.")
}

func pageBadRequest(message string) *pageError {
	return failedPage(http.StatusBadRequest, message)
}

func plainRefusal(failure *textError) *pageError { return &pageError{text: failure} }

// fromAPI turns what a JSON handler would have answered into the page answer:
// sign-in and password prompts become redirects.
func fromAPI(fault *apiError) *pageError {
	switch {
	case fault.isNotSignedIn():
		return redirectTo("/login")
	case fault.code == codeReauthRequired:
		return redirectTo("/confirm")
	case fault.status == http.StatusTooManyRequests:
		return &pageError{status: fault.status, message: tooManyAttempts, retryAfter: fault.retryAfter}
	case fault.status == http.StatusInternalServerError:
		return &pageError{cause: fault.cause, status: fault.status, message: somethingWentWrong}
	}
	return failedPage(fault.status, fault.message)
}

const (
	tooManyAttempts    = "Too many attempts. Try again later."
	somethingWentWrong = "Something went wrong."
	sessionEndedNotice = "session-ended"
)

// formRefusal is for a form that was refused: the status and message to show on
// the form again. Faults the form cannot fix go to the error page instead.
func formRefusal(fault *apiError) (status int, message string, other *pageError) {
	switch {
	case fault.isNotSignedIn(),
		fault.code == codeReauthRequired,
		fault.status == http.StatusTooManyRequests,
		fault.status == http.StatusInternalServerError:
		return 0, "", fromAPI(fault)
	}
	return fault.status, fault.message, nil
}

// signInAgain sends someone whose session is gone to the sign-in page, which
// brings them back to next afterwards. sessionEnded is true only when a cookie
// came along: a first visit has no session to end.
func signInAgain(next string, sessionEnded bool) *pageError {
	var pairs [][2]string
	if next != "" {
		pairs = append(pairs, [2]string{"next", next})
	}
	if sessionEnded {
		pairs = append(pairs, [2]string{"notice", sessionEndedNotice})
	}
	return redirectTo(views.WithQuery("/login", pairs))
}

func (a *App) writePageError(w http.ResponseWriter, e *pageError) {
	switch {
	case e.redirect != "":
		redirect(w, e.redirect)
	case e.text != nil:
		e.text.write(w)
	default:
		a.writeErrorPage(w, e)
	}
}

func (a *App) writeErrorPage(w http.ResponseWriter, e *pageError) {
	if e.cause != nil {
		a.logger.Error("internal error", slog.Any("error", e.cause))
	}
	if e.status == http.StatusTooManyRequests {
		seconds := (&apiError{retryAfter: e.retryAfter}).retryAfterSeconds()
		w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
	}
	writeHTML(w, e.status, views.ErrorPage(e.status, e.message))
}

func writeHTML(w http.ResponseWriter, status int, page string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(page))
}

// redirect is a 303: the browser follows a form post with a GET.
func redirect(w http.ResponseWriter, location string) {
	w.Header().Set("Location", location)
	w.WriteHeader(http.StatusSeeOther)
}

// redirectWithNotice is a redirect that carries a fixed notice key, which the
// target page turns into text. path may carry a query already.
func redirectWithNotice(w http.ResponseWriter, path, notice string) {
	redirect(w, views.WithQuery(path, [][2]string{{"notice", notice}}))
}

// sameSitePath is value when it is a path on this site, so that a link or
// redirect built from it cannot lead to another one. A scheme, a // host, a
// backslash (which browsers read as /) and control characters are all refused.
func sameSitePath(value string) bool {
	if len(value) == 0 || value[0] != '/' || len(value) > 1 && value[1] == '/' {
		return false
	}
	for _, r := range value {
		if r == '\\' || r < 0x20 || r >= 0x7f && r <= 0x9f {
			return false
		}
	}
	return true
}

// pathOrHome is value as a same-site path, else the list.
func pathOrHome(value string) string {
	if sameSitePath(value) {
		return value
	}
	return "/"
}
