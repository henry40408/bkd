// Package bookmarks holds bookmarks and their tags. Every Store method takes the
// owner's user id and puts it in the SQL, so a bookmark id that belongs to
// someone else behaves as if it did not exist.
package bookmarks

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxURLChars   = 2048
	maxTitleChars = 300
	maxNoteChars  = 5000
	maxTagChars   = 50
	// MaxTagsPerBookmark is the most tags one bookmark may carry.
	MaxTagsPerBookmark = 20
)

// Bookmark is one stored bookmark. JSON field order is part of the API.
type Bookmark struct {
	ID        int64    `json:"id"`
	URL       string   `json:"url"`
	Title     string   `json:"title"`
	Note      string   `json:"note"`
	Tags      []string `json:"tags"`
	CreatedAt int64    `json:"created_at"`
	UpdatedAt int64    `json:"updated_at"`
	// Link is nil until the link is checked, and again after the URL changes.
	Link *LinkCheck `json:"link"`
}

// LinkCheck is the result of the last link check.
type LinkCheck struct {
	// Status is ok, redirected, broken or unknown.
	Status    string `json:"status"`
	CheckedAt int64  `json:"checked_at"`
	Detail    string `json:"detail"`
}

// TagCount is a tag with the number of bookmarks that carry it.
type TagCount struct {
	Tag   string `json:"tag"`
	Count int64  `json:"count"`
}

// LinkCount is a link check result with the number of bookmarks that have it.
type LinkCount struct {
	Status string `json:"status"`
	Count  int64  `json:"count"`
}

// ValidationError is a message that is safe to show to the client.
type ValidationError string

func (e ValidationError) Error() string { return string(e) }

// Input is what a client sends. It becomes a Valid only through Validate.
type Input struct {
	URL   string
	Title string
	Note  string
	Tags  []string
}

// Valid is input that passed every rule. Only this type is stored.
type Valid struct {
	URL   string
	Title string
	Note  string
	Tags  []string
}

// Validate checks every rule and returns the cleaned-up bookmark.
func (in Input) Validate() (Valid, error) {
	url, err := validateURL(in.URL)
	if err != nil {
		return Valid{}, err
	}
	title, err := validateTitle(in.Title)
	if err != nil {
		return Valid{}, err
	}
	note, err := validateNote(in.Note)
	if err != nil {
		return Valid{}, err
	}
	tags, err := NormalizeTags(in.Tags)
	if err != nil {
		return Valid{}, err
	}
	return Valid{URL: url, Title: title, Note: note, Tags: tags}, nil
}

// Fitted is for an imported file, where one long title must not cost the whole
// bookmark: it shortens what is too long, falls back to the URL for a missing
// title and drops tags that cannot be kept. The URL is left alone, because a
// changed URL would point somewhere else.
func Fitted(url, title, note string, tags []string) Input {
	title = strings.TrimSpace(title)
	if title == "" {
		title = strings.TrimSpace(url)
	}
	kept := make([]string, 0, len(tags))
	for _, raw := range tags {
		tag, err := NormalizeTag(raw)
		if err != nil || tag == "" || slices.Contains(kept, tag) {
			continue
		}
		kept = append(kept, tag)
	}
	if len(kept) > MaxTagsPerBookmark {
		kept = kept[:MaxTagsPerBookmark]
	}
	return Input{
		URL:   url,
		Title: truncateRunes(title, maxTitleChars),
		Note:  truncateRunes(note, maxNoteChars),
		Tags:  kept,
	}
}

func validateURL(raw string) (string, error) {
	url := strings.TrimSpace(raw)
	if utf8.RuneCountInString(url) > maxURLChars {
		return "", ValidationError("url is too long")
	}
	// Only web links: the front end turns this into an <a href>, and a
	// `javascript:` URL there would run script.
	lowered := asciiLower(url)
	rest, ok := strings.CutPrefix(lowered, "https://")
	if !ok {
		rest, ok = strings.CutPrefix(lowered, "http://")
	}
	if !ok {
		return "", ValidationError("url must start with http:// or https://")
	}
	if rest == "" || strings.ContainsFunc(url, isSpaceOrControl) {
		return "", ValidationError("url is not valid")
	}
	return url, nil
}

func isSpaceOrControl(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }

func validateTitle(raw string) (string, error) {
	title := strings.TrimSpace(raw)
	if title == "" {
		return "", ValidationError("title is required")
	}
	if utf8.RuneCountInString(title) > maxTitleChars {
		return "", ValidationError("title is too long")
	}
	return title, nil
}

func validateNote(raw string) (string, error) {
	note := strings.TrimRightFunc(raw, unicode.IsSpace)
	if utf8.RuneCountInString(note) > maxNoteChars {
		return "", ValidationError("note is too long")
	}
	return note, nil
}

// NormalizeTag trims and lowercases a tag. It returns "" for a tag that is
// empty after trimming, which callers drop. Lowercasing happens here, not in
// SQL, because SQLite folds only ASCII.
func NormalizeTag(raw string) (string, error) {
	tag := strings.ToLower(strings.TrimSpace(raw))
	if tag == "" {
		return "", nil
	}
	if utf8.RuneCountInString(tag) > maxTagChars {
		return "", ValidationError("tag is too long")
	}
	if strings.ContainsFunc(tag, unicode.IsControl) {
		return "", ValidationError("tag contains a control character")
	}
	return tag, nil
}

// NormalizeTags normalizes each tag, drops empty ones and duplicates, and
// keeps the first order.
func NormalizeTags(raw []string) ([]string, error) {
	tags := make([]string, 0, len(raw))
	for _, item := range raw {
		tag, err := NormalizeTag(item)
		if err != nil {
			return nil, err
		}
		if tag == "" || slices.Contains(tags, tag) {
			continue
		}
		tags = append(tags, tag)
	}
	if len(tags) > MaxTagsPerBookmark {
		return nil, ValidationError("too many tags")
	}
	return tags, nil
}

func truncateRunes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit])
}

// asciiLower must not touch non-ASCII letters, unlike strings.ToLower.
func asciiLower(text string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, text)
}
