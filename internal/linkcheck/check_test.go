package linkcheck

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func loopbackChecker(timeout time.Duration) *Checker {
	return New(Config{Timeout: timeout, Allow: netip.Addr.IsLoopback})
}

func inspect(t *testing.T, checker *Checker, address string) Inspection {
	t.Helper()
	return checker.Inspect(context.Background(), address)
}

func assertVerdict(t *testing.T, got Inspection, status Status, detail string) {
	t.Helper()
	if got.Verdict != (Verdict{status, detail}) {
		t.Fatalf("got %+v, want %s %q", got.Verdict, status, detail)
	}
}

// silent blocks until the client gives up.
func silent(w http.ResponseWriter, r *http.Request) {
	select {
	case <-r.Context().Done():
	case <-time.After(5 * time.Second):
	}
}

func answering(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }
}

func TestStatusesAreSortedIntoTheFourOutcomes(t *testing.T) {
	requested := mustParseURL(t, "https://a.test/page#top")
	verdict := func(status int, final string) Verdict {
		return verdictFor(requested, answer{status: status, finalURL: mustParseURL(t, final)})
	}
	assertEqual(t, verdict(200, "https://a.test/page"), Verdict{StatusOK, ""})
	assertEqual(t, verdict(200, "HTTPS://A.test:443/page#x"), Verdict{StatusOK, ""})
	assertEqual(t, verdict(200, "https://a.test/new"), Verdict{StatusRedirected, "https://a.test/new"})
	for _, broken := range []int{404, 410, 400, 500} {
		assertEqual(t, verdict(broken, "https://a.test/page"), Verdict{StatusBroken, "HTTP " + strconv.Itoa(broken)})
	}
	for _, unknown := range []int{401, 403, 429, 503, 999, 304} {
		if got := verdict(unknown, "https://a.test/page").Status; got != StatusUnknown {
			t.Errorf("%d: got %s", unknown, got)
		}
	}
	challenged := verdictFor(requested, answer{status: 200, finalURL: requested, challenged: true})
	assertEqual(t, challenged, Verdict{StatusUnknown, "Blocked by a bot check"})
}

func TestTwoSpellingsOfOneAddressCompareEqual(t *testing.T) {
	same := [][2]string{
		{"HTTP://Example.COM", "http://example.com/"},
		{"http://example.com:80/", "http://example.com/"},
		{"https://example.com:443/a?b=1#frag", "https://example.com/a?b=1"},
	}
	for _, pair := range same {
		assertEqual(t, normalizeForCompare(mustParseURL(t, pair[0])), normalizeForCompare(mustParseURL(t, pair[1])))
	}
	if normalizeForCompare(mustParseURL(t, "http://example.com:8080/")) == normalizeForCompare(mustParseURL(t, "http://example.com/")) {
		t.Error("a non-default port is another address")
	}
}

func TestHostsAreComparedWithoutWWW(t *testing.T) {
	assertEqual(t, HostOf("https://WWW.Example.com/x"), "example.com")
	assertEqual(t, HostOf("https://sub.example.com/"), "sub.example.com")
	assertEqual(t, HostOf("not a url"), "")
}

func TestAnInternationalHostIsTheSameSiteAsItsPunycodeForm(t *testing.T) {
	assertEqual(t, HostOf("https://B\u00fccher.example/a"), "xn--bcher-kva.example")
	assertEqual(t, HostOf("https://xn--bcher-kva.example/a"), HostOf("https://b\u00fccher.example/b"))
	assertEqual(t, HostOf("http://[::1]:8080/"), "::1")
}

func TestAnAddressThatIsNotAURLIsUnknown(t *testing.T) {
	checker := New(Config{})
	assertVerdict(t, inspect(t, checker, "example.com"), StatusUnknown, "Not a valid address")
	assertVerdict(t, inspect(t, checker, "http://%zz/"), StatusUnknown, "Not a valid address")
}

