// Package netscape reads and writes the Netscape bookmark file: the HTML that
// Chrome, Firefox, Safari, Pocket and Raindrop all export. The parser is a
// tolerant tokenizer, not an HTML parser: the format is a fixed handful of
// tags and real files are often not well formed.
package netscape

import (
	"html"
	"strings"
	"unicode"
	"unicode/utf8"
)

type tokenKind int

const (
	startTag tokenKind = iota
	endTag
	textToken
)

type attribute struct {
	name  string
	value string
}

type token struct {
	kind       tokenKind
	name       string // lowercase tag name for startTag and endTag
	attributes []attribute
	text       string
}

func tokenize(source string) []token {
	var tokens []token
	rest := source
	for rest != "" {
		open := strings.IndexByte(rest, '<')
		if open < 0 {
			tokens = append(tokens, token{kind: textToken, text: html.UnescapeString(rest)})
			break
		}
		if open > 0 {
			tokens = append(tokens, token{kind: textToken, text: html.UnescapeString(rest[:open])})
		}
		rest = rest[open:]
		markup, consumed := readMarkup(rest)
		if markup != nil {
			tokens = append(tokens, *markup)
		}
		rest = rest[consumed:]
	}
	return tokens
}

// readMarkup reads one `<...>` at the start of input. It returns the token
// (nil for comments and the doctype) and how many bytes it used.
func readMarkup(input string) (*token, int) {
	if comment, ok := strings.CutPrefix(input, "<!--"); ok {
		length := len(comment)
		if end := strings.Index(comment, "-->"); end >= 0 {
			length = end + 3
		}
		return nil, 4 + length
	}
	first, _ := utf8.DecodeRuneInString(input[1:])
	if first == '!' || first == '?' {
		if end := strings.IndexByte(input, '>'); end >= 0 {
			return nil, end + 1
		}
		return nil, len(input)
	}
	if !isTagStart(first) {
		return &token{kind: textToken, text: "<"}, 1
	}
	end := tagEnd(input)
	markup := parseTag(input[1:end])
	return &markup, min(end+1, len(input))
}

// isTagStart is false for the empty input too, where first is RuneError.
func isTagStart(first rune) bool {
	return first == '/' || (first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z')
}

// tagEnd is the index of the `>` that closes the tag, ignoring any inside a
// quoted attribute value. A quote opens a value only right after `=`, so the
// apostrophe in an unquoted `HREF=x?q=bob's` does not swallow the links that
// follow.
func tagEnd(input string) int {
	var quote rune
	previous := ' '
	for index, c := range input {
		if index == 0 {
			continue
		}
		switch {
		case quote == 0 && c == '>':
			return index
		case quote == 0 && (c == '"' || c == '\'') && previous == '=':
			quote = c
		case quote == c:
			quote = 0
		}
		if quote == 0 && !unicode.IsSpace(c) {
			previous = c
		}
	}
	return len(input)
}

func parseTag(inner string) token {
	if name, ok := strings.CutPrefix(inner, "/"); ok {
		return token{kind: endTag, name: asciiLower(strings.TrimSpace(name))}
	}
	nameEnd := strings.IndexFunc(inner, func(c rune) bool { return unicode.IsSpace(c) || c == '/' })
	if nameEnd < 0 {
		nameEnd = len(inner)
	}
	return token{kind: startTag, name: asciiLower(inner[:nameEnd]), attributes: parseAttributes(inner[nameEnd:])}
}

func parseAttributes(input string) []attribute {
	var attributes []attribute
	for {
		input = strings.TrimLeftFunc(input, func(c rune) bool { return unicode.IsSpace(c) || c == '/' })
		if input == "" {
			return attributes
		}
		nameEnd := strings.IndexFunc(input, func(c rune) bool { return unicode.IsSpace(c) || c == '=' || c == '/' })
		if nameEnd < 0 {
			nameEnd = len(input)
		}
		name := asciiLower(input[:nameEnd])
		input = strings.TrimLeftFunc(input[nameEnd:], unicode.IsSpace)
		afterEquals, ok := strings.CutPrefix(input, "=")
		if !ok {
			attributes = append(attributes, attribute{name: name})
			continue
		}
		value, remaining := readAttributeValue(strings.TrimLeftFunc(afterEquals, unicode.IsSpace))
		attributes = append(attributes, attribute{name: name, value: html.UnescapeString(value)})
		input = remaining
	}
}

func readAttributeValue(input string) (value, remaining string) {
	if input == "" || (input[0] != '"' && input[0] != '\'') {
		end := strings.IndexFunc(input, unicode.IsSpace)
		if end < 0 {
			end = len(input)
		}
		return input[:end], input[end:]
	}
	body := input[1:]
	end := strings.IndexByte(body, input[0])
	if end < 0 {
		return body, ""
	}
	return body[:end], body[end+1:]
}

func asciiLower(text string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, text)
}
