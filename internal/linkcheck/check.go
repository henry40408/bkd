package linkcheck

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/idna"
)

const maxRequestsInFlight = 16

// A slow site is usually busy, not gone, so a timeout is "unknown" rather than "broken".
const (
	detailTimedOut        = "Timed out"
	detailCouldNotConnect = "Could not connect"
	detailNotPublic       = "Address is not public"
	detailBotCheck        = "Blocked by a bot check"
	detailNotValid        = "Not a valid address"
	detailCheckFailed     = "Check failed"
)

// Status says what a check found.
type Status string

const (
	// StatusOK: answered with success at the address that was saved.
	StatusOK Status = "ok"
	// StatusRedirected: answered with success, but only after a redirect to
	// another address.
	StatusRedirected Status = "redirected"
	StatusBroken     Status = "broken"
	// StatusUnknown: the site answered in a way that does not say whether the
	// page exists, such as a refusal for robots, or the address was not contacted.
	StatusUnknown Status = "unknown"
)

// Verdict is a status and a short detail for people: `HTTP 404`, or the
// address a redirect ended at.
type Verdict struct {
	Status Status
	Detail string
}

// Backoff names a site that should be left alone for a while. RetryAfter is
// what the site asked for, when it said; how long to really wait is for the
// caller to decide.
type Backoff struct {
	Host       string
	RetryAfter *time.Duration
}

// Inspection is a verdict, and whether the site should be left alone.
type Inspection struct {
	Verdict Verdict
	Backoff *Backoff
	// Answered: a site replied, as opposed to a timeout or a failed connection.
	Answered bool
	timedOut bool
}

// Config sets up a Checker. The zero value contacts public addresses only.
type Config struct {
	// Timeout is the budget for one bookmark, HEAD and GET together.
	Timeout time.Duration
	// Allow admits addresses that are not public. Only tests set it.
	Allow func(netip.Addr) bool
	// Logger receives a recovered panic; nil discards.
	Logger *slog.Logger
}

// Checker asks addresses whether they answer. It is safe for concurrent use.
type Checker struct {
	client   *http.Client
	guard    guard
	inFlight chan struct{}
	budget   time.Duration
	logger   *slog.Logger
}

