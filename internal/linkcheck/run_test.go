package linkcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func collect(t *testing.T, running *RunningCheck) []Event {
	t.Helper()
	var events []Event
	deadline := time.After(10 * time.Second)
	for {
		select {
		case event, open := <-running.Events:
			if !open {
				return events
			}
			events = append(events, event)
		case <-deadline:
			t.Fatal("the check did not finish")
		}
	}
}

func targetsOn(base string, ids ...int64) []Target {
	targets := make([]Target, len(ids))
	for i, id := range ids {
		targets[i] = Target{ID: id, URL: base + "/" + string(rune('a'+i))}
	}
	return targets
}

func TestEveryTargetGetsOneEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/b" {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	events := collect(t, loopbackChecker(0).Start(context.Background(), targetsOn(server.URL, 1, 2, 3)))
	verdicts := map[int64]Verdict{}
	for _, event := range events {
		verdicts[event.Checked.Target.ID] = event.Checked.Verdict
	}
	assertEqual(t, verdicts, map[int64]Verdict{
		1: {StatusOK, ""}, 2: {StatusBroken, "HTTP 404"}, 3: {StatusOK, ""},
	})
}

func TestNoTargetsEndsAtOnce(t *testing.T) {
	assertEqual(t, len(collect(t, New(Config{}).Start(context.Background(), nil))), 0)
}

func TestOneHostIsAskedOneRequestAtATimeWithAPause(t *testing.T) {
	var mu sync.Mutex
	var active, busiest int
	var times []time.Time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		active++
		busiest = max(busiest, active)
		times = append(times, time.Now())
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		mu.Lock()
		active--
		mu.Unlock()
	}))
	defer server.Close()

	pause := 60 * time.Millisecond
	collect(t, loopbackChecker(0).StartPaced(context.Background(), targetsOn(server.URL, 1, 2, 3), pause))
	assertEqual(t, busiest, 1)
	for i := 1; i < len(times); i++ {
		if gap := times[i].Sub(times[i-1]); gap < pause {
			t.Errorf("request %d came %v after the one before", i, gap)
		}
	}
}

func TestASiteThatAsksToWaitSkipsItsOtherTargets(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	events := collect(t, loopbackChecker(0).Start(context.Background(), targetsOn(server.URL, 1, 2, 3)))
	assertEqual(t, hits, 1)
	assertEqual(t, len(events), 3)
	assertEqual(t, events[0].Checked.Backoff.Host, "127.0.0.1")
	for _, event := range events[1:] {
		assertEqual(t, event.Skipped.Reason, SkipSlowDown)
		assertEqual(t, event.Skipped.Host, "127.0.0.1")
	}
}

func TestASiteThatTimesOutTwiceInARowIsGivenUp(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		silent(w, r)
	}))
	defer server.Close()

	events := collect(t, loopbackChecker(80*time.Millisecond).Start(context.Background(), targetsOn(server.URL, 1, 2, 3, 4)))
	assertEqual(t, hits.Load(), int32(2))
	assertEqual(t, len(events), 4)
	assertEqual(t, events[0].Checked.Backoff, (*Backoff)(nil))
	assertEqual(t, events[1].Checked.Backoff, &Backoff{Host: "127.0.0.1"})
	assertEqual(t, events[2].Skipped.Reason, SkipNotAnswering)
	assertEqual(t, events[3].Skipped.Reason, SkipNotAnswering)
}

func TestAnAnswerBetweenTimeoutsResetsTheCount(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) != 2 {
			silent(w, r)
		}
	}))
	defer server.Close()

	events := collect(t, loopbackChecker(80*time.Millisecond).Start(context.Background(), targetsOn(server.URL, 1, 2, 3)))
	for _, event := range events {
		if event.Skipped != nil {
			t.Fatalf("nothing should be skipped: %+v", event.Skipped)
		}
	}
}

func TestHostsAreCheckedAtTheSameTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
	}))
	defer server.Close()
	port := mustParseURL(t, server.URL).Port()
	targets := []Target{
		{ID: 1, URL: "http://127.0.0.1:" + port + "/"},
		{ID: 2, URL: "http://localhost:" + port + "/"},
	}

	started := time.Now()
	assertEqual(t, len(collect(t, loopbackChecker(0).Start(context.Background(), targets))), 2)
	if took := time.Since(started); took > 280*time.Millisecond {
		t.Fatalf("hosts waited for each other: %v", took)
	}
}

func TestCancelEndsTheCheckAndLeavesNoGoroutines(t *testing.T) {
	before := runtime.NumGoroutine()
	server := httptest.NewServer(http.HandlerFunc(silent))
	port := mustParseURL(t, server.URL).Port()
	var targets []Target
	for id, host := range []string{"127.0.0.1", "localhost"} {
		targets = append(targets, Target{ID: int64(id), URL: "http://" + host + ":" + port + "/"})
	}

	running := loopbackChecker(0).Start(context.Background(), targets)
	time.Sleep(50 * time.Millisecond)
	running.Cancel()
	assertEqual(t, len(collect(t, running)), 0)

	server.Close()
	http.DefaultTransport.(*http.Transport).CloseIdleConnections()
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutines: %d before, %d after", before, after)
	}
}

func TestAUserHasOneCheckAtATime(t *testing.T) {
	var active ActiveChecks
	release, ok := active.Start(1)
	assertEqual(t, ok, true)
	if _, again := active.Start(1); again {
		t.Fatal("a second check for one user must not start")
	}
	if _, other := active.Start(2); !other {
		t.Fatal("other users are not affected")
	}
	release()
	if _, afterwards := active.Start(1); !afterwards {
		t.Fatal("finishing frees the slot")
	}
}

func TestABatchAsksForBetweenOneAndTheMaximumDistinctIDs(t *testing.T) {
	validate := func(ids []int64) (ValidCheck, error) { return CheckInput{IDs: ids}.Validate() }
	if _, err := validate(nil); err == nil || err.Error() != "ids must not be empty" {
		t.Errorf("empty: %v", err)
	}
	tooMany := make([]int64, MaxCheckIDs+1)
	for i := range tooMany {
		tooMany[i] = int64(i)
	}
	if _, err := validate(tooMany); err == nil || err.Error() != "too many ids" {
		t.Errorf("too many: %v", err)
	}
	valid, err := validate(tooMany[:MaxCheckIDs])
	assertEqual(t, err, nil)
	assertEqual(t, len(valid.IDs), MaxCheckIDs)
	valid, _ = validate([]int64{3, 3, 4})
	assertEqual(t, valid.IDs, []int64{3, 4})
}

// panickingOn panics for one path and lets every other request through.
type panickingOn struct {
	path  string
	inner http.RoundTripper
}

func (p panickingOn) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path == p.path {
		panic("boom")
	}
	return p.inner.RoundTrip(r)
}

func TestAPanicLeavesOneEventPerTargetAndTheProcessRunning(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer healthy.Close()
	crashing := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer crashing.Close()
	checker := loopbackChecker(0)
	checker.client.Transport = panickingOn{path: "/b", inner: checker.client.Transport}
	// Hosts are told apart by name, not port, so the healthy site is reached as "localhost".
	healthyURL := strings.Replace(healthy.URL, "127.0.0.1", "localhost", 1)
	targets := append(targetsOn(crashing.URL, 1, 2, 3), targetsOn(healthyURL, 4)...)

	events := collect(t, checker.Start(context.Background(), targets))
	verdicts := map[int64]Verdict{}
	for _, event := range events {
		verdicts[event.Checked.Target.ID] = event.Checked.Verdict
	}
	assertEqual(t, len(events), 4)
	assertEqual(t, verdicts, map[int64]Verdict{
		1: {StatusOK, ""}, 2: {StatusUnknown, detailCheckFailed}, 3: {StatusUnknown, detailCheckFailed},
		4: {StatusOK, ""},
	})
}
