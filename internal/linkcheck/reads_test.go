package linkcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/henry40408/bkd/internal/sitemeta"
)

func readAll(t *testing.T, checker *Checker, targets []Target, pause time.Duration) []Event {
	t.Helper()
	return collect(t, checker.StartReads(context.Background(), targets, pause))
}

// readOne reads a single address and returns what became of it.
func readOne(t *testing.T, checker *Checker, address string) Checked {
	t.Helper()
	events := readAll(t, checker, []Target{{ID: 1, URL: address}}, 0)
	assertEqual(t, len(events), 1)
	if events[0].Checked == nil {
		t.Fatalf("the target was skipped: %+v", events[0].Skipped)
	}
	return *events[0].Checked
}

func eventsByID(t *testing.T, events []Event) map[int64]Event {
	t.Helper()
	byID := map[int64]Event{}
	for _, event := range events {
		id := int64(0)
		if event.Checked != nil {
			id = event.Checked.Target.ID
		} else {
			id = event.Skipped.Target.ID
		}
		if _, again := byID[id]; again {
			t.Fatalf("target %d got two events", id)
		}
		byID[id] = event
	}
	return byID
}

func TestStartReadsFillsTheMetaOfEveryPageRead(t *testing.T) {
	pages := map[string]string{
		"/a": `<head><meta property="og:title" content="Og Title"><meta property="og:description" content="Og words">` +
			`<title>Plain Title</title><meta name="description" content="Plain words"></head>`,
		"/b": `<title>Only Title</title><meta name="description" content="Plain words">`,
		"/c": `<html><body>nothing to say</body></html>`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		htmlPage(pages[r.URL.Path])(w, r)
	}))
	defer server.Close()

	events := readAll(t, loopbackChecker(0), targetsOn(server.URL, 1, 2, 3), 0)
	assertEqual(t, len(events), 3)
	byID := eventsByID(t, events)
	want := map[int64]*sitemeta.Meta{
		1: {Title: "Og Title", Description: "Og words"},
		2: {Title: "Only Title", Description: "Plain words"},
		3: {},
	}
	for id, meta := range want {
		checked := byID[id].Checked
		assertEqual(t, checked.Verdict, Verdict{StatusOK, ""})
		assertEqual(t, checked.Meta, meta)
		assertEqual(t, checked.Backoff, (*Backoff)(nil))
		assertEqual(t, checked.Answered, true)
	}
}

func TestStartReadsSendsTheUserAgentOfEachTarget(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.Header.Get("User-Agent")
		mu.Unlock()
		htmlPage("<title>Hi</title>")(w, r)
	}))
	defer server.Close()
	targets := targetsOn(server.URL, 1, 2)
	targets[0].UserAgent = "Custom/1.0"

	readAll(t, loopbackChecker(0), targets, 0)
	assertEqual(t, seen, map[string]string{"/a": "Custom/1.0", "/b": DefaultUserAgent()})
}

func TestStartReadsCutsAHugeTitleAndDescriptionBeforeTheyAreHeld(t *testing.T) {
	huge := strings.Repeat("é", maxReadMetaChars+50)
	exact := strings.Repeat("é", maxReadMetaChars)
	server := httptest.NewServer(htmlPage(
		`<head><title>` + huge + `</title><meta name="description" content="` + exact + `">`))
	defer server.Close()

	read := readOne(t, loopbackChecker(0), server.URL)
	assertEqual(t, read.Meta, &sitemeta.Meta{Title: exact, Description: exact})
}

func TestStartReadsParsesOnlyTheFirstBytesOfAPage(t *testing.T) {
	padding := strings.Repeat(" ", maxPageBytes)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/early" {
			htmlPage("<head><title>Early</title>"+padding)(w, r)
			return
		}
		htmlPage("<head>"+padding+"<title>Late</title>")(w, r)
	}))
	defer server.Close()

	early := readOne(t, loopbackChecker(0), server.URL+"/early")
	assertEqual(t, early.Meta, &sitemeta.Meta{Title: "Early"})
	late := readOne(t, loopbackChecker(0), server.URL+"/late")
	assertEqual(t, late.Verdict, Verdict{StatusOK, ""})
	assertEqual(t, late.Meta, &sitemeta.Meta{})
}