func TestAnAddressThatIsNotPublicIsNotContacted(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer server.Close()
	checker := New(Config{})

	assertVerdict(t, inspect(t, checker, server.URL), StatusUnknown, "Address is not public")
	assertVerdict(t, inspect(t, checker, "ftp://example.test/"), StatusUnknown, "Address is not public")
	// A name is only judged once it resolves.
	port := mustParseURL(t, server.URL).Port()
	assertVerdict(t, inspect(t, checker, "http://localhost:"+port+"/"), StatusUnknown, "Address is not public")
	assertEqual(t, hits, 0)
}

func TestAWorkingPageIsOkAndOnlyHeadIsSent(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		assertUserAgent(t, r)
	}))
	defer server.Close()

	got := inspect(t, loopbackChecker(0), server.URL+"/page#section")
	assertVerdict(t, got, StatusOK, "")
	assertEqual(t, got.Answered, true)
	assertEqual(t, methods, []string{"HEAD"})
}

func assertUserAgent(t *testing.T, r *http.Request) {
	t.Helper()
	agent := r.Header.Get("User-Agent")
	if !strings.HasPrefix(agent, "Mozilla/5.0 (compatible; bkd-link-checker/") ||
		!strings.HasSuffix(agent, "; +https://github.com/henry40408/bkd)") {
		t.Errorf("unexpected User-Agent %q", agent)
	}
}

func TestGetConfirmsWhatHeadRefused(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	assertVerdict(t, inspect(t, loopbackChecker(0), server.URL), StatusOK, "")
	assertEqual(t, methods, []string{"HEAD", "GET"})
}

func TestAMissingPageIsBroken(t *testing.T) {
	server := httptest.NewServer(answering(http.StatusNotFound))
	defer server.Close()
	assertVerdict(t, inspect(t, loopbackChecker(0), server.URL), StatusBroken, "HTTP 404")
}

func TestARedirectToAnotherPageIsRedirected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "/new", http.StatusMovedPermanently)
		}
	}))
	defer server.Close()
	assertVerdict(t, inspect(t, loopbackChecker(0), server.URL+"/old"), StatusRedirected, server.URL+"/new")
}

func TestTooManyRedirectsIsBroken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/again", http.StatusFound)
	}))
	defer server.Close()
	assertVerdict(t, inspect(t, loopbackChecker(0), server.URL), StatusBroken, "Too many redirects")
}

func TestARedirectIntoAPrivateNetworkIsNotFollowed(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Redirect(w, r, "http://10.0.0.1/secret", http.StatusFound)
	}))
	defer server.Close()

	assertVerdict(t, inspect(t, loopbackChecker(0), server.URL), StatusUnknown, "Address is not public")
	assertEqual(t, hits, 1)
}

func TestARedirectToAnotherSchemeIsNotFollowed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "ftp://example.test/", http.StatusFound)
	}))
	defer server.Close()
	assertVerdict(t, inspect(t, loopbackChecker(0), server.URL), StatusUnknown, "Address is not public")
}

// countdown redirects /n to /n-1 and answers 200 at /0.
func countdown(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
	if n == 0 {
		return
	}
	http.Redirect(w, r, "/"+strconv.Itoa(n-1), http.StatusFound)
}

func TestFiveRedirectsAreFollowedAndSixAreNot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(countdown))
	defer server.Close()
	assertVerdict(t, inspect(t, loopbackChecker(0), server.URL+"/5"), StatusRedirected, server.URL+"/0")
	assertVerdict(t, inspect(t, loopbackChecker(0), server.URL+"/6"), StatusBroken, "Too many redirects")
}

func TestARedirectDetailIsWrittenLikeTheWHATWGParserDoes(t *testing.T) {
	for location, want := range map[string]string{
		"https://Example.COM":          "https://example.com/",
		"https://example.com:443/a?b":  "https://example.com/a?b",
		"http://example.com:80":        "http://example.com/",
		"https://example.com:8443/x#f": "https://example.com:8443/x#f",
		"https://bücher.example/":      "https://xn--bcher-kva.example/",
		"https://[::1]:443/":           "https://[::1]/",
	} {
		address, err := url.Parse(location)
		if err != nil {
			t.Fatal(err)
		}
		if got := serialize(address); got != want {
			t.Errorf("%s: got %s, want %s", location, got, want)
		}
	}
}

