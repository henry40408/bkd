package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

// ClickAttempts is how many real clicks a control gets before the fallback.
const ClickAttempts = 3

// ClickSettle is how long one click gets to reach the page. A click that
// worked lands promptly, and waiting out the full WaitTimeout would only delay
// the next attempt.
const ClickSettle = 5 * time.Second

// Click clicks the element that locate finds, and confirms that the click
// reached it before returning.
//
// A native click reports success once the event is dispatched. On CI, Chrome
// sometimes accepts a click and delivers no event to the page, and for the
// rest of the session it can stop delivering mouse events altogether. So each
// attempt is checked and repeated. Once every real click is spent,
// probeClickTarget tells that apart from a page fault:
//
//   - The browser dropped the input: the point belongs to the element, the
//     recorder saw no mouse event at all, and a scripted click() reaches the
//     element. The scripted click stands in, with a WARNING, so the scenario
//     keeps testing bkd rather than the browser driver.
//   - Anything else fails. A covered control reports hitIsTarget false.
//
// The check is that the click event reached the element, not that the page
// reacted: what the page should do is for the next step to assert. With page
// scripts switched off the recorder cannot run, so the click is not verified.
func (b *Browser) Click(what string, locate func() (*Element, error), placement Placement) error {
	if b.scriptsOff {
		element, err := locate()
		if err != nil {
			return err
		}
		return element.Click(placement)
	}
	var probes []string
	for attempt := 1; attempt <= ClickAttempts; attempt++ {
		element, err := b.clickOnce(locate, placement)
		if err != nil {
			// A stale element or a page caught mid-navigation: look again.
			logf("e2e: click %d on %s failed: %v", attempt, what, err)
			probes = append(probes, err.Error())
			time.Sleep(WaitInterval)
			continue
		}
		landed, settleErr := b.settledClick()
		if landed {
			if attempt > 1 {
				logf("e2e: %s took %d clicks", what, attempt)
			}
			return nil
		}
		probe, probeErr := element.probeClickTarget(placement)
		reported := describeProbe(probe, probeErr, settleErr)
		logf("e2e: click %d on %s never reached it; url=%s %s", attempt, what, b.currentURL(), reported)
		if probeErr == nil && probe.replacedUnderneath() {
			logf("e2e: %s was replaced by the page while the click arrived; counting it as landed", what)
			return nil
		}
		probes = append(probes, reported)
		if attempt < ClickAttempts {
			continue
		}
		if probeErr == nil && probe.inputNeverArrived() && element.scriptedClickLands() {
			logf("e2e: WARNING: stood in for %s with a scripted click. The browser accepted %d real clicks "+
				"and delivered none of them to the page, so this scenario did not test that the control is "+
				"reachable by a pointer. See %s", what, ClickAttempts, reported)
			return nil
		}
	}
	return fmt.Errorf("clicked %s %d times and the click never reached it (%s)",
		what, ClickAttempts, strings.Join(probes, " | "))
}

// clickOnce finds the element afresh and sends it one real click.
func (b *Browser) clickOnce(locate func() (*Element, error), placement Placement) (*Element, error) {
	element, err := locate()
	if err != nil {
		return nil, err
	}
	if err := b.WaitForLoad(); err != nil {
		return nil, err
	}
	// Best-effort: the recorder only feeds the verdict, and a missing one
	// reads as "gone", which settledClick treats as a navigation.
	_ = element.armRecorder()
	return element, element.Click(placement)
}

func (b *Browser) currentURL() string {
	current, err := b.URL()
	if err != nil {
		return fmt.Sprintf("<%v>", err)
	}
	return current.String()
}

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// settledClick polls whether the click event reached the armed element. A
// page that has navigated has no recorder any more, which counts as reached:
// a click that was not delivered would have left the old page in place.
func (b *Browser) settledClick() (bool, error) {
	return poll(ClickSettle, func() (bool, error) {
		verdict, err := Eval[string](b, `window.__e2e ? (window.__e2e.hit ? 'hit' : 'waiting') : 'gone'`)
		return verdict != "waiting", err
	})
}

func describeProbe(probe clickProbe, probeErr, settleErr error) string {
	reported := "probe=" + probe.raw
	if probeErr != nil {
		reported = fmt.Sprintf("probe failed: %v", probeErr)
	}
	if settleErr != nil {
		reported += fmt.Sprintf(" (last check failed: %v)", settleErr)
	}
	return reported
}

