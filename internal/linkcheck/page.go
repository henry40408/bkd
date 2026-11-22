package linkcheck

import (
	"context"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
)

// The tags that describe a page are in its head, so the start of the page is
// enough, and a huge or endless body cannot fill the memory of 16 slots.
const maxPageBytes = 512 << 10

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
	target, err := url.Parse(address)
	if err != nil || target.Scheme == "" {
		return Page{}, PageError(detailNotValid)
	}
	if !c.guard.allowsURL(target) {
		return Page{}, PageError(detailNotPublic)
	}
	release, ok := c.acquireSlot(ctx)
	if !ok {
		return Page{}, PageError(detailCouldNotConnect)
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, c.budget)
	defer cancel()
	if userAgent == "" {
		userAgent = DefaultUserAgent()
	}
	return c.fetchPage(ctx, target, userAgent)
}

func (c *Checker) fetchPage(ctx context.Context, target *url.URL, userAgent string) (Page, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return Page{}, PageError(detailNotValid)
	}
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	response, err := c.client.Do(request)
	if err != nil {
		return Page{}, PageError(verdictForError(err).Detail)
	}
	defer func() { _ = response.Body.Close() }()
	if detail := refusalDetail(response); detail != "" {
		return Page{}, PageError(detail)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxPageBytes))
	if err != nil {
		return Page{}, PageError(verdictForError(err).Detail)
	}
	return Page{Body: body}, nil
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
