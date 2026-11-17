// Package search turns what a user types into a bookmark filter. It uses plain
// LIKE, chosen over FTS5 because FTS5's tokenizers split words on spaces and
// punctuation and so handle Chinese poorly.
//
// Every term must match (AND). A plain term matches in the title, URL, note or
// any tag. LIKE ignores case for ASCII only, so "É" does not match "é".
// Matching highlights are drawn by the front end.
//
// The query syntax (the front end reads it the same way; testdata/search_syntax.json
// holds the cases both sides must agree on):
//
//   - "two words" is one term. \" is a quote inside it. A quote that is never
//     closed runs to the end of the query.
//   - -term must NOT match. A lone - is plain text.
//   - title:, url:, note: match only that field; tag: matches a tag exactly;
//     status: matches the link check result (ok, redirected, broken, unknown or
//     unchecked). The value may be quoted: tag:"to read". Any other name: (such as
//     https://x.test) and a prefix with nothing after it are plain text.
//
// Only SQL shape is built from strings here; every user value goes through a
// bind parameter.
package search

import (
	"strings"
	"unicode"

	"github.com/henry40408/bkd/internal/bookmarks"
)

const (
	maxTerms     = 8
	maxTermChars = 100
)

// Field is the part of a bookmark a term looks at.
type Field int

const (
	Any Field = iota
	Title
	URL
	Note
	Tag
	Status
)

// String is the field's name in testdata/search_syntax.json.
func (f Field) String() string {
	return [...]string{"any", "title", "url", "note", "tag", "status"}[f]
}

// Term is one piece of a query.
type Term struct {
	Field   Field
	Text    string
	Negated bool
}

// Filter is a parsed query with an optional exact tag and link status. The zero
// value lets everything through. It implements bookmarks.Filter.
type Filter struct {
	terms  []Term
	tag    string
	status string
}

// NewFilter takes what the user typed in the search box and an exact tag (""
// for none). Extra terms beyond the limit are ignored and long terms are cut.
func NewFilter(query, tag string) Filter {
	return Filter{terms: Parse(query), tag: exactTag(tag)}
}

// WithStatus keeps only bookmarks with this link check result (unchecked for
// none). An unknown name matches nothing; a blank one changes nothing.
func (f Filter) WithStatus(status string) Filter {
	f.status = strings.ToLower(strings.TrimSpace(status))
	return f
}

// Tag is the normalized exact tag, or "" when there is none.
func (f Filter) Tag() string { return f.tag }

// Highlight is the part of a bookmark a page is about to show, to pick which
// terms to highlight.
type Highlight struct {
	Part Field
	// Tag is the one tag on show when Part is Tag: a tag: term matches only a
	// tag equal to it.
	Tag string
}

// Highlights returns the text of the terms that matched in part, for the page
// to mark.
func (f Filter) Highlights(part Highlight) []string {
	texts := []string{}
	for _, term := range f.terms {
		if !term.Negated && highlights(term, part) {
			texts = append(texts, term.Text)
		}
	}
	return texts
}

func highlights(term Term, part Highlight) bool {
	if term.Field == Any {
		return true
	}
	if term.Field == Tag {
		return part.Part == Tag && strings.ToLower(strings.TrimSpace(term.Text)) == part.Tag
	}
	return term.Field == part.Part
}

func fieldFromPrefix(name string) (Field, bool) {
	switch strings.ToLower(name) {
	case "title":
		return Title, true
	case "url":
		return URL, true
	case "note":
		return Note, true
	case "tag":
		return Tag, true
	case "status":
		return Status, true
	}
	return Any, false
}

func (f Field) column() string {
	switch f {
	case Title:
		return "title"
	case URL:
		return "url"
	case Note:
		return "note"
	}
	return ""
}

// readField reads a title:-style prefix at *at and moves past the colon.
// Anything else (including https:) leaves *at alone and is plain text.
func readField(chars []rune, at *int) Field {
	nameLength := 0
	for *at+nameLength < len(chars) && unicode.IsLetter(chars[*at+nameLength]) {
		nameLength++
	}
	colon := *at + nameLength
	if colon >= len(chars) || chars[colon] != ':' {
		return Any
	}
	field, ok := fieldFromPrefix(string(chars[*at:colon]))
	if !ok {
		return Any
	}
	*at = colon + 1
	return field
}

// readValue reads a bare word or a quoted phrase at *at.
func readValue(chars []rune, at *int) string {
	var value []rune
	if *at >= len(chars) || chars[*at] != '"' {
		for *at < len(chars) && !unicode.IsSpace(chars[*at]) {
			value = append(value, chars[*at])
			*at++
		}
		return string(value)
	}
	*at++
	for *at < len(chars) {
		character := chars[*at]
		*at++
		if character == '"' {
			break
		}
		if character == '\\' && *at < len(chars) && chars[*at] == '"' {
			value = append(value, '"')
			*at++
			continue
		}
		value = append(value, character)
	}
	return string(value)
}

