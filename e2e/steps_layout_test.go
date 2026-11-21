package e2e

import (
	"fmt"

	"github.com/cucumber/godog"

	"github.com/henry40408/bkd/e2e/internal/harness"
)

func registerLayoutSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the window is phone-sized$`, func() error { return w.browser.SetViewport(harness.Phone()) })
	sc.Step(`^the window becomes desktop-sized$`, func() error { return w.browser.SetViewport(harness.Desktop()) })
	sc.Step(`^scripts are switched off$`, func() error { return w.browser.SetScripts(false) })
	sc.Step(`^the browser's own form checks are switched off$`, w.formChecksOff)
	sc.Step(`^the browser refuses the field `+quoted+`$`, withString(w.browserRefusesField))
	sc.Step(`^I open the menu$`, func() error { return w.click(".menu") })
	sc.Step(`^I tap the backdrop$`, w.tapBackdrop)
	sc.Step(`^the sidebar is visible$`, func() error { return w.shownIs("#sidebar", true) })
	sc.Step(`^the sidebar is hidden$`, func() error { return w.shownIs("#sidebar", false) })
	sc.Step(`^the menu button is visible$`, func() error { return w.shownIs(".menu", true) })
	sc.Step(`^the menu button is hidden$`, func() error { return w.shownIs(".menu", false) })
	sc.Step(`^the backdrop covers the page$`, func() error { return w.shownIs(".backdrop", true) })
	sc.Step(`^the backdrop is hidden$`, func() error { return w.shownIs(".backdrop", false) })
	sc.Step(`^the link `+quoted+` is visible without hovering$`, withString(func(label string) error {
		_, err := w.element(fmt.Sprintf("a[aria-label='%s']", label))
		return err
	}))
	sc.Step(`^the page does not scroll sideways$`, w.noSidewaysScroll)
}

// formChecksOff matters because the script is what turns a refusal into a
// message instead of the browser's own bubble. It also lifts the script's own
// gate, which keeps a submit button off until the browser would accept the form.
func (w *world) formChecksOff() error {
	_, err := harness.Exec[any](w.browser, `for (const form of document.forms) {
		form.noValidate = true;
		form.removeAttribute("data-submit-when-valid");
		for (const button of form.querySelectorAll('button[type="submit"]')) button.disabled = false;
	}`)
	return err
}

func (w *world) browserRefusesField(selector string) error {
	field, err := w.element(selector)
	if err != nil {
		return err
	}
	invalid, err := harness.Call[bool](field, "function() { return this.matches(':invalid'); }")
	if err != nil {
		return err
	}
	if !invalid {
		return fmt.Errorf("%s is not refused", selector)
	}
	return nil
}

// tapBackdrop taps the page's right edge: the drawer covers the left of the
// backdrop.
func (w *world) tapBackdrop() error {
	rightOfDrawer := float64(harness.Phone().Width/2) - 10
	return w.clickLocator(harness.CSS(".backdrop"), harness.Placement{OffsetX: rightOfDrawer})
}

func (w *world) shownIs(selector string, shown bool) error {
	return harness.Eventually(fmt.Sprintf("%s shown is %t", selector, shown), func() (bool, error) {
		element, err := w.browser.First(harness.CSS(selector))
		if err != nil {
			return false, err
		}
		displayed, err := element.Displayed()
		return displayed == shown, err
	})
}

func (w *world) noSidewaysScroll() error {
	// The wide elements are named in the error: the cause of a one pixel
	// overflow can differ between browsers and cannot be seen from the number.
	report, err := harness.Exec[string](w.browser, `
		const page = document.scrollingElement;
		const overflow = page.scrollWidth - page.clientWidth;
		if (overflow <= 0) return "";
		const wide = [...document.body.querySelectorAll("*")]
			.filter((el) => el.getBoundingClientRect().right > page.clientWidth)
			.slice(0, 5)
			.map((el) => el.tagName.toLowerCase() + (el.className ? "." + el.className : "") +
				" right=" + el.getBoundingClientRect().right);
		return overflow + "px wider than the window; elements past the edge: " + wide.join(", ");`)
	if err != nil {
		return err
	}
	if report != "" {
		return fmt.Errorf("the page is %s", report)
	}
	return nil
}
