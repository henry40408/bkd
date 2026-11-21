package e2e

import (
	"fmt"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/henry40408/bkd/e2e/internal/harness"
)

const (
	loginForm     = "form[action='/login']"
	setupForm     = "form[action='/setup']"
	confirmForm   = "form[action='/confirm']"
	passwordForm  = ".password-form"
	searchBoxPath = "[role=search] input[name=q]"
)

func registerSessionSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the admin `+quoted+` exists$`, withString(w.adminExists))
	sc.Step(`^I am signed in as the admin `+quoted+`$`, withString(w.signedInAsAdmin))
	sc.Step(`^I open the site$`, func() error { return w.goTo("/") })
	sc.Step(`^I open the site at `+quoted+`$`, withString(w.goTo))
	sc.Step(`^I reload the page$`, func() error { return w.browser.Reload() })
	sc.Step(`^I go back$`, func() error { return w.browser.Back() })
	sc.Step(`^the address is `+quoted+`$`, withString(w.addressIs))
	sc.Step(`^the search box holds `+quoted+`$`, withString(w.searchBoxHolds))
	sc.Step(`^I (?:still )?see the setup page$`, func() error { _, err := w.element(setupForm); return err })
	sc.Step(`^I (?:still )?see the sign-in page$`, func() error { _, err := w.element(loginForm); return err })
	sc.Step(`^I create the first admin `+quoted+` with the setup token$`, withString(w.createFirstAdmin))
	sc.Step(`^I try to create the first admin `+quoted+` with the token `+quoted+`$`, withTwoStrings(w.tryCreateFirstAdmin))
	sc.Step(`^I sign in as `+quoted+` with the right password$`, withString(w.signInRightPassword))
	sc.Step(`^I sign in as `+quoted+` with a wrong password$`, withString(w.signInWrongPassword))
	sc.Step(`^I sign in as `+quoted+` in the browser$`, withString(w.signInInBrowser))
	sc.Step(`^I press Enter twice to sign in as `+quoted+`$`, withString(w.pressEnterTwiceToSignIn))
	sc.Step(`^my session stops working$`, w.sessionStopsWorking)
	sc.Step(`^I sign out$`, func() error { return w.click(".me button") })
	sc.Step(`^I am signed in as `+quoted+`$`, withString(w.signedInAs))
	sc.Step(`^I can open Manage users$`, func() error { _, err := w.element(manageUsersLink); return err })
	sc.Step(`^I see the error `+quoted+`$`, withString(w.seesError))
	sc.Step(`^the sign-in page says exactly `+quoted+`$`, withString(w.signInPageSaysExactly))
	sc.Step(`^the address has no query string$`, w.addressHasNoQuery)
	sc.Step(`^I open Account$`, w.openAccount)
	sc.Step(`^I mark the page$`, w.markPage)
	sc.Step(`^the page was not reloaded$`, w.pageWasNotReloaded)
	sc.Step(`^I click the bkd brand$`, func() error { return w.click("#sidebar a.brand") })
	sc.Step(`^I change my password from `+quoted+` to `+quoted+`$`, withTwoStrings(w.changePassword))
	sc.Step(`^I change my password from `+quoted+` to `+quoted+` but repeat `+quoted+`$`, withThreeStrings(w.submitPasswordForm))
	sc.Step(`^I am also signed in on another browser$`, w.alsoSignedInElsewhere)
	sc.Step(`^the list of browsers has (\d+) rows$`, w.browserListHasRows)
	sc.Step(`^I sign out the other browser$`, func() error { return w.click(".session-list button") })
	sc.Step(`^I sign out all other browsers$`, func() error {
		return w.click("form[action='/account/sessions/revoke-others'] button")
	})
	sc.Step(`^my password confirmation is old$`, func() error { return w.server.AgeConfirmations() })
	sc.Step(`^I am asked to confirm my password$`, func() error { _, err := w.element(confirmForm); return err })
	sc.Step(`^I confirm my password with `+quoted+`$`, withString(w.confirmPassword))
	sc.Step(`^I cancel the confirmation$`, func() error { return w.click(confirmForm + " a.quiet-link") })
	sc.Step(`^the browser reports no policy violation$`, w.noPolicyViolation)
}

func (w *world) adminExists(username string) error {
	session, err := w.api.Setup(username, harness.Password)
	if err != nil {
		return err
	}
	w.sessions[username] = session
	return nil
}

func (w *world) signedInAsAdmin(username string) error {
	if err := w.adminExists(username); err != nil {
		return err
	}
	w.me = username
	if err := w.browser.SetSessionCookie(w.server.BaseURL, w.sessions[username].Token); err != nil {
		return err
	}
	if err := w.goTo("/"); err != nil {
		return err
	}
	return w.signedInAs(username)
}

// addressIs waits for the address: the script changes it after a click.
func (w *world) addressIs(expected string) error {
	var last string
	err := harness.Eventually(fmt.Sprintf("the address is %q", expected), func() (bool, error) {
		current, err := w.browser.URL()
		if err != nil {
			return false, err
		}
		last = current.EscapedPath()
		if current.RawQuery != "" {
			last += "?" + current.RawQuery
		}
		return last == expected, nil
	})
	if err != nil {
		return fmt.Errorf("%w (the address was %q)", err, last)
	}
	return nil
}

func (w *world) searchBoxHolds(expected string) error {
	field, err := w.element(searchBoxPath)
	if err != nil {
		return err
	}
	actual, err := field.Value()
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("the search box holds %q", actual)
	}
	return nil
}

func (w *world) createFirstAdmin(username string) error {
	if err := w.submitSetupForm(w.server.SetupToken, username); err != nil {
		return err
	}
	w.me = username
	return nil
}

func (w *world) tryCreateFirstAdmin(username, token string) error {
	return w.submitSetupForm(token, username)
}

func (w *world) submitSetupForm(token, username string) error {
	fields := [][2]string{{"token", token}, {"username", username}, {"password", harness.Password}}
	for _, field := range fields {
		if err := w.fill(fmt.Sprintf("%s input[name=%s]", setupForm, field[0]), field[1]); err != nil {
			return err
		}
	}
	return w.click(setupForm + " button[type=submit]")
}

func (w *world) signInRightPassword(username string) error {
	if err := w.submitLoginForm(username, harness.Password); err != nil {
		return err
	}
	return w.landedSignedIn(username)
}

func (w *world) signInWrongPassword(username string) error {
	return w.submitLoginForm(username, "not-the-password")
}

// signInInBrowser starts from a signed-out browser, since the scenario's
// background may have signed another account in.
func (w *world) signInInBrowser(username string) error {
	if err := w.browser.ClearCookies(); err != nil {
		return err
	}
	if err := w.goTo("/"); err != nil {
		return err
	}
	if err := w.submitLoginForm(username, harness.Password); err != nil {
		return err
	}
	return w.landedSignedIn(username)
}

func (w *world) landedSignedIn(username string) error {
	// Navigating away before the sign-in lands would cancel it.
	if err := w.textContains(".me strong", username); err != nil {
		return err
	}
	w.me = username
	return nil
}

func (w *world) submitLoginForm(username, password string) error {
	if err := w.fill(loginForm+" input[name=username]", username); err != nil {
		return err
	}
	if err := w.fill(loginForm+" input[name=password]", password); err != nil {
		return err
	}
	return w.click(loginForm + " button[type=submit]")
}

func (w *world) pressEnterTwiceToSignIn(username string) error {
	passwordField := loginForm + " input[name=password]"
	if err := w.fill(loginForm+" input[name=username]", username); err != nil {
		return err
	}
	if err := w.fill(passwordField, harness.Password); err != nil {
		return err
	}
	if err := w.pressEnterTwice(passwordField); err != nil {
		return err
	}
	w.me = username
	return nil
}

// sessionStopsWorking is what expiry or a server-side sign-out looks like to
// the page: the cookie is still sent, but names no session. A missing cookie
// would look like a first visit instead.
func (w *world) sessionStopsWorking() error {
	return w.browser.SetSessionCookie(w.server.BaseURL, "ended")
}

func (w *world) signedInAs(username string) error {
	return w.textContains(".me strong", username)
}

func (w *world) seesError(expected string) error {
	err := harness.Eventually(fmt.Sprintf("an error says %q", expected), func() (bool, error) {
		alerts, err := w.browser.All(harness.CSS("[role=alert]"))
		if err != nil {
			return false, err
		}
		for _, alert := range alerts {
			text, err := alert.Text()
			if err != nil {
				return false, err
			}
			if strings.Contains(strings.ToLower(text), strings.ToLower(expected)) {
				return true, nil
			}
		}
		return false, nil
	})
	if err == nil {
		return nil
	}
	body, _ := w.pageText()
	return fmt.Errorf("%w; the page shows: %s", err, body)
}

func (w *world) pageText() (string, error) {
	body, err := w.browser.First(harness.CSS("body"))
	if err != nil {
		return "", err
	}
	return body.Text()
}

func (w *world) signInPageSaysExactly(expected string) error {
	notice, err := w.element(loginForm + " .notice")
	if err != nil {
		return err
	}
	text, err := notice.Text()
	if err != nil {
		return err
	}
	if strings.TrimSpace(text) != expected {
		return fmt.Errorf("the sign-in page says %q", text)
	}
	return nil
}

func (w *world) addressHasNoQuery() error {
	current, err := w.browser.URL()
	if err != nil {
		return err
	}
	if current.RawQuery != "" || current.ForceQuery {
		return fmt.Errorf("the address is %s", current)
	}
	return nil
}

func (w *world) openAccount() error {
	if err := w.click("#sidebar a[href='/account']"); err != nil {
		return err
	}
	_, err := w.element(passwordForm)
	return err
}

func (w *world) markPage() error {
	_, err := harness.Exec[any](w.browser, "window.pageMarker = true")
	return err
}

func (w *world) pageWasNotReloaded() error {
	marked, err := harness.Exec[bool](w.browser, "return window.pageMarker === true")
	if err != nil {
		return err
	}
	if !marked {
		return fmt.Errorf("the page reloaded")
	}
	return nil
}

func (w *world) changePassword(current, replacement string) error {
	return w.submitPasswordForm(current, replacement, replacement)
}

func (w *world) submitPasswordForm(current, replacement, again string) error {
	fields := [][2]string{{"current", current}, {"new", replacement}, {"again", again}}
	for _, field := range fields {
		if err := w.fill(fmt.Sprintf("%s input[name=%s]", passwordForm, field[0]), field[1]); err != nil {
			return err
		}
	}
	return w.click(passwordForm + " button[type=submit]")
}

func (w *world) alsoSignedInElsewhere() error {
	if w.me == "" {
		return fmt.Errorf("nobody is signed in")
	}
	session, err := w.api.Login(w.me, harness.Password)
	if err != nil {
		return err
	}
	w.sessions["elsewhere"] = session
	return nil
}

func (w *world) browserListHasRows(expected int) error {
	return harness.Eventually(fmt.Sprintf("the list has %d rows", expected), func() (bool, error) {
		rows, err := w.browser.All(harness.CSS(".session-list .user-row"))
		return len(rows) == expected, err
	})
}

func (w *world) confirmPassword(password string) error {
	if err := w.fill(confirmForm+" input[name=password]", password); err != nil {
		return err
	}
	return w.click(confirmForm + " button[type=submit]")
}

func (w *world) noPolicyViolation() error {
	// The violation event fires asynchronously after a rejected submission.
	time.Sleep(500 * time.Millisecond)
	violations, err := harness.Exec[*[]string](w.browser, "return window.cspViolations")
	if err != nil {
		return err
	}
	if violations == nil {
		return fmt.Errorf("the violation watcher is gone: the page reloaded")
	}
	if len(*violations) != 0 {
		return fmt.Errorf("the browser reported %v", *violations)
	}
	return nil
}
