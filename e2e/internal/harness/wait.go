package harness

import (
	"fmt"
	"time"
)

// WaitTimeout is how long a query waits for a condition before giving up.
//
// Only a genuine failure pays it in full, so it is sized for a slow CI runner,
// where 10 s was not enough.
const WaitTimeout = 30 * time.Second

// WaitInterval is how often a query re-checks while waiting.
const WaitInterval = 100 * time.Millisecond

// poll calls probe until it reports done or the timeout passes. An error from
// probe means "not yet", never a failure: during a navigation the page
// evaluates nothing ("Execution context was destroyed"), and a page that
// rebuilds a region leaves stale elements behind; the next poll succeeds. A
// probe that reports done together with an error is not done: its value is a
// zero value read from a page that was not there. The last error is returned
// so a timeout still names its cause.
func poll(timeout time.Duration, probe func() (bool, error)) (bool, error) {
	deadline := time.Now().Add(timeout)
	for {
		done, err := probe()
		if done && err == nil {
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, err
		}
		time.Sleep(WaitInterval)
	}
}

// Eventually polls probe until it reports true.
func Eventually(what string, probe func() (bool, error)) error {
	done, lastErr := poll(WaitTimeout, probe)
	if done {
		return nil
	}
	if lastErr != nil {
		return fmt.Errorf("%s: still not true after %v, last error: %w", what, WaitTimeout, lastErr)
	}
	return fmt.Errorf("%s: still not true after %v", what, WaitTimeout)
}

// EventuallyText polls read until holds accepts the text, and names the last
// text seen when it never does.
func EventuallyText(what string, read func() (string, error), holds func(string) bool) error {
	var last string
	err := Eventually(what, func() (bool, error) {
		text, err := read()
		if err != nil {
			return false, err
		}
		last = text
		return holds(text), nil
	})
	if err != nil {
		return fmt.Errorf("%w (the text was %q)", err, last)
	}
	return nil
}
