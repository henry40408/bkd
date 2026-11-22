package e2e

import (
	"fmt"

	"github.com/cucumber/godog"

	"github.com/henry40408/bkd/e2e/internal/harness"
)

const (
	siteTitleButton       = editForm + " button[data-site-title]"
	siteDescriptionButton = editForm + " button[data-site-description]"
)

func registerSiteSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I have the bookmark `+quoted+` titled `+quoted+` whose page says `+quoted+` and `+quoted+`$`,
		func(url, title, pageTitle, description string) error {
			if err := w.createBookmark(harness.NewBookmark{URL: unquote(url), Title: unquote(title)}); err != nil {
				return err
			}
			return w.server.SetSite(unquote(url), unquote(pageTitle), unquote(description))
		})
	sc.Step(`^I refresh the page details of `+quoted+`$`, withString(w.refreshPageDetails))
	sc.Step(`^I use the page title$`, func() error { return w.click(siteTitleButton) })
	sc.Step(`^I use the page description$`, func() error { return w.click(siteDescriptionButton) })
	sc.Step(`^the page title button is not offered$`, func() error {
		return w.buttonIsGone("the page title button", siteTitleButton)
	})
	sc.Step(`^the page description button is not offered$`, func() error {
		return w.buttonIsGone("the page description button", siteDescriptionButton)
	})
	sc.Step(`^the `+quoted+` field of the edit form holds `+quoted+`$`, withTwoStrings(func(name, expected string) error {
		return w.fieldHolds(editForm, name, expected)
	}))
}

// refreshPageDetails presses a row's refresh button, which shows only while the
// pointer is over the row.
func (w *world) refreshPageDetails(title string) error {
	row, err := w.browser.Await(harness.XPath(rowOf(title)))
	if err != nil {
		return err
	}
	if err := row.Hover(); err != nil {
		return err
	}
	return w.click(fmt.Sprintf("button[aria-label='Refresh page details of %s']", title))
}

func (w *world) buttonIsGone(name, selector string) error {
	return harness.Eventually(name+" is not offered", func() (bool, error) {
		button, err := w.browser.First(harness.CSS(selector))
		if err != nil {
			return false, err
		}
		shown, err := button.Displayed()
		return !shown, err
	})
}