func TestARedirectedDetailHasTheNormalizedFinalAddress(t *testing.T) {
	requested := mustParseURL(t, "https://a.test/")
	got := verdictFor(requested, answer{status: 200, finalURL: mustParseURL(t, "https://B.test:443")})
	assertEqual(t, got, Verdict{StatusRedirected, "https://b.test/"})
}

func TestAHomoglyphRedirectTargetIsStoredAsPunycode(t *testing.T) {
	requested := mustParseURL(t, "https://a.test/")
	// The first letter is Cyrillic U+0430; it must not display like Latin "a".
	got := verdictFor(requested, answer{status: 200, finalURL: mustParseURL(t, "https://\u0430pple.test/")})
	assertEqual(t, got, Verdict{StatusRedirected, "https://xn--pple-43d.test/"})
}

func TestARetryAfterBeyondThirtyTwoBitsIsKept(t *testing.T) {
	beyond32Bits := retryAfter("4294967296")
	if beyond32Bits == nil || *beyond32Bits != 4294967296*time.Second {
		t.Fatalf("got %v", beyond32Bits)
	}
	// Past what a Duration can hold the value is capped, not wrapped around.
	for _, header := range []string{"99999999999", "18446744073709551615"} {
		capped := retryAfter(header)
		if capped == nil || *capped != time.Duration(math.MaxInt64/time.Second)*time.Second {
			t.Fatalf("%s: got %v", header, capped)
		}
	}
	if retryAfter("18446744073709551616") != nil || retryAfter("soon") != nil {
		t.Fatal("accepted a bad value")
	}
}

func TestABotCheckIsUnknownWhateverTheStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cf-Mitigated", "Challenge")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	assertVerdict(t, inspect(t, loopbackChecker(0), server.URL), StatusUnknown, "Blocked by a bot check")
}

func TestASiteThatAsksToWaitIsNotAskedTwiceAndSaysFor(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	got := inspect(t, loopbackChecker(0), "http://localhost:"+mustParseURL(t, server.URL).Port()+"/")
	assertVerdict(t, got, StatusUnknown, "HTTP 429")
	wait := 120 * time.Second
	assertEqual(t, got.Backoff, &Backoff{Host: "localhost", RetryAfter: &wait})
	assertEqual(t, methods, []string{"HEAD"})
}

func TestASilentSiteTimesOutWithoutAGetAndIsUnknown(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		silent(w, r)
	}))
	defer server.Close()

	got := inspect(t, loopbackChecker(150*time.Millisecond), server.URL)
	assertVerdict(t, got, StatusUnknown, "Timed out")
	assertEqual(t, got.timedOut, true)
	assertEqual(t, got.Answered, false)
	assertEqual(t, hits.Load(), int32(1))
}

func TestACanceledCallerEndsTheRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(silent))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	started := time.Now()
	loopbackChecker(0).Inspect(ctx, server.URL)
	if time.Since(started) > 2*time.Second {
		t.Fatal("cancel did not end the request")
	}
}

func TestAClosedPortIsBrokenAsNotConnected(t *testing.T) {
	server := httptest.NewServer(answering(http.StatusOK))
	address := server.URL
	server.Close()
	assertVerdict(t, inspect(t, loopbackChecker(0), address), StatusBroken, "Could not connect")
}

func TestOnlyTheCheckerOfTheTestAcceptsLoopback(t *testing.T) {
	server := httptest.NewServer(answering(http.StatusOK))
	defer server.Close()
	for _, checker := range []*Checker{New(Config{}), New(Config{Allow: func(netip.Addr) bool { return false }})} {
		assertVerdict(t, inspect(t, checker, server.URL), StatusUnknown, "Address is not public")
	}
}

func TestADefaultTimeoutApplies(t *testing.T) {
	assertEqual(t, New(Config{}).budget, requestTimeout)
	assertEqual(t, New(Config{Timeout: time.Second}).budget, time.Second)
}
