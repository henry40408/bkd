package linkcheck

import (
	"bytes"
	"context"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"

	"github.com/henry40408/bkd/internal/sitemeta"
	"github.com/henry40408/bkd/internal/truncate"
)

// The tags that describe a page are in its head, so the start of the page is
// enough, and a huge or endless body cannot fill the memory of 16 slots.
const maxPageBytes = 512 << 10

// maxReadMetaChars cuts each of a read's title and description, since a page
// can say 500 KB in one meta tag and a batch holds all its results until they
// are sent. It is above what a bookmark keeps (300 and 500 characters), so the
// bookmark's own cut is the one that decides.
const maxReadMetaChars = 1000

const detailNotHTML = "Not an HTML page"

// Page is the start of a web page.
type Page struct {
	// Body is at most maxPageBytes long.
	Body []byte
}

// PageError says why a page could not be read, in words that are safe to show
// to the user.
type PageError string

func (e PageError) Error() string { return string(e) }

// ReadPage fetches the start of an HTML page, saying userAgent is asking; empty
// means DefaultUserAgent. It goes through the same guard, request slots and time
// budget as Inspect, so it too contacts public addresses only, including through
// redirects and DNS answers.
func (c *Checker) ReadPage(ctx context.Context, address, userAgent string) (Page, error) {
	page, inspection := c.readPage(ctx, address, userAgent)
	if inspection.Verdict.Status != StatusOK {
		return Page{}, PageError(inspection.Verdict.Detail)
	}
	return page, nil
}

// inspectRead reads one target's page and keeps what the page says about itself
// instead of its body, so a batch of reads does not hold a page per target in
// memory while it waits to be collected.
func (c *Checker) inspectRead(ctx context.Context, target Target) Inspection {
	page, inspection := c.readPage(ctx, target.URL, target.UserAgent)
	if inspection.Verdict.Status != StatusOK {
		return inspection
	}
	meta := sitemeta.Parse(bytes.NewReader(page.Body))
	meta.Title = truncate.Runes(meta.Title, maxReadMetaChars)
	meta.Description = truncate.Runes(meta.Description, maxReadMetaChars)
	inspection.Meta = &meta
	return inspection
}

// readPage is ReadPage with what the run of a batch needs to know besides the
// page: a failure is a verdict that is not OK, whose detail says why, and the answer of
// a site that asks to be left alone is a backoff. The page is set only when the
// verdict is OK.
func (c *Checker) readPage(ctx context.Context, address, userAgent string) (Page, Inspection) {
	target, err := url.Parse(address)
	if err != nil || target.Scheme == "" {
		return Page{}, withoutBackoff(Verdict{StatusUnknown, detailNotValid})
	}
	if !c.guard.allowsURL(target) {
		return Page{}, withoutBackoff(Verdict{StatusUnknown, detailNotPublic})
	}
	release, ok := c.acquireSlot(ctx)
	if !ok {
		return Page{}, withoutBackoff(Verdict{StatusUnknown, detailCouldNotConnect})
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, c.budget)
	defer cancel()
	if userAgent == "" {
		userAgent = DefaultUserAgent()
	}
	return c.fetchPage(ctx, target, userAgent)
}

func (c *Checker) fetchPage(ctx context.Context, target *url.URL, userAgent string) (Page, Inspection) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return Page{}, withoutBackoff(Verdict{StatusUnknown, detailNotValid})
	}
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	response, err := c.client.Do(request)
	if err != nil {
		return Page{}, fromError(err)
	}
	defer func() { _ = response.Body.Close() }()
	reply := answer{
		status: response.StatusCode, finalURL: response.Request.URL,
		challenged: isChallenge(response.Header), retryAfter: retryAfter(response.Header.Get("Retry-After")),
	}
	inspection := Inspection{Answered: true}
	if reply.asksToWait() {
		inspection.Backoff = &Backoff{Host: HostOf(serialize(reply.finalURL)), RetryAfter: reply.retryAfter}
	}
	if detail := refusalDetail(response); detail != "" {
		inspection.Verdict = Verdict{StatusUnknown, detail}
		return Page{}, inspection
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxPageBytes))
	if err != nil {
		failure := fromError(err)
		failure.Answered = true
		return Page{}, failure
	}
	inspection.Verdict = Verdict{StatusOK, ""}
	return Page{Body: body}, inspection
}

// refusalDetail is why a response has no page to read, or "" when it has one.
func refusalDetail(response *http.Response) string {
	if isChallenge(response.Header) {
		return detailBotCheck
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "HTTP " + strconv.Itoa(response.StatusCode)
	}
	if !isHTML(response.Header.Get("Content-Type")) {
		return detailNotHTML
	}
	return ""
}

// isHTML accepts a missing type too: a site that does not say what it sends
// still may send a page, and the parser reads text only.
func isHTML(contentType string) bool {
	if contentType == "" {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && (mediaType == "text/html" || mediaType == "application/xhtml+xml")
}
