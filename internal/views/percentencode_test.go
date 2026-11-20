package views

import (
	"math/rand"
	"net/url"
	"strings"
	"testing"
)

// referenceEncode is the hand-written encoder escapeValue replaced: everything but
// letters, digits and -_.~ becomes %XX per byte.
func referenceEncode(value string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
	return b.String()
}

// escapeValueInputs are every single byte, multi-byte UTF-8, invalid UTF-8
// and the shapes the call sites pass: tag names, back and next paths with
// their own query, and notices.
func escapeValueInputs() []string {
	var inputs []string
	for b := 0; b < 256; b++ {
		inputs = append(inputs, string([]byte{byte(b)}))
	}
	inputs = append(inputs,
		"", " ", "  ", "+", "a b+c", "é", "日本語", "a😀b", "é", "a\xffb", "\xf0\x9f",
		"~._-", "/", "/?tag=a b&status=x+y", "/bookmarks/3/edit?back=%2F", "100%", "%20", "%zz",
		"a/b c", "#hash", "tag:go & rust", "Your session has ended.", "x=1;y=2", "a,b", "'\"<>",
	)
	random := rand.New(rand.NewSource(1))
	for range 2000 {
		raw := make([]byte, random.Intn(12))
		for i := range raw {
			raw[i] = byte(random.Intn(256))
		}
		inputs = append(inputs, string(raw))
	}
	return inputs
}

func TestEscapeValueEqualsTheHandWrittenEncoder(t *testing.T) {
	for _, input := range escapeValueInputs() {
		want := referenceEncode(input)
		if got := escapeValue(input); got != want {
			t.Errorf("%q: got %q, want %q", input, got, want)
		}
	}
}

func TestPlainQueryEscapeDiffersOnlyForSpace(t *testing.T) {
	for _, input := range escapeValueInputs() {
		got := escapeValue(input)
		if query := url.QueryEscape(input); query != got && !strings.Contains(input, " ") {
			t.Errorf("QueryEscape differs without a space: %q: %q vs %q", input, query, got)
		}
		if strings.Contains(input, " ") && url.QueryEscape(input) == got {
			t.Errorf("QueryEscape equals percentEncode although %q holds a space", input)
		}
	}
	// PathEscape keeps these, percentEncode escapes them.
	for _, input := range []string{"$", "&", "+", "=", ":", "@"} {
		if url.PathEscape(input) == escapeValue(input) {
			t.Errorf("PathEscape(%q) unexpectedly equals percentEncode", input)
		}
	}
}
