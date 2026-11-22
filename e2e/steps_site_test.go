package e2e

import (
	"fmt"

	"github.com/cucumber/godog"

	"github.com/henry40408/bkd/e2e/internal/harness"
)

func registerSiteSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I have the bookmark `+quoted+` titled `+quoted+` whose page says `+quoted+` and `+quoted+`$`,
		func(url, title, pageTitle, description string) error {
			if err := w.createBookmark(harness.NewBookmark{URL: unquote(url), Title: unquote(title)}); err != nil {
				return err
			}
			return w.server.SetSite(unquote(url), unquote(pageTitle), unquote(description))
		})
	sc.Step(`^the add form says `+quoted+`$`, withString(func(text string) error { return w.textContains(addForm, text) }))
	sc.Step(`^the page has asked the server to read an address (\d+) times?$`, w.pageAskedToReadTimes)
	sc.Step(`^I read the page again in the add form$`, func() error { return w.click(addForm + " [data-site-reread]") })
	sc.Step(`^I read the page again in the edit form$`, func() error { return w.click(editForm + " [data-site-reread]") })
	sc.Step(`^the read again button of the add form is no wider than its text$`, func() error { return w.readAgainButtonIsCompact(addForm) })
	sc.Step(`^the read again button of the edit form is no wider than its text$`, func() error { return w.readAgainButtonIsCompact(editForm) })
	sc.Step(`^I clear the `+quoted+` field of the edit form$`, withString(func(name string) error {
		return w.fill(fieldOf(editForm, name), "")
	}))
	sc.Step(`^I type `+quoted+` in the `+quoted+` field of the edit form$`, withTwoStrings(func(text, name string) error {
		return w.fill(fieldOf(editForm, name), text)
	}))
	sc.Step(`^the `+quoted+` field of the edit form holds `+quoted+`$`, withTwoStrings(func(name, expected string) error {
		return w.fieldHolds(editForm, name, expected)
	}))
	sc.Step(`^the `+quoted+` field of the add form shows `+quoted+` as its placeholder$`, withTwoStrings(func(name, expected string) error {
		return w.placeholderIs(addForm, name, expected)
	}))
	sc.Step(`^the `+quoted+` field of the edit form shows `+quoted+` as its placeholder$`, withTwoStrings(func(name, expected string) error {
		return w.placeholderIs(editForm, name, expected)
	}))
	sc.Step(`^the `+quoted+` field of the edit form has no placeholder$`, withString(func(name string) error {
		return w.placeholderIs(editForm, name, "")
	}))
}

func fieldOf(form, name string) string { return fmt.Sprintf("%s [name=%s]", form, name) }

// maxCompactButtonShare is the largest part of its form's width the read again
// button may take. A grid stretches a button to the whole row unless the
// stylesheet says otherwise, and the stretched button looked like a banner.
const maxCompactButtonShare = 0.5

func (w *world) readAgainButtonIsCompact(form string) error {
	script := fmt.Sprintf(`(() => {
		const button = document.querySelector(%q);
		return button.getBoundingClientRect().width / button.closest("form").getBoundingClientRect().width;
	})()`, form+" [data-site-reread]")
	share, err := harness.Eval[float64](w.browser, script)
	if err != nil {
		return err
	}
	if share > maxCompactButtonShare {
		return fmt.Errorf("the read again button takes %.0f%% of the width of its form", share*100)
	}
	return nil
}

// pageAskedToReadTimes waits for the page to have sent that many page-detail
// requests. The status line says the same after a failed read and after a
// failed read again, so only the request count shows that the button worked.
func (w *world) pageAskedToReadTimes(expected int) error {
	const countRequests = `performance.getEntriesByType("resource").filter(entry => entry.name.endsWith("/api/site-preview")).length`
	return harness.Eventually(fmt.Sprintf("the page has asked the server to read an address %d times", expected), func() (bool, error) {
		count, err := harness.Eval[int](w.browser, countRequests)
		return count == expected, err
	})
}

// placeholderIs waits for the field's placeholder to be expected; "" means none.
// The script changes placeholders after a request, so the first look may be early.
func (w *world) placeholderIs(form, name, expected string) error {
	return harness.Eventually(fmt.Sprintf("the %s field shows %q as its placeholder", name, expected), func() (bool, error) {
		field, err := w.browser.First(harness.CSS(fieldOf(form, name)))
		if err != nil {
			return false, err
		}
		actual, _, err := field.Attr("placeholder")
		return actual == expected, err
	})
}
