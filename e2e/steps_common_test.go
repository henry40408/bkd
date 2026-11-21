package e2e

import (
	"github.com/chromedp/chromedp/kb"

	"github.com/henry40408/bkd/e2e/internal/harness"
)

// quoted matches a step argument in either quote style: a few features use
// single quotes around a value that holds double quotes.
const quoted = `("[^"]*"|'[^']*')`

// unquote strips the quotes a quoted argument arrived with.
func unquote(argument string) string {
	return argument[1 : len(argument)-1]
}

// withString, withTwoStrings and withThreeStrings adapt a step that takes
// plain strings to the quoted arguments godog hands over.
func withString(step func(string) error) func(string) error {
	return func(a string) error { return step(unquote(a)) }
}

func withTwoStrings(step func(string, string) error) func(string, string) error {
	return func(a, b string) error { return step(unquote(a), unquote(b)) }
}

func withThreeStrings(step func(string, string, string) error) func(string, string, string) error {
	return func(a, b, c string) error { return step(unquote(a), unquote(b), unquote(c)) }
}

// The sidebar link only an admin sees.
const manageUsersLink = "#sidebar a[href='/users']"

// slowFetchAndWatchCSP slows every request down and records each Content
// Security Policy violation.
const slowFetchAndWatchCSP = `window.cspViolations = [];
	document.addEventListener('securitypolicyviolation', (e) => window.cspViolations.push(e.violatedDirective));
	const fetchNow = window.fetch;
	window.fetch = (...args) => new Promise((resolve) => setTimeout(resolve, 500)).then(() => fetchNow(...args));`

// pressEnter presses Enter once in the field, so a form that must refuse the
// submit shows it is the browser stopping it, not only a disabled button.
func (w *world) pressEnter(selector string) error {
	field, err := w.element(selector)
	if err != nil {
		return err
	}
	if err := field.Focus(); err != nil {
		return err
	}
	return w.browser.KeyEvent(kb.Enter)
}

// pressEnterTwice presses Enter twice in a row in the field, as an impatient
// user would. The page's script must send one request and leave the form alone
// for the second key.
func (w *world) pressEnterTwice(selector string) error {
	// A local server answers before the second key arrives; slow requests keep
	// the first one running, as they would over a real network.
	if _, err := harness.Exec[any](w.browser, slowFetchAndWatchCSP); err != nil {
		return err
	}
	field, err := w.element(selector)
	if err != nil {
		return err
	}
	if err := field.Focus(); err != nil {
		return err
	}
	return w.browser.KeyEvent(kb.Enter + kb.Enter)
}
