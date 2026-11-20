// Package importer imports a Netscape bookmark file: parse, apply the usual
// bookmark rules, skip what cannot be kept and report it.
package importer

import (
	"context"
	"fmt"

	"github.com/henry40408/bkd/internal/bookmarks"
	"github.com/henry40408/bkd/internal/netscape"
	"github.com/henry40408/bkd/internal/truncate"
)

const (
	// invalidExamples is how many rejected links the report names. The count
	// covers all of them.
	invalidExamples = 5
	maxExampleChars = 200
	// secondsLimit: some exporters write milliseconds or microseconds instead
	// of seconds.
	secondsLimit int64 = 100_000_000_000
)

// Report says what an import did. JSON field order is part of the API.
type Report struct {
	Added           int           `json:"added"`
	Duplicates      int           `json:"duplicates"`
	Invalid         int           `json:"invalid"`
	InvalidExamples []InvalidLink `json:"invalid_examples"`
}

// InvalidLink is a link that could not be kept, and why.
type InvalidLink struct {
	URL    string `json:"url"`
	Reason string `json:"reason"`
}

// Request is one import: whose bookmarks, from what file, at what time.
type Request struct {
	UserID int64
	HTML   string
	Now    int64
}

// ImportHTML adds the links of a bookmark file for one user.
func ImportHTML(ctx context.Context, store *bookmarks.Store, req Request) (Report, error) {
	var entries []bookmarks.ImportEntry
	invalid := []InvalidLink{}
	for _, link := range netscape.Parse(req.HTML) {
		entry, rejected := toEntry(link, req.Now)
		if rejected != nil {
			invalid = append(invalid, *rejected)
			continue
		}
		entries = append(entries, entry)
	}
	added, err := store.Import(ctx, req.UserID, entries)
	if err != nil {
		return Report{}, fmt.Errorf("import bookmarks: %w", err)
	}
	return Report{
		Added:           added,
		Duplicates:      len(entries) - added,
		Invalid:         len(invalid),
		InvalidExamples: invalid[:min(len(invalid), invalidExamples)],
	}, nil
}

func toEntry(link netscape.Link, now int64) (bookmarks.ImportEntry, *InvalidLink) {
	input := bookmarks.Fitted(link.URL, link.Title, link.Note, link.Tags)
	bookmark, err := input.Validate()
	if err != nil {
		return bookmarks.ImportEntry{}, &InvalidLink{URL: truncate.Runes(link.URL, maxExampleChars), Reason: err.Error()}
	}
	return bookmarks.ImportEntry{Bookmark: bookmark, CreatedAt: createdAt(link.Added, now)}, nil
}

// createdAt: a missing, zero, negative or future date means the exporter did
// not know it, so the link counts as saved now instead of landing in 1970 or
// after the present.
func createdAt(added *int64, now int64) int64 {
	if added == nil {
		return now
	}
	timestamp := toSeconds(*added)
	if timestamp > 0 && timestamp <= now {
		return timestamp
	}
	return now
}

func toSeconds(timestamp int64) int64 {
	for timestamp >= secondsLimit {
		timestamp /= 1000
	}
	return timestamp
}
