package e2e

import (
	"fmt"
	"math"

	"github.com/cucumber/godog"

	"github.com/henry40408/bkd/e2e/internal/harness"
)

// placeTolerance is how far, in pixels, a row may move and still count as staying put.
const placeTolerance = 2

// watchPlaceScript notes, at every tap from now on and before the page reacts to
// it, where the watched row and the top row on screen are. A scripted click, which
// the harness falls back to when Chrome delivers no input, has no pointer event.
const watchPlaceScript = `
	const rows = () => [...document.querySelectorAll("li.item")];
	const watched = rows().find((row) => row.querySelector("a.title").textContent.trim() === arguments[0]);
	if (!watched) return false;
	watched.scrollIntoView({ block: "center", behavior: "instant" });
	window.e2ePlace = { id: watched.id, taps: [] };
	if (window.e2eNoteTap) return true;
	const markOf = (row) => ({ id: row.id, top: row.getBoundingClientRect().top });
	window.e2eNoteTap = () => {
		const row = document.getElementById(window.e2ePlace.id);
		if (!row) return;
		const topEdge = document.querySelector(".topbar").getBoundingClientRect().bottom;
		const top = rows().find((each) => each.getBoundingClientRect().top >= topEdge);
		window.e2ePlace.taps.push({ scrolled: window.scrollY, watched: markOf(row), top: top && markOf(top) });
	};
	addEventListener("pointerdown", () => window.e2eNoteTap(), true);
	addEventListener("click", (event) => { if (!event.isTrusted) window.e2eNoteTap(); }, true);
	return true;`

// slowPagesScript holds every request of the page script back, as a phone's
// network does, and notes how far the page on screen is scrolled by then.
const slowPagesScript = `
	const send = window.fetch;
	window.fetch = async (...request) => {
		await new Promise((resolve) => setTimeout(resolve, 500));
		window.e2eScrolledWhileWaiting = window.scrollY;
		return send(...request);
	};`

// dropPageScript does what a phone does to a browser put away: the page is told
// it is hidden, and nothing it does after that is kept. The events of the reload
// that follows cannot be held back, so what they store is thrown away.
const dropPageScript = `
	Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "hidden" });
	document.dispatchEvent(new Event("visibilitychange"));
	Storage.prototype.setItem = () => {};`

// movedScript answers how far a noted row is from where it was at a tap, or null
// while the row or the tap is not there.
const movedScript = `
	const [which, mark, title] = arguments;
	const taps = window.e2ePlace?.taps || [];
	const tap = which === "first" ? taps[0] : taps[taps.length - 1];
	const row = tap?.[mark] && document.getElementById(tap[mark].id);
	if (!row || (title && row.querySelector("a.title").textContent.trim() !== title)) return null;
	return { by: row.getBoundingClientRect().top - tap[mark].top, scrolled: tap.scrolled };`

type movement struct {
	By       float64 `json:"by"`
	Scrolled float64 `json:"scrolled"`
}

// placeCheck names a row noted at a tap: the watched one, by title, or the top one.
type placeCheck struct {
	tap, mark, title string
}

func registerPlaceSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I watch where `+quoted+` is$`, withString(w.watchPlace))
	sc.Step(`^`+quoted+` is where it was at the (first|last) tap$`, func(title, tap string) error {
		return w.rowStayed(placeCheck{tap: tap, mark: "watched", title: unquote(title)})
	})
	sc.Step(`^the top row is where it was at the last tap$`, func() error {
		return w.rowStayed(placeCheck{tap: "last", mark: "top"})
	})
	sc.Step(`^pages are slow to arrive$`, func() error {
		_, err := harness.Exec[any](w.browser, slowPagesScript)
		return err
	})
	sc.Step(`^the page on screen did not scroll while the next one arrived$`, w.waitedAtTheTop)
	sc.Step(`^I read on to `+quoted+`$`, withString(w.readOnTo))
	sc.Step(`^I note how far the page was scrolled at the last tap$`, func() error {
		return w.noteScroll("return window.e2ePlace.taps.at(-1).scrolled")
	})
	sc.Step(`^the page is scrolled as far as noted$`, w.scrolledAsNoted)
	sc.Step(`^I go forward$`, func() error { return w.browser.Forward() })
	sc.Step(`^I go back two pages$`, w.backTwoPages)
	sc.Step(`^the phone drops the page while the browser is away$`, func() error {
		_, err := harness.Exec[any](w.browser, dropPageScript)
		return err
	})
	sc.Step(`^the settings page is shown$`, func() error { _, err := w.element(settingsForm); return err })
	sc.Step(`^the screen is a touch screen$`, func() error { return w.browser.Touch() })
	// These taps leave the page where it is, where the other click steps first
	// bring their control to the centre: that would move a sticky bar's page, and
	// would put a row's action back by itself.
	sc.Step(`^I tap the row action `+quoted+` where it is$`, withString(func(label string) error {
		return w.tapAsShown(fmt.Sprintf("a[aria-label='%s']", label))
	}))
	sc.Step(`^I tap `+quoted+` in the bulk bar where it is$`, withString(func(label string) error {
		return w.tapAsShown(fmt.Sprintf("%s [title^='%s']", bulkBar, label))
	}))
	sc.Step(`^I confirm the deletion where the button is$`, func() error {
		return w.tapAsShown(bulkBar + " button.danger-button")
	})
	sc.Step(`^I fill in the tags `+quoted+` for the selection$`, withString(func(tags string) error {
		return w.fill(bulkTagsPath, tags)
	}))
}

func (w *world) tapAsShown(selector string) error {
	return w.clickLocator(harness.CSS(selector), harness.Placement{AsShown: true})
}

func (w *world) watchPlace(title string) error {
	return harness.Eventually("watching the row "+title, func() (bool, error) {
		return harness.Exec[bool](w.browser, watchPlaceScript, title)
	})
}

// readOnTo scrolls to a row without a tap, and notes how far down that is.
func (w *world) readOnTo(title string) error {
	if err := w.watchPlace(title); err != nil {
		return err
	}
	return w.noteScroll("return window.scrollY")
}

func (w *world) noteScroll(script string) error {
	scrolled, err := harness.Exec[float64](w.browser, script)
	if err != nil {
		return err
	}
	if scrolled <= 0 {
		return fmt.Errorf("the page is at the top, so noting that proves nothing")
	}
	w.scrolledTo = scrolled
	return nil
}

func (w *world) scrolledAsNoted() error {
	what := fmt.Sprintf("the page is scrolled to %.0f", w.scrolledTo)
	return harness.Eventually(what, func() (bool, error) {
		scrolled, err := harness.Exec[float64](w.browser, "return window.scrollY")
		if err != nil {
			return false, err
		}
		if math.Abs(scrolled-w.scrolledTo) > placeTolerance {
			return false, fmt.Errorf("it is at %.0f", scrolled)
		}
		return true, nil
	})
}

// backTwoPages goes past the page in between, as picking from the history menu
// does, and waits for the list to be there.
func (w *world) backTwoPages() error {
	if _, err := harness.Exec[any](w.browser, "setTimeout(() => history.go(-2))"); err != nil {
		return err
	}
	return harness.Eventually("the list is back", func() (bool, error) {
		return harness.Exec[bool](w.browser, `return location.pathname === "/" && document.readyState === "complete" && !!document.querySelector("li.item")`)
	})
}

// waitedAtTheTop follows a step that left a page at its top with pages slow to
// arrive: the browser must not move it before the next one is there.
func (w *world) waitedAtTheTop() error {
	return harness.Eventually("the page stayed at the top while it waited", func() (bool, error) {
		scrolled, err := harness.Exec[*float64](w.browser, "return window.e2eScrolledWhileWaiting ?? null")
		if err != nil || scrolled == nil {
			return false, err
		}
		if *scrolled != 0 {
			return false, fmt.Errorf("it was at %.0f", *scrolled)
		}
		return true, nil
	})
}

// rowStayed waits for the row, which a page swap may still be bringing back. A
// step before it has to assert what the tap did, or this passes before the page
// reacts.
func (w *world) rowStayed(check placeCheck) error {
	what := fmt.Sprintf("the %s row %s stays where it was at the %s tap", check.mark, check.title, check.tap)
	return harness.Eventually(what, func() (bool, error) {
		moved, err := harness.Exec[*movement](w.browser, movedScript, check.tap, check.mark, check.title)
		if err != nil || moved == nil {
			return false, err
		}
		if moved.Scrolled <= 0 {
			return false, fmt.Errorf("the page was at the top at that tap, so this proves nothing")
		}
		if math.Abs(moved.By) > placeTolerance {
			return false, fmt.Errorf("it moved by %.0f pixels", moved.By)
		}
		return true, nil
	})
}
