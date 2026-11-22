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
