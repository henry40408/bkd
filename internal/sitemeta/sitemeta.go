// Package sitemeta reads what a web page says about itself: its Open Graph
// tags, or its <title> and description when it has none. It only reads text it
// is given; fetching the page is the link checker's job, because that is where
// the rule that keeps the server away from private addresses lives.
package sitemeta

import (
	"io"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// Meta is the title and description of a page, each trimmed to one line.
// Either may be empty.
type Meta struct {
	Title       string
	Description string
}

// IsEmpty says the page told nothing.
func (m Meta) IsEmpty() bool { return m.Title == "" && m.Description == "" }

// found collects the first value of each source. Parse picks between them
// only at the end, because a page may list them in any order.
type found struct {
	ogTitle, pageTitle, ogDescription, metaDescription string
}

func (f found) meta() Meta {
	return Meta{
		Title:       firstNonEmpty(f.ogTitle, f.pageTitle),
		Description: firstNonEmpty(f.ogDescription, f.metaDescription),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// Parse reads the head of an HTML page. It stops at <body>, since the tags it
// wants are in the head, so a large page costs no more than its head.
func Parse(page io.Reader) Meta {
	var seen found
	tokens := html.NewTokenizer(page)
	for {
		switch tokens.Next() {
		case html.ErrorToken:
			return seen.meta()
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttributes := tokens.TagName()
			switch string(name) {
			case "body":
				return seen.meta()
			case "meta":
				if hasAttributes {
					seen.addMeta(metaTag(tokens))
				}
			case "title":
				seen.addTitle(tokens)
			}
		}
	}
}

// metaTag is the name (or property) and content of a <meta> tag.
func metaTag(tokens *html.Tokenizer) (key, content string) {
	for {
		attribute, value, more := tokens.TagAttr()
		switch string(attribute) {
		case "property", "name":
			if key == "" {
				key = strings.ToLower(strings.TrimSpace(string(value)))
			}
		case "content":
			content = clean(string(value))
		}
		if !more {
			return key, content
		}
	}
}

func (f *found) addMeta(key, content string) {
	if content == "" {
		return
	}
	switch key {
	case "og:title":
		f.ogTitle = firstNonEmpty(f.ogTitle, content)
	case "og:description":
		f.ogDescription = firstNonEmpty(f.ogDescription, content)
	case "description":
		f.metaDescription = firstNonEmpty(f.metaDescription, content)
	}
}

// addTitle reads the text of the <title> tag that was just opened.
func (f *found) addTitle(tokens *html.Tokenizer) {
	if f.pageTitle != "" || tokens.Next() != html.TextToken {
		return
	}
	f.pageTitle = clean(string(tokens.Text()))
}

// clean makes text fit on one line, so a page cannot put line breaks, control
// characters or text-direction overrides into the list.
func clean(text string) string {
	text = strings.ToValidUTF8(text, "�")
	text = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Bidi_Control, r) || (unicode.IsControl(r) && !unicode.IsSpace(r)) {
			return -1
		}
		return r
	}, text)
	return strings.Join(strings.Fields(text), " ")
}