// armRecorder installs, once per document, a capture-phase listener on window
// that records which mouse and pointer events arrived, and points it at this
// element: `hit` turns true when a click lands on it or inside it. `seen`
// counts every event, so "nothing arrived" can be told from "something else
// was hit".
func (e *Element) armRecorder() error {
	_, err := Call[chromedp.Void](e, `function() {
		if (!window.__e2e) {
			const state = { want: null, hit: false, seen: 0, events: [] };
			window.__e2e = state;
			const name = (node) => node && node.tagName ? node.tagName.toLowerCase() : '?';
			for (const type of ['pointerdown', 'mousedown', 'mouseup', 'pointerup', 'click']) {
				window.addEventListener(type, (event) => {
					state.seen++;
					if (state.events.length < 24) {
						state.events.push(type + ':' + name(event.target)
							+ '@' + Math.round(event.clientX) + ',' + Math.round(event.clientY));
					}
					if (type === 'click' && state.want && state.want.contains(event.target)) { state.hit = true; }
				}, true);
			}
		}
		window.__e2e.want = this;
		window.__e2e.hit = false;
		window.__e2e.seen = 0;
		window.__e2e.events.length = 0;
	}`)
	return err
}

// clickProbe is what probeClickTarget read back from the page.
type clickProbe struct {
	// raw is the probe as compact JSON, for log lines.
	raw string
	// Connected: the element is still part of the page.
	Connected bool `json:"connected"`
	// HitIsTarget: the element at the click point is the target or inside it.
	HitIsTarget bool `json:"hitIsTarget"`
	// Seen is how many mouse and pointer events the recorder saw.
	Seen int `json:"seen"`
}

// inputNeverArrived says whether the browser dropped the input, rather than
// the page ignoring it: nothing covers the element, yet no event came.
func (p clickProbe) inputNeverArrived() bool {
	return p.HitIsTarget && p.Seen == 0
}

// replacedUnderneath says whether the click arrived, but the page rebuilt the
// element before the verdict: the click then reached the new copy, which the
// recorder was not watching. Looking again would click whatever the page shows
// next.
func (p clickProbe) replacedUnderneath() bool {
	return !p.Connected && p.Seen > 0
}

// probeClickTarget asks the page what is at the click point and what the
// recorder collected.
func (e *Element) probeClickTarget(placement Placement) (clickProbe, error) {
	raw, err := Call[json.RawMessage](e, `function(dx, dy) {
		if (!this.isConnected) {
			return { connected: false, seen: window.__e2e ? window.__e2e.seen : 0 };
		}
		const r = this.getBoundingClientRect();
		const x = r.left + r.width / 2 + dx, y = r.top + r.height / 2 + dy;
		const hit = document.elementFromPoint(x, y);
		const recorder = window.__e2e;
		return {
			connected: true,
			ready: document.readyState,
			rect: [r.x, r.y, r.width, r.height],
			point: [x, y],
			viewport: [window.innerWidth, window.innerHeight],
			hit: hit === null ? null : hit.tagName.toLowerCase() + (hit.id ? '#' + hit.id : ''),
			hitIsTarget: hit === null ? false : (hit === this || this.contains(hit)),
			hasFocus: document.hasFocus(),
			visibilityState: document.visibilityState,
			seen: recorder ? recorder.seen : 0,
			events: recorder ? recorder.events : null,
		};
	}`, placement.OffsetX, placement.OffsetY)
	if err != nil {
		return clickProbe{}, err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return clickProbe{}, err
	}
	probe := clickProbe{raw: compact.String()}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return clickProbe{}, err
	}
	return probe, nil
}

// scriptedClickLands clicks the element from script and reports whether the
// click reached it. It calls click(), which covers link and form activation
// and every click listener. Only trust it together with inputNeverArrived:
// alone it bypasses hit-testing, and the suite would stop testing that
// controls are reachable.
func (e *Element) scriptedClickLands() bool {
	if _, err := Call[chromedp.Void](e, "function() { this.click(); }"); err != nil {
		return false
	}
	landed, _ := e.browser.settledClick()
	return landed
}

// WaitForAddress waits, for as long as a click gets to settle, until the page
// shows the address. A link the script follows changes the address and the
// page in one step, so a step that comes next does not meet the old page. An
// address that never shows up is not an error: a redirect may have led
// elsewhere, and the next step checks what it needs.
func (b *Browser) WaitForAddress(address string) {
	_, _ = poll(ClickSettle, func() (bool, error) {
		return Exec[bool](b, "return location.href === arguments[0]", address)
	})
}
