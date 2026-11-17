package e2e

import (
	"context"
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	"github.com/henry40408/bkd/e2e/internal/harness"
)

// world is the state of one scenario: its server, its browser and the accounts
// it made.
type world struct {
	binary  string
	server  *harness.Server
	api     *harness.Api
	browser *harness.Browser
	// sessions are the accounts created through the API, by username.
	sessions map[string]harness.Session
	// me is the account the browser is signed in as, once a step signs it in.
	me string
	// bulkCount is how many bulk bookmarks exist, so each batch gets fresh titles.
	bulkCount int
}

// initializeScenario runs once per scenario, so every scenario gets its own
// world through the closure.
func initializeScenario(scenario *godog.ScenarioContext, binary string) {
	w := &world{binary: binary, sessions: map[string]harness.Session{}}
	scenario.Before(w.start)
	scenario.After(w.stop)
	registerSessionSteps(scenario, w)
	registerBookmarkSteps(scenario, w)
	registerBulkSteps(scenario, w)
	registerKeyboardSteps(scenario, w)
	registerLayoutSteps(scenario, w)
	registerLinkSteps(scenario, w)
	registerTransferSteps(scenario, w)
	registerUserSteps(scenario, w)
}

func (w *world) start(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
	server, err := harness.StartServer(w.binary)
	if err != nil {
		return ctx, fmt.Errorf("could not start the server: %w", err)
	}
	w.server = server
	w.api = harness.NewApi(server)
	browser, err := harness.Open()
	if err != nil {
		return ctx, fmt.Errorf("could not open a browser session: %w", err)
	}
	w.browser = browser
	return ctx, nil
}

// stop runs on every outcome, so a failed scenario leaves no Chrome and no
// server behind.
func (w *world) stop(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
	if w.browser != nil {
		w.browser.Close()
		w.browser = nil
	}
	if w.server != nil {
		w.server.Stop()
		w.server = nil
	}
	return ctx, nil
}

func (w *world) session(username string) (harness.Session, error) {
	session, found := w.sessions[username]
	if !found {
		return harness.Session{}, fmt.Errorf("no account %q: no step created it", username)
	}
	return session, nil
}

// mySession is the session of the account the browser is signed in as.
func (w *world) mySession() (harness.Session, error) {
	if w.me == "" {
		return harness.Session{}, fmt.Errorf("no step signed an account in")
	}
	return w.session(w.me)
}

func (w *world) goTo(path string) error {
	return w.browser.Goto(w.server.BaseURL + path)
}

// element is the first element matching the selector that is on screen,
// waiting for it.
func (w *world) element(selector string) (*harness.Element, error) {
	return w.browser.Await(harness.CSS(selector))
}

// fill looks the field up again for every attempt: a page that rebuilds itself
// mid-way leaves the field found first stale, or its text in the wrong place.
func (w *world) fill(selector, text string) error {
	return harness.Eventually("filling in "+selector, func() (bool, error) {
		field, err := w.element(selector)
		if err != nil {
			return false, err
		}
		return true, field.Fill(text)
	})
}

func (w *world) click(selector string) error {
	return w.clickLocator(harness.CSS(selector), harness.Placement{})
}

// clickLocator clicks the first element on screen that the locator finds. The
// element is looked up again for every attempt, because the page may rebuild
// it between two.
func (w *world) clickLocator(locator harness.Locator, placement harness.Placement) error {
	return w.clickFollowing(locator.String(), func() (*harness.Element, error) {
		return w.browser.Await(locator)
	}, placement)
}

// clickFollowing clicks the element that locate finds and, for a link, waits
// until the page shows where it leads. The script follows a link with a
// request, so the old page stays for a moment, and a step that came next would
// act on it and lose its work when the new page replaced it.
func (w *world) clickFollowing(what string, locate func() (*harness.Element, error), placement harness.Placement) error {
	var destination string
	err := w.browser.Click(what, func() (*harness.Element, error) {
		element, err := locate()
		if err != nil {
			return nil, err
		}
		destination, err = harness.Call[string](element, "function() { return this.tagName === 'A' ? this.href : ''; }")
		return element, err
	}, placement)
	if err != nil || destination == "" {
		return err
	}
	w.browser.WaitForAddress(destination)
	return nil
}

// expectText waits until the visible text of the page contains text.
func (w *world) expectText(text string) error {
	return w.textContains("body", text)
}

// textContains waits until the text of the element matching selector contains
// expected. The element may not exist yet, or may be rebuilt mid-poll; both
// count as "not yet".
func (w *world) textContains(selector, expected string) error {
	return w.eventuallyText(selector+" contains "+expected, selector, func(text string) bool {
		return strings.Contains(text, expected)
	})
}

// textLacks waits until the text of the element matching selector no longer
// contains unexpected.
func (w *world) textLacks(selector, unexpected string) error {
	return w.eventuallyText(selector+" no longer contains "+unexpected, selector, func(text string) bool {
		return !strings.Contains(text, unexpected)
	})
}

func (w *world) eventuallyText(what, selector string, holds func(string) bool) error {
	read := func() (string, error) {
		element, err := w.browser.First(harness.CSS(selector))
		if err != nil {
			return "", err
		}
		return element.Text()
	}
	return harness.EventuallyText(what, read, holds)
}

// clickLink clicks the link whose text is exactly text, inside the element
// matching scope.
func (w *world) clickLink(scope, text string) error {
	// The page may still be replacing itself after the last click, which makes
	// the elements found a moment ago stale; looking again finds the new ones.
	link := harness.XPath(fmt.Sprintf(".//a[normalize-space(.)=%s]", xpathString(text)))
	return w.clickFollowing("the link "+text+" in "+scope, func() (*harness.Element, error) {
		container, err := w.element(scope)
		if err != nil {
			return nil, err
		}
		return container.Await(link)
	}, harness.Placement{})
}

// xpathString quotes text as an XPath string literal.
func xpathString(text string) string {
	if !strings.Contains(text, "'") {
		return "'" + text + "'"
	}
	return `"` + text + `"`
}
