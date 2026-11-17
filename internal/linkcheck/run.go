package linkcheck

import (
	"context"
	"sync"
	"time"
)

const (
	// maxHostsPerCheck is per check, so one user cannot queue ahead of
	// everyone else for the request slots.
	maxHostsPerCheck = 4
	// timeoutsBeforeGivingUp: two timeouts in a row mean the site is not
	// answering. Asking about its other bookmarks would only make the check
	// wait 10 seconds for each of them.
	timeoutsBeforeGivingUp = 2
)

// Target is a bookmark to check.
type Target struct {
	ID  int64
	URL string
}

// Checked is the result for one target.
type Checked struct {
	Target  Target
	Verdict Verdict
	// Backoff is set when a site should be left alone for a while.
	Backoff *Backoff
	// Answered: a site replied. A timeout or a failed connection is not an answer.
	Answered bool
}

// SkipReason says why a target was not asked.
type SkipReason string

const (
	SkipSlowDown     SkipReason = "slow_down"
	SkipNotAnswering SkipReason = "not_answering"
)

// Skipped is a target that was not asked, because its site asked to be left
// alone or stopped answering.
type Skipped struct {
	Target Target
	Reason SkipReason
	Host   string
}

// Event is what became of one target. Exactly one field is set.
type Event struct {
	Checked *Checked
	Skipped *Skipped
}

// RunningCheck is a check in progress.
type RunningCheck struct {
	// Events delivers a result as each target finishes, and is closed once
	// every target has been dealt with or the check was cancelled.
	Events <-chan Event
	cancel context.CancelFunc
}

// Cancel stops the requests that have not finished, so a client that goes
// away does not leave the server working for nobody.
func (r *RunningCheck) Cancel() { r.cancel() }

// Start checks every target. Targets on one host go one after another, so a
// batch of bookmarks from one site is not a burst of requests to it. A host
// that asks to be left alone (429, 503) or does not answer twice in a row is
// not asked again in this check; its other targets come back as skipped.
func (c *Checker) Start(ctx context.Context, targets []Target) *RunningCheck {
	return c.StartPaced(ctx, targets, 0)
}

// StartPaced is like Start, and waits pause between two requests to one host.
func (c *Checker) StartPaced(ctx context.Context, targets []Target, pause time.Duration) *RunningCheck {
	ctx, cancel := context.WithCancel(ctx)
	// Every target gets exactly one event, so a sender never blocks and no
	// goroutine waits for a reader that left.
	events := make(chan Event, len(targets))
	run := hostRun{checker: c, events: events, pause: pause}
	slots := make(chan struct{}, maxHostsPerCheck)
	var workers sync.WaitGroup
	for host, group := range groupByHost(targets) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				return
			}
			run.checkHost(ctx, host, group)
		}()
	}
	go func() {
		workers.Wait()
		cancel()
		close(events)
	}()
	return &RunningCheck{Events: events, cancel: cancel}
}

func groupByHost(targets []Target) map[string][]Target {
	groups := make(map[string][]Target)
	for _, target := range targets {
		host := HostOf(target.URL)
		groups[host] = append(groups[host], target)
	}
	return groups
}

// hostRun is what the workers of one check share.
type hostRun struct {
	checker *Checker
	events  chan<- Event
	pause   time.Duration
}

// checkHost asks one host's targets, one after another, until the host asks
// to be left alone or stops answering.
func (r hostRun) checkHost(ctx context.Context, host string, group []Target) {
	timeoutsInARow := 0
	for i, target := range group {
		if i > 0 && !sleep(ctx, r.pause) {
			return
		}
		inspection := r.checker.Inspect(ctx, target.URL)
		if ctx.Err() != nil {
			return
		}
		if inspection.timedOut {
			timeoutsInARow++
		} else {
			timeoutsInARow = 0
		}
		givesUp := timeoutsInARow >= timeoutsBeforeGivingUp
		backoff := inspection.Backoff
		// A redirect can end at another site; only an answer from this one stops this group.
		askedToWait := backoff != nil && backoff.Host == host
		if givesUp && backoff == nil {
			backoff = &Backoff{Host: host}
		}
		r.events <- Event{Checked: &Checked{
			Target: target, Verdict: inspection.Verdict, Backoff: backoff, Answered: inspection.Answered,
		}}
		if reason, stop := skipReason(askedToWait, givesUp); stop {
			r.skipRest(host, group[i+1:], reason)
			return
		}
	}
}

func skipReason(askedToWait, givesUp bool) (SkipReason, bool) {
	switch {
	case askedToWait:
		return SkipSlowDown, true
	case givesUp:
		return SkipNotAnswering, true
	}
	return "", false
}

func (r hostRun) skipRest(host string, rest []Target, reason SkipReason) {
	for _, target := range rest {
		r.events <- Event{Skipped: &Skipped{Target: target, Reason: reason, Host: host}}
	}
}

// sleep waits, and reports false when the check was cancelled meanwhile.
func sleep(ctx context.Context, wait time.Duration) bool {
	if wait <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// ActiveChecks tracks the users who have a check running. A user gets one at
// a time, so a few accounts cannot take every request slot from everyone else.
// The zero value is ready to use.
type ActiveChecks struct {
	mu    sync.Mutex
	users map[int64]struct{}
}

// Start claims the slot of a user. It returns false when the user already has
// a check running; otherwise the returned function frees the slot.
func (a *ActiveChecks) Start(userID int64) (release func(), ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, running := a.users[userID]; running {
		return nil, false
	}
	if a.users == nil {
		a.users = make(map[int64]struct{})
	}
	a.users[userID] = struct{}{}
	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		delete(a.users, userID)
	}, true
}