func TestStartReadsSaysWhyAPageWasNotRead(t *testing.T) {
	cases := map[string]struct {
		handler http.HandlerFunc
		detail  string
	}{
		"not found": {answering(http.StatusNotFound), "HTTP 404"},
		"not html": {func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"title":"x"}`))
		}, "Not an HTML page"},
		"bot check": {func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cf-Mitigated", "challenge")
			htmlPage("<title>Just a moment</title>")(w, r)
		}, "Blocked by a bot check"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(c.handler)
			defer server.Close()
			checked := readOne(t, loopbackChecker(0), server.URL)
			assertEqual(t, checked.Verdict, Verdict{StatusUnknown, c.detail})
			assertEqual(t, checked.Meta, (*sitemeta.Meta)(nil))
			assertEqual(t, checked.Backoff, (*Backoff)(nil))
			assertEqual(t, checked.Answered, true)
		})
	}
}

func TestStartReadsIsNotAnsweredWhenNoSiteReplied(t *testing.T) {
	var hits atomic.Int32
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer live.Close()
	closed := httptest.NewServer(htmlPage("x"))
	closedURL := closed.URL
	closed.Close()

	notPublic := readOne(t, New(Config{}), live.URL)
	assertEqual(t, notPublic.Verdict, Verdict{StatusUnknown, "Address is not public"})
	assertEqual(t, hits.Load(), int32(0))
	unreachable := readOne(t, loopbackChecker(0), closedURL)
	assertEqual(t, unreachable.Verdict.Detail, "Could not connect")
	for _, checked := range []Checked{notPublic, unreachable} {
		assertEqual(t, checked.Meta, (*sitemeta.Meta)(nil))
		assertEqual(t, checked.Backoff, (*Backoff)(nil))
		assertEqual(t, checked.Answered, false)
	}
}

func TestStartReadsSkipsTheRestOfASiteThatAsksToWait(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.Host, "localhost") {
					htmlPage("<title>Fine</title>")(w, r)
					return
				}
				hits.Add(1)
				w.Header().Set("Retry-After", "7")
				w.WriteHeader(status)
			}))
			defer server.Close()
			other := Target{ID: 4, URL: "http://localhost:" + mustParseURL(t, server.URL).Port() + "/d"}
			targets := append(targetsOn(server.URL, 1, 2, 3), other)

			events := readAll(t, loopbackChecker(0), targets, 0)
			byID := eventsByID(t, events)
			assertEqual(t, len(events), 4)
			assertEqual(t, hits.Load(), int32(1))
			wait := 7 * time.Second
			asked := byID[1].Checked
			assertEqual(t, asked.Verdict, Verdict{StatusUnknown, "HTTP " + strconv.Itoa(status)})
			assertEqual(t, asked.Backoff, &Backoff{Host: "127.0.0.1", RetryAfter: &wait})
			assertEqual(t, asked.Meta, (*sitemeta.Meta)(nil))
			assertEqual(t, asked.Answered, true)
			for _, id := range []int64{2, 3} {
				assertEqual(t, byID[id].Skipped, &Skipped{Target: targets[id-1], Reason: SkipSlowDown, Host: "127.0.0.1"})
			}
			assertEqual(t, byID[4].Checked.Meta, &sitemeta.Meta{Title: "Fine"})
		})
	}
}

func TestStartReadsBacksOffFromTheSiteThatAnsweredNotTheOneAsked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Host, "localhost") {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if r.URL.Path == "/a" {
			http.Redirect(w, r, "http://"+strings.Replace(r.Host, "127.0.0.1", "localhost", 1)+"/limited", http.StatusFound)
			return
		}
		htmlPage("<title>Fine</title>")(w, r)
	}))
	defer server.Close()

	events := readAll(t, loopbackChecker(0), targetsOn(server.URL, 1, 2), 0)
	assertEqual(t, len(events), 2)
	assertEqual(t, events[0].Checked.Verdict, Verdict{StatusUnknown, "HTTP 429"})
	assertEqual(t, events[0].Checked.Backoff, &Backoff{Host: "localhost"})
	assertEqual(t, events[1].Checked.Meta, &sitemeta.Meta{Title: "Fine"})
}

func TestStartReadsGivesUpOnASiteThatTimesOutTwiceInARow(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		silent(w, r)
	}))
	defer server.Close()

	events := readAll(t, loopbackChecker(80*time.Millisecond), targetsOn(server.URL, 1, 2, 3, 4), 0)
	assertEqual(t, hits.Load(), int32(2))
	assertEqual(t, len(events), 4)
	assertEqual(t, events[0].Checked.Verdict, Verdict{StatusUnknown, "Timed out"})
	assertEqual(t, events[0].Checked.Backoff, (*Backoff)(nil))
	assertEqual(t, events[0].Checked.Answered, false)
	assertEqual(t, events[1].Checked.Backoff, &Backoff{Host: "127.0.0.1"})
	assertEqual(t, events[2].Skipped.Reason, SkipNotAnswering)
	assertEqual(t, events[3].Skipped.Reason, SkipNotAnswering)
}

func TestStartReadsCountsABodyThatStallsAsAnAnsweredTimeout(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<title>Slow</title>"))
		w.(http.Flusher).Flush()
		silent(w, r)
	}))
	defer server.Close()

	events := readAll(t, loopbackChecker(80*time.Millisecond), targetsOn(server.URL, 1, 2, 3), 0)
	assertEqual(t, hits.Load(), int32(2))
	assertEqual(t, len(events), 3)
	assertEqual(t, events[0].Checked.Verdict, Verdict{StatusUnknown, "Timed out"})
	assertEqual(t, events[0].Checked.Meta, (*sitemeta.Meta)(nil))
	assertEqual(t, events[0].Checked.Answered, true)
	assertEqual(t, events[1].Checked.Backoff, &Backoff{Host: "127.0.0.1"})
	assertEqual(t, events[2].Skipped.Reason, SkipNotAnswering)
}

func TestStartReadsWaitsBetweenTwoPagesOfOneHost(t *testing.T) {
	var mu sync.Mutex
	var times []time.Time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		times = append(times, time.Now())
		mu.Unlock()
		htmlPage("<title>Hi</title>")(w, r)
	}))
	defer server.Close()

	pause := 60 * time.Millisecond
	readAll(t, loopbackChecker(0), targetsOn(server.URL, 1, 2, 3), pause)
	assertEqual(t, len(times), 3)
	for i := 1; i < len(times); i++ {
		if gap := times[i].Sub(times[i-1]); gap < pause {
			t.Errorf("page %d was asked %v after the one before", i, gap)
		}
	}
}
