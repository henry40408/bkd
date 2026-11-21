package e2e

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	"github.com/henry40408/bkd/e2e/internal/harness"
)

const (
	movedButton = editForm + " button[data-moved-to]"
	// dropHint is the line that only works with the script.
	dropHint = ".drop [data-script-only]"
	// maxIconControl is the widest or tallest an icon-only control may be, in pixels.
	maxIconControl = 60
	// maxClippedLabel is the widest a visually hidden label may be, in pixels.
	maxClippedLabel = 2
)

func registerRedesignSteps(sc *godog.ScenarioContext, w *world) {
	registerRedesignSetupSteps(sc, w)
	registerRedesignActionSteps(sc, w)
	registerRedesignAssertionSteps(sc, w)
}

func registerRedesignSetupSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I have (\d+) bookmarks tagged `+quoted+`$`, func(count int, tag string) error {
		return w.haveTaggedBulkBookmarks(count, unquote(tag))
	})
	sc.Step(`^I have the bookmark `+quoted+` titled `+quoted+` whose link moved to `+quoted+`$`,
		withThreeStrings(func(url, title, target string) error {
			return w.haveBookmarkWithLink(url, title, "redirected", target)
		}))
	sc.Step(`^I have the bookmark `+quoted+` titled `+quoted+` whose link is broken$`,
		withTwoStrings(func(url, title string) error {
			return w.haveBookmarkWithLink(url, title, "broken", "HTTP 404")
		}))
}

func registerRedesignActionSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I focus Load more$`, func() error {
		link, err := w.element(loadMoreLink)
		if err != nil {
			return err
		}
		return link.Focus()
	})
	sc.Step(`^I choose the filter chip `+quoted+`$`, withString(w.chooseFilterChip))
	sc.Step(`^I use the new address$`, func() error { return w.click(movedButton) })
	sc.Step(`^I save the edit$`, func() error { return w.click(editForm + " button[type=submit]") })
	sc.Step(`^I change the title in the edit form to `+quoted+`$`, withString(func(title string) error {
		return w.fill(editForm+" input[name=title]", title)
	}))
	sc.Step(`^I choose Edit in the pick$`, func() error { return w.clickLink("section.pick", "Edit") })
	sc.Step(`^I choose Delete in the pick$`, func() error { return w.clickLink("section.pick", "Delete") })
	sc.Step(`^I type `+quoted+`$`, withString(func(keys string) error { return w.browser.KeyEvent(keys) }))
	sc.Step(`^I focus the `+quoted+` field of the add form$`, withString(w.focusAddFormField))
	sc.Step(`^I watch the keys$`, func() error {
		_, err := harness.Exec[any](w.browser, watchKeysScript)
		return err
	})
}

func registerRedesignAssertionSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the heading says `+quoted+`$`, withString(func(text string) error { return w.textContains("main h1", text) }))
	sc.Step(`^the count says `+quoted+`$`, withString(func(text string) error { return w.textContains(".head .count", text) }))
	sc.Step(`^the filter chip says `+quoted+`$`, withString(func(text string) error { return w.textContains(".active", text) }))
	sc.Step(`^there is no filter chip$`, w.noFilterChip)
	sc.Step(`^the edit form says `+quoted+`$`, withString(func(text string) error { return w.textContains(editForm, text) }))
	sc.Step(`^the edit form holds `+quoted+` as the URL$`, withString(func(url string) error {
		return w.fieldHolds(editForm, "url", url)
	}))
	sc.Step(`^the new address button is offered$`, func() error { return w.newAddressButtonIs(true) })
	sc.Step(`^the new address button is not offered$`, func() error { return w.newAddressButtonIs(false) })
	sc.Step(`^the edit form is inside the pick$`, func() error { _, err := w.element("section.pick " + editForm); return err })
	sc.Step(`^the delete question is closed$`, w.deleteQuestionClosed)
	sc.Step(`^the `+quoted+` field of the add form holds `+quoted+`$`, withTwoStrings(func(name, expected string) error {
		return w.fieldHolds(addForm, name, expected)
	}))
	sc.Step(`^the page took the slash key$`, w.pageTookSlashKey)
	sc.Step(`^the control labelled `+quoted+` shows only its icon$`, withString(w.showsOnlyItsIcon))
	sc.Step(`^the import box offers dropping a file$`, func() error { return w.dropHintIs(true) })
	sc.Step(`^the import box does not offer dropping a file$`, func() error { return w.dropHintIs(false) })
}

// watchKeysScript records whether the page cancelled each key press. The
// browser under test is driven through the DevTools protocol, which delivers
// the typed character even after the page cancelled the key press, so the
// field's text cannot show that a real browser would have left it out.
const watchKeysScript = `window.keyCancelled = [];
	window.addEventListener('keydown', (event) => window.keyCancelled.push([event.key, event.defaultPrevented]));`

func (w *world) pageTookSlashKey() error {
	presses, err := harness.Exec[[][]any](w.browser, "return window.keyCancelled")
	if err != nil {
		return err
	}
	if len(presses) != 1 || presses[0][0] != "/" || presses[0][1] != true {
		return fmt.Errorf("the page did not cancel the slash key: %v", presses)
	}
	return nil
}

// haveBookmarkWithLink makes a bookmark and stores a link check result on it:
// the checker only contacts public addresses, so no real check can say this.
func (w *world) haveBookmarkWithLink(url, title, status, detail string) error {
	if err := w.createBookmark(harness.NewBookmark{URL: url, Title: title}); err != nil {
		return err
	}
	return w.server.SetLinkCheck(url, status, detail)
}

// chooseFilterChip clicks the chip that removes a filter; its label is followed
// by the words "Remove filter".
func (w *world) chooseFilterChip(label string) error {
	chip := fmt.Sprintf("//div[contains(@class, 'active')]/a[starts-with(normalize-space(.), %s)]", xpathString(label))
	return w.clickLocator(harness.XPath(chip), harness.Placement{})
}

func (w *world) noFilterChip() error {
	return harness.Eventually("no filter chip is shown", func() (bool, error) {
		chips, err := w.browser.All(harness.CSS(".active .chip"))
		return len(chips) == 0, err
	})
}

// newAddressButtonIs waits until the Use new address button of the edit form
// is on screen, or is in the page but hidden.
func (w *world) newAddressButtonIs(offered bool) error {
	return harness.Eventually(fmt.Sprintf("the new address button is offered: %t", offered), func() (bool, error) {
		button, err := w.browser.First(harness.CSS(movedButton))
		if err != nil {
			return false, err
		}
		shown, err := button.Displayed()
		if err != nil || shown != offered {
			return false, err
		}
		text, err := button.Text()
		return !offered || text == "Use new address", err
	})
}

func (w *world) deleteQuestionClosed() error {
	return harness.Eventually("the delete question is closed", func() (bool, error) {
		questions, err := w.browser.All(harness.CSS("main form:has(.danger-button)"))
		return len(questions) == 0, err
	})
}

func (w *world) focusAddFormField(name string) error {
	if err := w.openAddForm(); err != nil {
		return err
	}
	field, err := w.element(fmt.Sprintf("%s [name=%s]", addForm, name))
	if err != nil {
		return err
	}
	return field.Focus()
}

func (w *world) fieldHolds(form, name, expected string) error {
	field, err := w.element(fmt.Sprintf("%s [name=%s]", form, name))
	if err != nil {
		return err
	}
	actual, err := field.Value()
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("the %s field holds %q", name, actual)
	}
	return nil
}

// iconControlProbe reads what a phone-sized control shows and what it is called.
const iconControlProbe = `function() {
	const label = this.querySelector('.lbl');
	const box = this.getBoundingClientRect();
	const hidden = label ? label.getBoundingClientRect() : { width: -1, height: -1 };
	return JSON.stringify({
		label: label ? label.textContent.trim() : '',
		name: this.getAttribute('aria-label') || this.textContent,
		width: box.width, height: box.height,
		labelWidth: hidden.width, labelHeight: hidden.height,
	});
}`

type iconControl struct {
	Label       string  `json:"label"`
	Name        string  `json:"name"`
	Width       float64 `json:"width"`
	Height      float64 `json:"height"`
	LabelWidth  float64 `json:"labelWidth"`
	LabelHeight float64 `json:"labelHeight"`
}

// showsOnlyItsIcon finds a control by its aria-label or title. Its label must
// stay in the name that a screen reader gets, while the box is small and the
// label is clipped to a point.
func (w *world) showsOnlyItsIcon(name string) error {
	control, err := w.browser.Await(harness.XPath(fmt.Sprintf(
		"//*[(@aria-label=%[1]s or @title=%[1]s)][.//*[contains(concat(' ', normalize-space(@class), ' '), ' lbl ')]]",
		xpathString(name))))
	if err != nil {
		return err
	}
	encoded, err := harness.Call[string](control, iconControlProbe)
	if err != nil {
		return err
	}
	var probe iconControl
	if err := json.Unmarshal([]byte(encoded), &probe); err != nil {
		return fmt.Errorf("reading %s: %w", encoded, err)
	}
	return probe.check(name)
}

func (probe iconControl) check(name string) error {
	if probe.Label == "" || !strings.Contains(probe.Name, probe.Label) {
		return fmt.Errorf("%s is named %q, which lacks its label %q", name, probe.Name, probe.Label)
	}
	if probe.Width > maxIconControl || probe.Height > maxIconControl {
		return fmt.Errorf("%s is %vx%v px, wider than an icon", name, probe.Width, probe.Height)
	}
	if probe.LabelWidth > maxClippedLabel || probe.LabelHeight > maxClippedLabel {
		return fmt.Errorf("the label of %s is %vx%v px on screen", name, probe.LabelWidth, probe.LabelHeight)
	}
	return nil
}

func (w *world) dropHintIs(offered bool) error {
	return harness.Eventually(fmt.Sprintf("the drop hint is offered: %t", offered), func() (bool, error) {
		hint, err := w.browser.First(harness.CSS(dropHint))
		if err != nil {
			return false, err
		}
		shown, err := hint.Displayed()
		if err != nil || shown != offered {
			return false, err
		}
		text, err := hint.Text()
		return !offered || text == "Or drop the file in this box.", err
	})
}
