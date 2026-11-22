package linkcheck

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func readPage(checker *Checker, address string) (Page, error) {
	return checker.ReadPage(context.Background(), address, "")
}

func assertPageError(t *testing.T, err error, want string) {
	t.Helper()
	var pageErr PageError
	if !errors.As(err, &pageErr) || string(pageErr) != want {
		t.Fatalf("got error %v, want %q", err, want)
	}
}

func htmlPage(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}
}

func TestReadPageReturnsTheBodyOfAWorkingPage(t *testing.T) {
	var request *http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request = r
		htmlPage("<title>Hi</title>")(w, r)
	}))
	defer server.Close()

	page, err := readPage(loopbackChecker(0), server.URL+"/page#section")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, string(page.Body), "<title>Hi</title>")
	assertEqual(t, request.Method, http.MethodGet)
	assertEqual(t, request.URL.Path, "/page")
	assertEqual(t, request.Header.Get("Accept"), "text/html,application/xhtml+xml")
	assertUserAgent(t, request)
}

func TestReadPageSendsTheGivenUserAgentOrTheDefault(t *testing.T) {
	cases := map[string]struct{ given, want string }{
		"chosen":  {"Custom/1.0", "Custom/1.0"},
		"default": {"", DefaultUserAgent()},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var sent string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sent = r.Header.Get("User-Agent")
				htmlPage("<title>Hi</title>")(w, r)
			}))
			defer server.Close()

			_, err := loopbackChecker(0).ReadPage(context.Background(), server.URL, tc.given)
			if err != nil {
				t.Fatal(err)
			}
			assertEqual(t, sent, tc.want)
		})
	}
}

func TestReadPageKeepsOnlyTheFirstBytesOfAHugePage(t *testing.T) {
	cases := map[string]struct{ sent, kept int }{
		"under the limit": {maxPageBytes - 1, maxPageBytes - 1},
		"at the limit":    {maxPageBytes, maxPageBytes},
		"over the limit":  {maxPageBytes + 1, maxPageBytes},
		"far over":        {3 * maxPageBytes, maxPageBytes},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(htmlPage(strings.Repeat("x", c.sent)))
			defer server.Close()
			page, err := readPage(loopbackChecker(0), server.URL)
			if err != nil {
				t.Fatal(err)
			}
			assertEqual(t, len(page.Body), c.kept)
		})
	}
}

func TestReadPageKeepsTheBytesInOrder(t *testing.T) {
	body := bytes.Repeat([]byte("0123456789"), maxPageBytes/10+1)
	server := httptest.NewServer(htmlPage(string(body)))
	defer server.Close()
	page, err := readPage(loopbackChecker(0), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(page.Body, body[:maxPageBytes]) {
		t.Fatal("the kept bytes are not the first bytes of the page")
	}
}

func TestReadPageFollowsARedirectToAnotherPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "/new", http.StatusMovedPermanently)
			return
		}
		htmlPage("<title>New</title>")(w, r)
	}))
	defer server.Close()
	page, err := readPage(loopbackChecker(0), server.URL+"/old")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, string(page.Body), "<title>New</title>")
}

func TestReadPageRefusesWhatIsNotAnHTMLPage(t *testing.T) {
	cases := map[string]struct {
		contentType string
		ok          bool
	}{
		"html":               {"text/html", true},
		"html with charset":  {"Text/HTML; charset=utf-8", true},
		"xhtml":              {"application/xhtml+xml", true},
		"no type":            {"", true},
		"json":               {"application/json", false},
		"pdf":                {"application/pdf", false},
		"image":              {"image/png", false},
		"plain text":         {"text/plain", false},
		"not a media type":   {"text/html; charset", false},
		"text/htmlx":         {"text/htmlx", false},
		"prefix of an other": {"xtext/html", false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header()["Content-Type"] = nil // stop net/http from sniffing a type
				if c.contentType != "" {
					w.Header().Set("Content-Type", c.contentType)
				}
				_, _ = w.Write([]byte("<title>x</title>"))
			}))
			defer server.Close()
			_, err := readPage(loopbackChecker(0), server.URL)
			if c.ok && err != nil {
				t.Fatal(err)
			}
			if !c.ok {
				assertPageError(t, err, "Not an HTML page")
			}
		})
	}
}

func TestReadPageRefusesAnUnsuccessfulAnswer(t *testing.T) {
	// 304 is a redirect class status without a Location, which net/http hands back as it is.
	for _, status := range []int{300, 304, 404, 410, 429, 500, 503} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(status)
		}))
		_, err := readPage(loopbackChecker(0), server.URL)
		server.Close()
		assertPageError(t, err, "HTTP "+strconv.Itoa(status))
	}
}

