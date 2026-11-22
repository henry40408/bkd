package linkcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestAUserAgentIsTrimmedAndAnEmptyOneMeansTheDefault(t *testing.T) {
	for given, want := range map[string]string{
		"":                  "",
		"  \t ":             "",
		"Mozilla/5.0 (X11)": "Mozilla/5.0 (X11)",
		"  padded/1.0  ":    "padded/1.0",
		"two  words":        "two  words",
		"!~":                "!~",
	} {
		got, err := ValidateUserAgent(given)
		assertEqual(t, err, nil)
		assertEqual(t, got, want)
	}
}

func TestAUserAgentMayBeTwoHundredCharactersAndNoMore(t *testing.T) {
	atLimit := strings.Repeat("a", MaxUserAgentLength)
	got, err := ValidateUserAgent(atLimit)
	assertEqual(t, err, nil)
	assertEqual(t, got, atLimit)

	// Padding does not count: only what is kept is measured.
	got, err = ValidateUserAgent("  " + atLimit + "  ")
	assertEqual(t, err, nil)
	assertEqual(t, got, atLimit)

	_, err = ValidateUserAgent(atLimit + "a")
	if err == nil || err.Error() != "user agent is too long" {
		t.Fatalf("error = %v, want the too long refusal", err)
	}
}

func TestAUserAgentThatCouldBreakTheHeaderIsRefused(t *testing.T) {
	const refusal = "user agent must use printable ASCII characters only"
	for name, given := range map[string]string{
		"line feed":       "bkd\nX-Injected: 1",
		"carriage return": "bkd\rX-Injected: 1",
		"tab":             "a\tb",
		"NUL":             "a\x00b",
		"unit separator":  "a\x1fb",
		"DEL":             "a\x7fb",
		"non-ASCII":       "bkd é",
		"emoji":           "🤖",
	} {
		_, err := ValidateUserAgent(given)
		if err == nil || err.Error() != refusal {
			t.Errorf("%s: error = %v, want %q", name, err, refusal)
		}
	}
}

func TestTheChosenUserAgentIsSentWithHeadAndWithGet(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.Method] = r.Header.Get("User-Agent")
		mu.Unlock()
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	got := loopbackChecker(0).Inspect(context.Background(), server.URL, "Custom/1.0")
	assertVerdict(t, got, StatusOK, "")
	assertEqual(t, seen, map[string]string{"HEAD": "Custom/1.0", "GET": "Custom/1.0"})
}

func TestWithoutAChosenUserAgentTheDefaultIsSent(t *testing.T) {
	var sent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = r.Header.Get("User-Agent")
	}))
	defer server.Close()

	loopbackChecker(0).Inspect(context.Background(), server.URL, "")
	assertEqual(t, sent, DefaultUserAgent())
	if !strings.Contains(sent, "bkd-link-checker/") {
		t.Errorf("default User-Agent %q does not name the checker", sent)
	}
}

func TestEachTargetSendsItsOwnersUserAgent(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.Header.Get("User-Agent")
		mu.Unlock()
	}))
	defer server.Close()

	collect(t, loopbackChecker(0).Start(context.Background(), []Target{
		{ID: 1, URL: server.URL + "/mine", UserAgent: "Mine/1"},
		{ID: 2, URL: server.URL + "/theirs", UserAgent: "Theirs/2"},
		{ID: 3, URL: server.URL + "/default"},
	}))
	assertEqual(t, seen, map[string]string{
		"/mine": "Mine/1", "/theirs": "Theirs/2", "/default": DefaultUserAgent(),
	})
}
