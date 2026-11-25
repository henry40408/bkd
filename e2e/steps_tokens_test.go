package e2e

import (
	"fmt"
	"regexp"

	"github.com/cucumber/godog"

	"github.com/henry40408/bkd/e2e/internal/harness"
)

const (
	tokenForm     = "form[action='/settings/tokens']"
	newTokenField = "input[aria-label='New API token']"
)

var tokenSecret = regexp.MustCompile(`^[0-9a-f]{64}$`)

func registerTokenSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I create the API token `+quoted+`$`, withString(w.createAPIToken))
	sc.Step(`^I try to create the API token `+quoted+`$`, withString(w.tryCreateAPIToken))
	sc.Step(`^I am shown the new token once$`, w.seeNewToken)
	sc.Step(`^the token list shows `+quoted+`$`, withString(func(name string) error { return w.textContains("#main", name) }))
	sc.Step(`^the token list does not show `+quoted+`$`, withString(func(name string) error { return w.textLacks("#main", name) }))
	sc.Step(`^I revoke the API token `+quoted+`$`, withString(w.revokeAPIToken))
	sc.Step(`^the page no longer shows the new token$`, func() error {
		fields, err := w.browser.All(harness.CSS(newTokenField))
		if err != nil {
			return err
		}
		if len(fields) != 0 {
			return fmt.Errorf("the page still shows the new token")
		}
		return nil
	})
}

// createAPIToken waits for the new token to be listed: the script swaps the page
// in when the answer arrives, and a step that came next would act on the old one.
// The token field alone is no sign, since the page of an earlier token has one.
func (w *world) createAPIToken(name string) error {
	if err := w.tryCreateAPIToken(name); err != nil {
		return err
	}
	if err := w.textContains("#main", name); err != nil {
		return err
	}
	_, err := w.element(newTokenField)
	return err
}

func (w *world) tryCreateAPIToken(name string) error {
	if err := w.fill(tokenForm+" input[name=name]", name); err != nil {
		return err
	}
	return w.click(tokenForm + " button[type=submit]")
}

func (w *world) seeNewToken() error {
	field, err := w.element(newTokenField)
	if err != nil {
		return err
	}
	secret, err := field.Value()
	if err != nil {
		return err
	}
	if !tokenSecret.MatchString(secret) {
		return fmt.Errorf("the new token is %q, not 64 hex digits", secret)
	}
	return nil
}

func (w *world) revokeAPIToken(name string) error {
	return w.click(fmt.Sprintf("button[aria-label='Revoke %s']", name))
}