func TestReadPageAcceptsEverySuccessStatus(t *testing.T) {
	for _, status := range []int{200, 201, 299} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(status)
			_, _ = w.Write([]byte("<title>x</title>"))
		}))
		page, err := readPage(loopbackChecker(0), server.URL)
		server.Close()
		if err != nil {
			t.Fatalf("%d: %v", status, err)
		}
		assertEqual(t, string(page.Body), "<title>x</title>")
	}
}

func TestReadPageSaysAboutABotCheckWhateverTheStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cf-Mitigated", "challenge")
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<title>Just a moment</title>"))
	}))
	defer server.Close()
	_, err := readPage(loopbackChecker(0), server.URL)
	assertPageError(t, err, "Blocked by a bot check")
}

func TestReadPageDoesNotContactAnAddressThatIsNotPublic(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer server.Close()
	checker := New(Config{})

	_, err := readPage(checker, server.URL)
	assertPageError(t, err, "Address is not public")
	_, err = readPage(checker, "ftp://example.test/")
	assertPageError(t, err, "Address is not public")
	// A name is only judged once it resolves.
	_, err = readPage(checker, "http://localhost:"+mustParseURL(t, server.URL).Port()+"/")
	assertPageError(t, err, "Address is not public")
	assertEqual(t, hits.Load(), int32(0))
}

func TestReadPageDoesNotFollowARedirectIntoAPrivateNetwork(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://10.0.0.1/secret", http.StatusFound)
	}))
	defer server.Close()
	_, err := readPage(loopbackChecker(0), server.URL)
	assertPageError(t, err, "Address is not public")
}

func TestReadPageSaysWhenTheAddressIsNotAURL(t *testing.T) {
	checker := loopbackChecker(0)
	for _, address := range []string{"", "example.test/page", "http://[::1", "http://a b/"} {
		_, err := readPage(checker, address)
		assertPageError(t, err, "Not a valid address")
	}
}

func TestReadPageGivesUpOnTooManyRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/again", http.StatusFound)
	}))
	defer server.Close()
	_, err := readPage(loopbackChecker(0), server.URL)
	assertPageError(t, err, "Too many redirects")
}

func TestReadPageTimesOutOnASilentSite(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(silent))
	defer server.Close()
	_, err := readPage(loopbackChecker(150*time.Millisecond), server.URL)
	assertPageError(t, err, "Timed out")
}

func TestReadPageTimesOutOnABodyThatNeverEnds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<title>Slow</title>"))
		w.(http.Flusher).Flush()
		silent(w, r)
	}))
	defer server.Close()
	_, err := readPage(loopbackChecker(150*time.Millisecond), server.URL)
	assertPageError(t, err, "Timed out")
}

func TestReadPageSaysWhenItCouldNotConnect(t *testing.T) {
	server := httptest.NewServer(htmlPage("x"))
	address := server.URL
	server.Close()
	_, err := readPage(loopbackChecker(0), address)
	assertPageError(t, err, "Could not connect")
}

func TestReadPageSaysWhenNoSlotIsFreeBeforeTheCallerGivesUp(t *testing.T) {
	checker := loopbackChecker(0)
	for range maxRequestsInFlight {
		checker.inFlight <- struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := checker.ReadPage(ctx, "http://example.test/", "")
	assertPageError(t, err, "Could not connect")
}

func TestReadPageReleasesItsSlot(t *testing.T) {
	server := httptest.NewServer(htmlPage("x"))
	defer server.Close()
	checker := loopbackChecker(0)
	for range 3 {
		if _, err := readPage(checker, server.URL); err != nil {
			t.Fatal(err)
		}
	}
	assertEqual(t, len(checker.inFlight), 0)
	_, _ = readPage(checker, "http://127.0.0.1:1/")
	assertEqual(t, len(checker.inFlight), 0)
}

func TestOnlyTheCheckerOfTheTestReadsLoopback(t *testing.T) {
	server := httptest.NewServer(htmlPage("x"))
	defer server.Close()
	for _, checker := range []*Checker{New(Config{}), New(Config{Allow: func(netip.Addr) bool { return false }})} {
		_, err := readPage(checker, server.URL)
		assertPageError(t, err, "Address is not public")
	}
}

func TestAPageErrorIsItsMessage(t *testing.T) {
	assertEqual(t, PageError("Timed out").Error(), "Timed out")
}

func TestInspectSaysNoSlotWhenTheCallerGivesUpBeforeOneIsFree(t *testing.T) {
	checker := loopbackChecker(0)
	for range maxRequestsInFlight {
		checker.inFlight <- struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	assertVerdict(t, checker.Inspect(ctx, "http://example.test/", ""), StatusUnknown, "Could not connect")
}