// New creates a Checker.
func New(config Config) *Checker {
	g := guard{allow: config.Allow}
	budget := config.Timeout
	if budget <= 0 {
		budget = requestTimeout
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Checker{
		logger:   logger,
		client:   newHTTPClient(g),
		guard:    g,
		inFlight: make(chan struct{}, maxRequestsInFlight),
		budget:   budget,
	}
}

// Inspect asks one address. The request slot is across all users: a few slow
// sites must not tie up the server.
func (c *Checker) Inspect(ctx context.Context, address string) Inspection {
	target, err := url.Parse(address)
	if err != nil || target.Scheme == "" {
		return withoutBackoff(Verdict{StatusUnknown, detailNotValid})
	}
	if !c.guard.allowsURL(target) {
		return withoutBackoff(Verdict{StatusUnknown, detailNotPublic})
	}
	select {
	case c.inFlight <- struct{}{}:
		defer func() { <-c.inFlight }()
	case <-ctx.Done():
		return withoutBackoff(Verdict{StatusUnknown, detailCouldNotConnect})
	}
	// HEAD and the GET after it share one budget, so a slow site costs 10 seconds, not 20.
	ctx, cancel := context.WithTimeout(ctx, c.budget)
	defer cancel()
	return c.request(ctx, target)
}

func (c *Checker) request(ctx context.Context, target *url.URL) Inspection {
	reply, err := ask(ctx, c.client, http.MethodHead, target)
	if err == nil && reply.isFinal() {
		return fromAnswer(target, reply)
	}
	// Waiting for the same host again would only repeat the delay.
	if err != nil && (isTimeout(err) || errors.Is(err, errBlocked)) {
		return fromError(err)
	}
	// Many servers refuse or mishandle HEAD, so anything else is confirmed with GET.
	reply, err = ask(ctx, c.client, http.MethodGet, target)
	if err != nil {
		return fromError(err)
	}
	return fromAnswer(target, reply)
}

func withoutBackoff(verdict Verdict) Inspection {
	return Inspection{Verdict: verdict}
}

func fromError(err error) Inspection {
	return Inspection{Verdict: verdictForError(err), timedOut: isTimeout(err)}
}

func fromAnswer(requested *url.URL, reply answer) Inspection {
	inspection := Inspection{Verdict: verdictFor(requested, reply), Answered: true}
	if reply.asksToWait() {
		// The site that answered, which after a redirect is not the one that was asked.
		inspection.Backoff = &Backoff{Host: HostOf(serialize(reply.finalURL)), RetryAfter: reply.retryAfter}
	}
	return inspection
}

func verdictFor(requested *url.URL, reply answer) Verdict {
	if reply.challenged {
		return Verdict{StatusUnknown, detailBotCheck}
	}
	if reply.status >= 200 && reply.status < 300 {
		if normalizeForCompare(requested) == normalizeForCompare(reply.finalURL) {
			return Verdict{StatusOK, ""}
		}
		return Verdict{StatusRedirected, serialize(reply.finalURL)}
	}
	detail := "HTTP " + strconv.Itoa(reply.status)
	if isInconclusive(reply.status) {
		return Verdict{StatusUnknown, detail}
	}
	return Verdict{StatusBroken, detail}
}

// isInconclusive: 401, 403, 429 and 503 say "not for robots" or "not now",
// not "gone". 999 is LinkedIn's refusal. Statuses that are neither client nor
// server errors, such as a 304, say nothing about the page either.
func isInconclusive(status int) bool {
	switch status {
	case 401, 403, 429, 503, 999:
		return true
	}
	return status < 400 || status >= 600
}

func verdictForError(err error) Verdict {
	switch {
	case errors.Is(err, errBlocked):
		return Verdict{StatusUnknown, detailNotPublic}
	case errors.Is(err, errTooManyRedirects):
		return Verdict{StatusBroken, "Too many redirects"}
	case isTimeout(err):
		return Verdict{StatusUnknown, detailTimedOut}
	case isBadRedirect(err):
		return Verdict{StatusBroken, "Bad redirect"}
	}
	return Verdict{StatusBroken, detailCouldNotConnect}
}

// normalizeForCompare makes two spellings of one address equal: case of scheme
// and host, an empty path, a default port and the fragment do not count.
func normalizeForCompare(address *url.URL) string {
	scheme := strings.ToLower(address.Scheme)
	path := address.EscapedPath()
	if path == "" {
		path = "/"
	}
	normalized := scheme + "://" + canonicalAuthority(address) + path
	if address.RawQuery != "" || address.ForceQuery {
		normalized += "?" + address.RawQuery
	}
	return normalized
}

// serialize writes an address the way the WHATWG URL parser does, so the detail
// stored for a redirect matches what the Rust server stored: host in lower case
// and punycode, no default port, an empty path as "/".
func serialize(address *url.URL) string {
	canonical := *address
	canonical.Scheme = strings.ToLower(address.Scheme)
	canonical.Host = canonicalAuthority(address)
	if canonical.Path == "" && canonical.RawPath == "" {
		canonical.Path = "/"
	}
	return canonical.String()
}

// canonicalHost is the lower-case punycode form of a host name, the spelling
// the WHATWG URL parser keeps. IPv6 literals are only lower-cased.
func canonicalHost(hostname string) string {
	host := strings.ToLower(hostname)
	if strings.Contains(host, ":") {
		return host
	}
	if ascii, err := idna.Lookup.ToASCII(host); err == nil {
		return ascii
	}
	return host
}

// canonicalAuthority is the host and port without a default port. A name that
// IDNA refuses stays as it is, in lower case.
func canonicalAuthority(address *url.URL) string {
	host := canonicalHost(address.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	port := address.Port()
	if port == "" || port == defaultPort(strings.ToLower(address.Scheme)) {
		return host
	}
	return host + ":" + port
}

func defaultPort(scheme string) string {
	switch scheme {
	case "http":
		return "80"
	case "https":
		return "443"
	}
	return ""
}

// HostOf is the host that requests are spaced by. `www.example.com` and
// `example.com` are one site, so they count as one.
func HostOf(address string) string {
	parsed, err := url.Parse(address)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(canonicalHost(parsed.Hostname()), "www.")
}
