// Package truncate cuts text to a number of characters.
package truncate

import "unicode/utf8"

// Runes is the first limit characters (runes, not bytes) of text. A text
// that fits is returned byte for byte, invalid UTF-8 included; a cut one has
// each invalid byte turned into U+FFFD. It does not look at combining marks, so
// the cut can separate a mark from its base. limit must not be negative.
func Runes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit])
}
