package e2e

import (
	"fmt"

	"github.com/chromedp/chromedp"
	"github.com/cucumber/godog"

	"github.com/henry40408/bkd/e2e/internal/harness"
)

const (
	bulkBar = ".bulk"
	// The form of the search bar lays out as part of the bar, so the bar is the link scope.
	searchTools  = ".bar"
	bulkTagsPath = ".bulk input[name=tags]"
)

func registerBulkSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I enter select mode$`, func() error { return w.clickLink(searchTools, "Select") })
	sc.Step(`^I leave select mode$`, func() error { return w.clickLink(bulkBar, "Done") })
	sc.Step(`^I tick `+quoted+`$`, withString(func(title string) error { return w.click(checkbox(title)) }))
	sc.Step(`^I Shift-click `+quoted+`$`, withString(w.shiftClick))
	sc.Step(`^I select all on this page$`, func() error { return w.clickLink(bulkBar, "Select all on this page") })
	sc.Step(`^I clear the selection$`, func() error { return w.clickLink(bulkBar, "Clear selection") })
	sc.Step(`^I add the tag `+quoted+` to the selection$`, withString(func(tags string) error {
		return w.submitBulkTags("add_tags", tags)
	}))
	sc.Step(`^I remove the tag `+quoted+` from the selection$`, withString(func(tags string) error {
		return w.submitBulkTags("remove_tags", tags)
	}))
	sc.Step(`^I start deleting the selection$`, func() error { return w.pressBulkButton("delete") })
	sc.Step(`^I check the links of the selection$`, func() error { return w.pressBulkButton("check") })
	sc.Step(`^the selection count says `+quoted+`$`, withString(func(text string) error {
		return w.textContains("#bulk-count", text)
	}))
	sc.Step(`^the selection count is blank$`, func() error {
		return w.eventuallyText("#bulk-count is blank", "#bulk-count", func(text string) bool { return text == "" })
	})
	sc.Step(`^select mode is on$`, func() error { return w.selectModeIs(true) })
	sc.Step(`^select mode is off$`, func() error { return w.selectModeIs(false) })
	sc.Step(`^the bulk bar shows `+quoted+`$`, withString(func(text string) error { return w.textContains(bulkBar, text) }))
	sc.Step(`^the tags field holds `+quoted+`$`, withString(w.tagsFieldHolds))
	sc.Step(`^`+quoted+` is ticked$`, withString(func(title string) error { return w.tickedIs(title, true) }))
	sc.Step(`^`+quoted+` is not ticked$`, withString(func(title string) error { return w.tickedIs(title, false) }))
}

func checkbox(title string) string {
	return fmt.Sprintf("ul.list input[aria-label='Select %s']", title)
}

func (w *world) shiftClick(title string) error {
	return w.clickLocator(harness.CSS(checkbox(title)), harness.Placement{Modifiers: chromedp.ModifierShift})
}

// submitBulkTags retries as a whole: the script redraws the panel as ticks
// change, so a field found a moment ago can go stale.
func (w *world) submitBulkTags(action, tags string) error {
	return harness.Eventually("the tags field and the bulk button", func() (bool, error) {
		if err := w.fill(bulkTagsPath, tags); err != nil {
			return false, err
		}
		return true, w.pressBulkButton(action)
	})
}

func (w *world) pressBulkButton(action string) error {
	return w.click(fmt.Sprintf("%s button[name=action][value=%s]", bulkBar, action))
}

func (w *world) tagsFieldHolds(expected string) error {
	field, err := w.element(bulkTagsPath)
	if err != nil {
		return err
	}
	actual, err := field.Value()
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("the tags field holds %q", actual)
	}
	return nil
}

func (w *world) tickedIs(title string, expected bool) error {
	selector := checkbox(title)
	return harness.Eventually(fmt.Sprintf("%s is ticked: %t", title, expected), func() (bool, error) {
		box, err := w.browser.First(harness.CSS(selector))
		if err != nil {
			return false, err
		}
		ticked, err := box.Checked()
		return ticked == expected, err
	})
}

// selectModeIs waits until the Select link is pressed (class "on") or is not.
func (w *world) selectModeIs(on bool) error {
	return harness.Eventually(fmt.Sprintf("select mode is %t", on), func() (bool, error) {
		pressed, err := w.browser.All(harness.CSS(searchTools + " a.on"))
		return (len(pressed) > 0) == on, err
	})
}
