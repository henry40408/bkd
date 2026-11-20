package truncate

import "testing"

func TestRunes(t *testing.T) {
	cases := []struct {
		name, text string
		limit      int
		want       string
	}{
		{"empty", "", 3, ""},
		{"empty zero limit", "", 0, ""},
		{"short", "abc", 5, "abc"},
		{"exact", "abc", 3, "abc"},
		{"cut", "abcd", 3, "abc"},
		{"zero limit", "abc", 0, ""},
		{"multibyte counts runes", "héllo", 2, "hé"},
		{"multibyte exact", "héllo", 5, "héllo"},
		{"four-byte rune", "a😀b", 2, "a😀"},
		{"combining mark split", "éx", 1, "e"},
		{"combining mark kept", "éx", 2, "é"},
		{"invalid short unchanged", "a\xffb", 5, "a\xffb"},
		{"invalid exact unchanged", "a\xffb", 3, "a\xffb"},
		{"invalid cut becomes U+FFFD", "a\xff\xfeb", 2, "a�"},
	}
	for _, c := range cases {
		if got := Runes(c.text, c.limit); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestNegativeLimitPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("a negative limit on a non-empty text no longer panics")
		}
	}()
	_ = Runes("abc", -1)
}
