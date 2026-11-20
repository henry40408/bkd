package views

import "testing"

type highlightCase struct {
	name  string
	text  string
	terms []string
	want  string
}

func runHighlightCases(t *testing.T, cases []highlightCase) {
	t.Helper()
	for _, c := range cases {
		if got := string(highlight(c.text, c.terms)); got != c.want {
			t.Errorf("%s: highlight(%q, %q) = %q, want %q", c.name, c.text, c.terms, got, c.want)
		}
	}
}

func TestHighlightOrdinaryText(t *testing.T) {
	runHighlightCases(t, []highlightCase{
		{"ascii any case", "Hello World hello", []string{"HELLO"}, "<mark>Hello</mark> World <mark>hello</mark>"},
		{"latin accents", "Café CAFÉ cafe", []string{"café"}, "<mark>Café</mark> <mark>CAFÉ</mark> cafe"},
		{"cjk", "學習語言", []string{"習語"}, "學<mark>習語</mark>言"},
		{"multi-byte runes", "日本語日本語", []string{"本語"}, "日<mark>本語</mark>日<mark>本語</mark>"},
		{"emoji", "é🙂é", []string{"🙂"}, "é<mark>🙂</mark>é"},
		{"match at start", "abx", []string{"ab"}, "<mark>ab</mark>x"},
		{"match at end", "xab", []string{"ab"}, "x<mark>ab</mark>"},
		{"match at both ends", "abxab", []string{"ab"}, "<mark>ab</mark>x<mark>ab</mark>"},
		{"whole text", "ab", []string{"ab"}, "<mark>ab</mark>"},
		{"term longer than text", "ab", []string{"abc"}, "ab"},
		{"no match", "abc", []string{"x"}, "abc"},
		{"empty term", "abc", []string{""}, "abc"},
		{"no terms", "abc", nil, "abc"},
		{"empty text", "", []string{"a"}, ""},
		{"empty term next to a real one", "abc", []string{"", "b"}, "a<mark>b</mark>c"},
		{"adjacent matches of one term share a mark", "aaaa", []string{"aa"}, "<mark>aaaa</mark>"},
		{"repeated word", "abcabc", []string{"abc"}, "<mark>abcabc</mark>"},
		{"adjacent terms share a mark", "abcd", []string{"ab", "cd"}, "<mark>abcd</mark>"},
		{"overlapping terms: first one wins", "abcd", []string{"ab", "bc"}, "<mark>ab</mark>cd"},
		{"overlapping terms: earlier start wins", "abcd", []string{"bc", "ab"}, "<mark>ab</mark>cd"},
		{"overlapping terms: chained", "abcd", []string{"abc", "bcd"}, "<mark>abc</mark>d"},
		{"longest term at a position", "abcd", []string{"ab", "abc"}, "<mark>abc</mark>d"},
		{"longest term, other order", "abcd", []string{"abc", "ab"}, "<mark>abc</mark>d"},
		{"unmatched text is escaped", "a<b>&c", []string{"b"}, "a&lt;<mark>b</mark>&gt;&amp;c"},
		{"matched text is escaped", "a<b>", []string{"<b>"}, "a<mark>&lt;b&gt;</mark>"},
	})
}

func TestHighlightTreatsRegexpMetacharactersAsText(t *testing.T) {
	runHighlightCases(t, []highlightCase{
		{"dot", "a.b axb", []string{"."}, "a<mark>.</mark>b axb"},
		{"star", "a*b", []string{"*"}, "a<mark>*</mark>b"},
		{"open paren", "f(x)", []string{"("}, "f<mark>(</mark>x)"},
		{"open bracket", "a[0]", []string{"["}, "a<mark>[</mark>0]"},
		{"backslash", `a\b`, []string{`\`}, `a<mark>\</mark>b`},
		{"plus", "1+1", []string{"+"}, "1<mark>+</mark>1"},
		{"question", "why?", []string{"?"}, "why<mark>?</mark>"},
		{"caret", "a^b", []string{"^"}, "a<mark>^</mark>b"},
		{"dollar", "a$b", []string{"$"}, "a<mark>$</mark>b"},
		{"pipe", "a|b", []string{"|"}, "a<mark>|</mark>b"},
		{"pipe does not mean or", "ab", []string{"a|b"}, "ab"},
		{"dot star", "abc", []string{".*"}, "abc"},
		{"bracket class", "abc", []string{"[a-c]"}, "abc"},
		{"anchors", "abc", []string{"^abc$"}, "abc"},
		{"literal anchors", "^abc$", []string{"^abc$"}, "<mark>^abc$</mark>"},
		{"escape sequence", `a\d1`, []string{`\d`}, `a<mark>\d</mark>1`},
		{"case folded term", "A.B", []string{"a.b"}, "<mark>A.B</mark>"},
	})
}

// Special case folding: unicode.ToLower maps one rune to one rune, so these
// follow that mapping rather than full Unicode case folding.
func TestHighlightSpecialCaseFolding(t *testing.T) {
	runHighlightCases(t, []highlightCase{
		{"capital sigma against final sigma", "ΟΔΟΣ", []string{"οδος"}, "ΟΔΟΣ"},
		{"capital sigma against sigma", "ΟΔΟΣ", []string{"οδοσ"}, "<mark>ΟΔΟΣ</mark>"},
		{"final sigma against sigma", "οδος", []string{"οδοσ"}, "οδος"},
		{"final sigma against itself", "οδος", []string{"οδος"}, "<mark>οδος</mark>"},
		{"long s against s", "ſ", []string{"s"}, "ſ"},
		{"s against long s", "s", []string{"ſ"}, "s"},
		{"kelvin sign against k", "K", []string{"k"}, "<mark>K</mark>"},
		{"k against kelvin sign", "k", []string{"K"}, "<mark>k</mark>"},
		{"dotted capital i against i", "İ", []string{"i"}, "<mark>İ</mark>"},
		{"i against dotted capital i", "i", []string{"İ"}, "<mark>i</mark>"},
	})
}