// Parse splits a query into terms.
func Parse(query string) []Term {
	chars := []rune(query)
	terms := []Term{}
	at := 0
	for at < len(chars) && len(terms) < maxTerms {
		if unicode.IsSpace(chars[at]) {
			at++
			continue
		}
		start := at
		negated := chars[at] == '-' && at+1 < len(chars) && !unicode.IsSpace(chars[at+1])
		if negated {
			at++
		}
		field := readField(chars, &at)
		value := readValue(chars, &at)
		term, ok := buildTerm(termParts{field: field, negated: negated, value: value, typed: string(chars[start:at])})
		if ok {
			terms = append(terms, term)
		}
	}
	return terms
}

type termParts struct {
	field   Field
	negated bool
	value   string
	// typed is everything the user wrote for this term, prefix included.
	typed string
}

func buildTerm(parts termParts) (Term, bool) {
	term := Term{Field: parts.field, Negated: parts.negated, Text: parts.value}
	if strings.TrimSpace(parts.value) == "" {
		if parts.field == Any {
			return Term{}, false
		}
		// A prefix with nothing (or only spaces) after it is what the user
		// typed, not a filter.
		term = Term{Field: Any, Text: parts.typed}
	}
	term.Text = truncateRunes(term.Text, maxTermChars)
	return term, true
}

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

// exactTag is the stored form of a tag, or a lowercase fallback for one that
// could never be stored (too long, control characters): it must match
// nothing, not everything. "" means no tag.
func exactTag(raw string) string {
	tag, err := bookmarks.NormalizeTag(raw)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(raw))
	}
	return tag
}

// containsPattern is %term% with the three characters that LIKE treats
// specially made literal.
func containsPattern(term string) string {
	var pattern strings.Builder
	pattern.WriteByte('%')
	for _, character := range term {
		if character == '\\' || character == '%' || character == '_' {
			pattern.WriteByte('\\')
		}
		pattern.WriteRune(character)
	}
	pattern.WriteByte('%')
	return pattern.String()
}

// SQL returns ` AND ...` conditions for a query that selects from bookmarks,
// with one `?` per argument.
func (f Filter) SQL() (conditions string, args []any) {
	var sql sqlBuilder
	for _, term := range f.terms {
		if term.Negated {
			sql.write(" AND NOT (")
		} else {
			sql.write(" AND (")
		}
		writeTermMatch(&sql, term)
		sql.write(")")
	}
	if f.tag != "" {
		sql.write(" AND ")
		writeTagEquals(&sql, f.tag)
	}
	if f.status != "" {
		sql.write(" AND ")
		writeStatusEquals(&sql, f.status)
	}
	return sql.text.String(), sql.args
}

type sqlBuilder struct {
	text strings.Builder
	args []any
}

func (b *sqlBuilder) write(text string) { b.text.WriteString(text) }

func (b *sqlBuilder) bind(value any) {
	b.text.WriteString("?")
	b.args = append(b.args, value)
}

func writeTermMatch(sql *sqlBuilder, term Term) {
	switch term.Field {
	case Tag:
		writeTagEquals(sql, exactTag(term.Text))
		return
	case Status:
		writeStatusEquals(sql, strings.ToLower(strings.TrimSpace(term.Text)))
		return
	}
	pattern := containsPattern(term.Text)
	columns := []string{"title", "url", "note"}
	if column := term.Field.column(); column != "" {
		columns = []string{column}
	}
	for index, column := range columns {
		if index > 0 {
			sql.write(" OR ")
		}
		sql.write("bookmarks." + column + " LIKE ")
		sql.bind(pattern)
		sql.write(` ESCAPE '\'`)
	}
	if term.Field == Any {
		sql.write(` OR EXISTS (SELECT 1 FROM bookmark_tags
                         WHERE bookmark_tags.bookmark_id = bookmarks.id AND bookmark_tags.tag LIKE `)
		sql.bind(pattern)
		sql.write(` ESCAPE '\')`)
	}
}

// writeStatusEquals: unchecked is a bookmark with no link check. COALESCE keeps
// -status:broken from dropping those rows: comparing NULL gives NULL, and NOT
// NULL is still not true.
func writeStatusEquals(sql *sqlBuilder, status string) {
	sql.write("COALESCE(bookmarks.link_status, 'unchecked') = ")
	sql.bind(status)
}

func writeTagEquals(sql *sqlBuilder, tag string) {
	sql.write(`EXISTS (SELECT 1 FROM bookmark_tags
                 WHERE bookmark_tags.bookmark_id = bookmarks.id AND bookmark_tags.tag = `)
	sql.bind(tag)
	sql.write(")")
}
