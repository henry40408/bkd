package server

import (
	"strings"
	"testing"
)

func TestUserAgentRuneHandling(t *testing.T) {
	a199 := strings.Repeat("a", 199)
	cases := map[string]string{
		"":                                    "",
		"a\xffb":                              "a�b",
		"a\xff\xfeb":                          "a��b",
		"é":                                  "é",
		a199 + "\xff":                         a199 + "�",
		strings.Repeat("a", 200) + "\xff\xfe": strings.Repeat("a", 200),
		a199 + "\xff\xfe":                     a199 + "�",
		a199 + "é":                           a199 + "e",
	}
	for given, want := range cases {
		if got := (caller{userAgent: given}).sessionStart(1).UserAgent; got != want {
			t.Errorf("%q: got %q, want %q", given, got, want)
		}
	}
}
