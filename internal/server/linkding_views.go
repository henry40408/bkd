package server

import (
	"math"
	"net/http"
	"net/url"
	"strconv"
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
	// linkdingMaxLimit is more than the 200 of the page-sized lists: a program
	// that mirrors every bookmark asks for big pages, and a page is small.
	linkdingMaxLimit uint32 = 1000
)

// linkdingPaging is limit and offset as Django REST framework reads them: a value
// that is not a number, or a limit below 1, is the default and not an error.
type linkdingPaging struct {
	limit  uint32
	offset uint32
}

func linkdingPagingOf(r *http.Request) linkdingPaging {
	query := r.URL.Query()
	paging := linkdingPaging{limit: linkdingDefaultLimit}
	if limit, err := strconv.ParseUint(query.Get("limit"), 10, 32); err == nil && limit > 0 {
		paging.limit = uint32(min(limit, uint64(linkdingMaxLimit)))
	}
	if offset, err := strconv.ParseUint(query.Get("offset"), 10, 64); err == nil {
		paging.offset = uint32(min(offset, math.MaxUint32))
	}
	return paging
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

// publicScheme is https when the operator says the site is served over it, or
// when the connection is TLS itself: a link to the next page has to be one the
// client can follow.
func (a *App) publicScheme(r *http.Request) string {
	if a.secureCookies || r.TLS != nil {
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
