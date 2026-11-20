package views

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// fold maps a character to its lowercase form for matching. Rust takes the
// first character of the full lowercase mapping; unicode.ToLower is the
// single-character mapping, which differs only for a few special cases
// (final sigma).
func fold(r rune) rune { return unicode.ToLower(r) }

// matchLength is how many characters of text from `from` the longest matching
// term covers, or 0 when no term matches there.
func matchLength(text []rune, from int, terms [][]rune) int {
	longest := 0
	for _, term := range terms {
		if len(term) == 0 || from+len(term) > len(text) || len(term) <= longest {
			continue
		}
		if equalFolded(text[from:from+len(term)], term) {
			longest = len(term)
		}
	}
	return longest
}

func equalFolded(left, right []rune) bool {
	for i := range left {
		if fold(left[i]) != fold(right[i]) {
			return false
		}
	}
	return true
}

type run struct {
	matched bool
	text    string
}

// splitMatches cuts text into runs; matched marks a run that matches a term,
// ignoring case.
func splitMatches(s string, terms []string) []run {
	termRunes := make([][]rune, len(terms))
	for i, term := range terms {
		termRunes[i] = []rune(term)
	}
	characters := []rune(s)
	var runs []run
	for at := 0; at < len(characters); {
		length := matchLength(characters, at, termRunes)
		matched, step := length > 0, max(length, 1)
		piece := string(characters[at : at+step])
		at += step
		if last := len(runs) - 1; last >= 0 && runs[last].matched == matched {
			runs[last].text += piece
			continue
		}
		runs = append(runs, run{matched: matched, text: piece})
	}
	return runs
}

// highlight is text with every occurrence of a term wrapped in <mark>.
func highlight(s string, terms []string) node {
	var b strings.Builder
	for _, r := range splitMatches(s, terms) {
		if r.matched {
			b.WriteString(string(el("mark", nil, text(r.text))))
			continue
		}
		b.WriteString(string(text(r.text)))
	}
	return node(b.String())
}

// DomainOf is the host of a URL without www., or the text itself when it has
// no host or cannot be parsed.
func DomainOf(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return rawURL
	}
	return strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
}

// SavedTime is a saved time as the page shows it. The server cannot know the
// reader's time zone, so the text is relative and the tooltip is in UTC; the
// script may replace it with local time.
type SavedTime struct {
	ISO   string
	Exact string
	Ago   string
}

// NewSavedTime formats unixSeconds relative to now.
func NewSavedTime(now, unixSeconds int64) SavedTime {
	t := time.Unix(unixSeconds, 0).UTC()
	return SavedTime{
		ISO:   t.Format("2006-01-02T15:04:05Z"),
		Exact: t.Format("2006-01-02 15:04") + " UTC",
		Ago:   timeAgo(now, unixSeconds),
	}
}

func plural(count int64, unit string) string {
	suffix := "s"
	if count == 1 {
		suffix = ""
	}
	return fmt.Sprintf("%d %s%s ago", count, unit, suffix)
}

func timeAgo(now, then int64) string {
	hours := (now - then) / 3_600
	if hours < 1 {
		return "just now"
	}
	if hours < 24 {
		return plural(hours, "hour")
	}
	days := hours / 24
	switch {
	case days == 1:
		return "yesterday"
	case days < 7:
		return plural(days, "day")
	case days < 30:
		return plural(days/7, "week")
	case days < 365:
		return plural(days/30, "month")
	}
	return plural(days/365, "year")
}

// ListView is what a list page is showing: the search, the tag, the link
// status and the page.
type ListView struct {
	Q      string
	Tag    string
	Status string
	Page   uint32
	Select bool
	// All is select mode with every bookmark on the page already ticked.
	All bool
}

// HrefForPage is the address of this view with the page changed.
func (v ListView) HrefForPage(page uint32) string {
	v.Page = page
	return v.Href()
}

// Href is the address of this view.
func (v ListView) Href() string { return v.HrefTo("/") }

// HrefTo is the same view on another path, such as /random.
func (v ListView) HrefTo(path string) string {
	var pairs [][2]string
	if q := strings.TrimSpace(v.Q); q != "" {
		pairs = append(pairs, [2]string{"q", q})
	}
	if v.Tag != "" {
		pairs = append(pairs, [2]string{"tag", v.Tag})
	}
	if v.Status != "" {
		pairs = append(pairs, [2]string{"status", v.Status})
	}
	if v.Page > 1 {
		pairs = append(pairs, [2]string{"page", strconv.FormatUint(uint64(v.Page), 10)})
	}
	if v.Select {
		pairs = append(pairs, [2]string{"select", "1"})
	}
	if v.Select && v.All {
		pairs = append(pairs, [2]string{"all", "1"})
	}
	return WithQuery(path, pairs)
}

// WithoutSelect leaves select mode.
func (v ListView) WithoutSelect() ListView {
	v.Select, v.All = false, false
	return v
}

// WithAll is the same select-mode view with every bookmark ticked, or none.
func (v ListView) WithAll(all bool) ListView {
	v.Select, v.All = true, all
	return v
}

// WithSelect enters select mode.
func (v ListView) WithSelect() ListView {
	v.Select = true
	return v
}

// WithQuery is path with the pairs appended, after any query it already has.
// Everything except letters, digits and -_.~ is percent-encoded.
func WithQuery(path string, pairs [][2]string) string {
	var b strings.Builder
	b.WriteString(path)
	hasQuery := strings.Contains(path, "?")
	for _, pair := range pairs {
		if hasQuery {
			b.WriteByte('&')
		} else {
			b.WriteByte('?')
			hasQuery = true
		}
		b.WriteString(pair[0] + "=")
		b.WriteString(escapeValue(pair[1]))
	}
	return b.String()
}

// escapeValue is QueryEscape with %20 for a space: the pages put the result in a
// URL query, and a space is never written as +.
func escapeValue(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}
