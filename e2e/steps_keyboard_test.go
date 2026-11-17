package e2e

import (
	"fmt"
	"time"

	"github.com/chromedp/chromedp/kb"
	"github.com/cucumber/godog"

	"github.com/henry40408/bkd/e2e/internal/harness"
)

func registerKeyboardSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I press Enter$`, func() error { return w.browser.KeyEvent(kb.Enter) })
	sc.Step(`^I press Escape$`, func() error { return w.browser.KeyEvent(kb.Escape) })
	sc.Step(`^the focus is on `+quoted+`$`, withString(w.focusIsOn))
}

// focusIsOn awaits the focus, because it moves after the next paint. It then
// reads it again after a pause: the browser drops focus from a control that
// has just been hidden only at its next frame, and that must not pass as
// "still focused".
func (w *world) focusIsOn(selector string) error {
	err := harness.Eventually("the focus is on "+selector, func() (bool, error) {
		return w.focusMatches(selector)
	})
	if err != nil {
		return fmt.Errorf("%w; %s", err, w.describeFocus())
	}
	time.Sleep(300 * time.Millisecond)
	matches, err := w.focusMatches(selector)
	if err != nil {
		return err
	}
	if !matches {
		return fmt.Errorf("the focus left %s; %s", selector, w.describeFocus())
	}
	return nil
}

func (w *world) focusMatches(selector string) (bool, error) {
	return harness.Exec[bool](w.browser, "return document.activeElement.matches(arguments[0])", selector)
}

func (w *world) describeFocus() string {
	held, err := harness.Exec[string](w.browser, "return document.activeElement.outerHTML.slice(0, 200)")
	if err != nil {
		return "the focus could not be read: " + err.Error()
	}
	return fmt.Sprintf("the focus is on %q", held)
}
