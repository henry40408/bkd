package linkcheck

import (
	"context"
	"errors"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	maxRedirects   = 5
	requestTimeout = 10 * time.Second
	connectTimeout = 5 * time.Second
)

var errTooManyRedirects = errors.New("too many redirects")

// answer is what a site said, without its body.
type answer struct {
	status   int
	finalURL *url.URL
	// challenged: the site put a bot check in front of the page (Cloudflare's
	// `cf-mitigated`), so the status says nothing about the page.
	challenged bool
	// retryAfter is the seconds form only; a date is rare, and the default
	// wait is a safe answer. Nil when the site did not say.
	retryAfter *time.Duration
}

// asksToWait: "too many requests" or "unavailable" mean the site wants to be
// left alone for a while.
func (a answer) asksToWait() bool {
	return a.status == http.StatusTooManyRequests || a.status == http.StatusServiceUnavailable
}

// isFinal: asking again with GET would get the same refusal, so it only adds load.
func (a answer) isFinal() bool {
	return (a.status >= 200 && a.status < 300) || a.challenged || a.asksToWait()
}

const maxResponseHeaderBytes = 64 << 10

func newHTTPClient(g guard) *http.Client {
	dialer := net.Dialer{Timeout: connectTimeout}
	transport := &http.Transport{
		Proxy: nil,
		// Every host is visited once, so a pooled connection is only an open file descriptor.
		DisableKeepAlives: true,
		ForceAttemptHTTP2: false,
		// hyper stops at about 400 KiB; the default 10 MiB would let a slow
		// site hold memory in all 16 slots.
		MaxResponseHeaderBytes: maxResponseHeaderBytes,
		TLSHandshakeTimeout:    connectTimeout,
		DialContext:            guardedDial(g, &dialer),
	}
	return &http.Client{
		Transport:     transport,
		CheckRedirect: followOnlyPublic(g),
	}
}

func followOnlyPublic(g guard) func(*http.Request, []*http.Request) error {
	return func(next *http.Request, via []*http.Request) error {
		if len(via) > maxRedirects {
			return errTooManyRedirects
		}
		if !g.allowsURL(next.URL) {
			return errBlocked
		}
		return nil
	}
}

// guardedDial resolves a host name and connects only to its public addresses.
// Resolving and connecting happen here together, so no second lookup can
// swap in another address.
func guardedDial(g guard, dialer *net.Dialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		found, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		allowed := publicOnly(g, found)
		if len(found) > 0 && len(allowed) == 0 {
			return nil, errBlocked
		}
		return dialFirst(ctx, dialer, dialTarget{network: network, port: port, addrs: allowed})
	}
}

func publicOnly(g guard, addrs []netip.Addr) []netip.Addr {
	var allowed []netip.Addr
	for _, addr := range addrs {
		if g.allows(addr) {
			allowed = append(allowed, addr)
		}
	}
	return allowed
}

type dialTarget struct {
	network string
	port    string
	addrs   []netip.Addr
}

func dialFirst(ctx context.Context, dialer *net.Dialer, target dialTarget) (net.Conn, error) {
	if len(target.addrs) == 0 {
		return nil, errors.New("no address for the host")
	}
	var lastErr error
	for _, addr := range target.addrs {
		conn, err := dialer.DialContext(ctx, target.network, net.JoinHostPort(addr.String(), target.port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// question is one request to a site: what to ask, and who says they are asking.
type question struct {
	method    string
	target    *url.URL
	userAgent string
}

// ask reads only the status line and headers. Closing the body drops the
// connection, so a GET never downloads the page.
func ask(ctx context.Context, client *http.Client, q question) (answer, error) {
	request, err := http.NewRequestWithContext(ctx, q.method, q.target.String(), nil)
	if err != nil {
		return answer{}, err
	}
	request.Header.Set("User-Agent", q.userAgent)
	response, err := client.Do(request)
	if err != nil {
		return answer{}, err
	}
	_ = response.Body.Close()
	return answer{
		status:     response.StatusCode,
		finalURL:   response.Request.URL,
		challenged: isChallenge(response.Header),
		retryAfter: retryAfter(response.Header.Get("Retry-After")),
	}, nil
}

func isChallenge(header http.Header) bool {
	return strings.EqualFold(header.Get("Cf-Mitigated"), "challenge")
}

func retryAfter(header string) *time.Duration {
	seconds, err := strconv.ParseUint(strings.TrimSpace(header), 10, 64)
	if err != nil {
		return nil
	}
	// A huge value still means "as long as allowed"; it must not overflow a Duration.
	seconds = min(seconds, uint64(math.MaxInt64/time.Second))
	wait := time.Duration(seconds) * time.Second
	return &wait
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// isBadRedirect: net/http gives no typed error for a Location it cannot read.
func isBadRedirect(err error) bool {
	return strings.Contains(err.Error(), "Location header")
}
