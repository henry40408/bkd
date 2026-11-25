package server

import (
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/henry40408/bkd/internal/bookmarks"
)

// What a bookmark and a tag look like to a program that speaks linkding.
//
// linkding's description is the text the page says about itself, which bkd keeps
// as site_description, and its notes are the user's own text, bkd's note. What
// bkd has no counterpart for (archive, unread, sharing, favicon, preview image)
// is answered with the value of a bookmark that never used it.

const linkdingTimeLayout = "2006-01-02T15:04:05.000000Z"

type linkdingBookmark struct {
	ID                    int64    `json:"id"`
	URL                   string   `json:"url"`
	Title                 string   `json:"title"`
	Description           string   `json:"description"`
	Notes                 string   `json:"notes"`
	WebArchiveSnapshotURL string   `json:"web_archive_snapshot_url"`
	FaviconURL            *string  `json:"favicon_url"`
	PreviewImageURL       *string  `json:"preview_image_url"`
	IsArchived            bool     `json:"is_archived"`
	Unread                bool     `json:"unread"`
	Shared                bool     `json:"shared"`
	TagNames              []string `json:"tag_names"`
	DateAdded             string   `json:"date_added"`
	DateModified          string   `json:"date_modified"`
	WebsiteTitle          *string  `json:"website_title"`
	WebsiteDescription    *string  `json:"website_description"`
}

func linkdingBookmarkOf(bookmark bookmarks.Bookmark) linkdingBookmark {
	site := bookmark.Site.Value()
	return linkdingBookmark{
		ID:                 bookmark.ID,
		URL:                bookmark.URL,
		Title:              bookmark.Title,
		Description:        site.Description,
		Notes:              bookmark.Note,
		TagNames:           bookmark.Tags,
		DateAdded:          linkdingTime(bookmark.CreatedAt),
		DateModified:       linkdingTime(bookmark.UpdatedAt),
		WebsiteTitle:       nullIfEmpty(site.Title),
		WebsiteDescription: nullIfEmpty(site.Description),
	}
}

func nullIfEmpty(text string) *string {
	if text == "" {
		return nil
	}
	return &text
}

func linkdingTime(unixSeconds int64) string {
	return time.Unix(unixSeconds, 0).UTC().Format(linkdingTimeLayout)
}

type linkdingTag struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	DateAdded string `json:"date_added"`
}

func linkdingTagOf(entry bookmarks.TagEntry) linkdingTag {
	return linkdingTag{ID: entry.ID, Name: entry.Name, DateAdded: linkdingTime(entry.AddedAt)}
}

const (
	linkdingDefaultLimit uint32 = 100
	// linkdingMaxBookmarkLimit is more than the 200 of the page-sized lists: a
	// program that mirrors every bookmark asks for big pages.
	linkdingMaxBookmarkLimit uint32 = 1000
	// linkdingMaxTagLimit is what the official browser extension asks for: it
	// reads one page of tags for its autocomplete and never follows `next`.
	linkdingMaxTagLimit uint32 = 5000
)

// linkdingPaging is limit and offset as Django REST framework reads them: a value
// that is not a number, or a limit below 1, is the default and not an error, and
// a number too big is the biggest allowed.
type linkdingPaging struct {
	limit  uint32
	offset uint32
}

func linkdingPagingOf(r *http.Request, maxLimit uint32) linkdingPaging {
	query := r.URL.Query()
	paging := linkdingPaging{limit: linkdingDefaultLimit}
	if limit, ok := wholeNumber(query.Get("limit")); ok && limit > 0 {
		paging.limit = uint32(min(limit, uint64(maxLimit)))
	}
	if offset, ok := wholeNumber(query.Get("offset")); ok {
		paging.offset = uint32(min(offset, math.MaxUint32))
	}
	return paging
}

// wholeNumber reads digits. A number beyond 64 bits comes back as the largest one
// rather than as a failure, so the caller's cap applies to it.
func wholeNumber(text string) (uint64, bool) {
	number, err := strconv.ParseUint(text, 10, 64)
	return number, err == nil || errors.Is(err, strconv.ErrRange)
}

func (paging linkdingPaging) page() bookmarks.Page {
	return bookmarks.Page{Limit: paging.limit, Offset: paging.offset}
}

// linkdingList is the envelope of every list.
type linkdingList[T any] struct {
	Count    int64   `json:"count"`
	Next     *string `json:"next"`
	Previous *string `json:"previous"`
	Results  []T     `json:"results"`
}

// newLinkdingList wraps one page of results. count is how many there are in all,
// and the links are this request's address with the page moved.
func newLinkdingList[T any](r *http.Request, scheme string, paging linkdingPaging, count int64, results []T) linkdingList[T] {
	base := url.URL{Scheme: scheme, Host: r.Host, Path: r.URL.Path, RawQuery: r.URL.RawQuery}
	return linkdingList[T]{
		Count:    count,
		Next:     nextLink(base, paging, count),
		Previous: previousLink(base, paging),
		Results:  results,
	}
}

// publicScheme is https when the operator says the site is served over it, when
// the connection is TLS itself, or when a proxy says it received the request over
// TLS. The header is believed only in this direction: a client that forges it
// can make a link that does not work for itself, and a plain http link would
// make a client send its token in the clear.
func (a *App) publicScheme(r *http.Request) string {
	forwarded, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
	if a.secureCookies || r.TLS != nil || strings.EqualFold(strings.TrimSpace(forwarded), "https") {
		return "https"
	}
	return "http"
}

func nextLink(base url.URL, paging linkdingPaging, count int64) *string {
	next := int64(paging.offset) + int64(paging.limit)
	if next >= count {
		return nil
	}
	return linkWith(base, paging.limit, &next)
}

func previousLink(base url.URL, paging linkdingPaging) *string {
	if paging.offset == 0 {
		return nil
	}
	previous := int64(paging.offset) - int64(paging.limit)
	if previous <= 0 {
		return linkWith(base, paging.limit, nil)
	}
	return linkWith(base, paging.limit, &previous)
}

// linkWith is the request's address with this limit and offset, and its other
// parameters unchanged. A nil offset leaves the parameter out.
func linkWith(base url.URL, limit uint32, offset *int64) *string {
	query := base.Query()
	query.Set("limit", strconv.FormatUint(uint64(limit), 10))
	query.Del("offset")
	if offset != nil {
		query.Set("offset", strconv.FormatInt(*offset, 10))
	}
	base.RawQuery = query.Encode()
	link := base.String()
	return &link
}
