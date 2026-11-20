package views

import (
	"strings"
	"testing"
)

func TestAMatchThatEndsExactlyAtTheEndOfTheTextIsMarked(t *testing.T) {
	if got := string(highlight("xab", []string{"ab"})); got != "x<mark>ab</mark>" {
		t.Fatalf("got %q", got)
	}
}

func TestPlainTextIsOneRunNotOnePerCharacter(t *testing.T) {
	got := splitMatches("abc", nil)
	if len(got) != 1 || got[0] != (run{matched: false, text: "abc"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestDomainEdgesAroundUserinfoAndPorts(t *testing.T) {
	cases := map[string]string{
		"http://@host/":     "host",
		"http://:80/":       "http://:80/",
		"http://h:0/":       "h",
		"http://h:9/":       "h",
		"http://h:09/":      "h",
		"http://h:a/":       "h:a",
		"http://h:./":       "h:.",
		"http://u:p@h:8080": "h",
		// url.Parse rejects these, but validateURL accepts them: the
		// credentials must still be dropped.
		"https://user:secret@example.com/50%off": "example.com",
		"https://example.com/50%off":             "example.com",
	}
	for input, want := range cases {
		if got := DomainOf(input); got != want {
			t.Errorf("DomainOf(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestADayIsTheFirstTimeThatNoLongerCountsInHours(t *testing.T) {
	const hour = 3_600
	cases := map[int64]string{
		hour - 1:       "just now",
		hour:           "1 hour ago",
		23 * hour:      "23 hours ago",
		24 * hour:      "yesterday",
		48 * hour:      "2 days ago",
		7 * 24 * hour:  "1 week ago",
		30 * 24 * hour: "1 month ago",
	}
	for age, want := range cases {
		if got := timeAgo(1_700_000_000+age, 1_700_000_000); got != want {
			t.Errorf("%d seconds: got %q, want %q", age, got, want)
		}
	}
}

func TestQueryValuesKeepOnlyUnreservedCharacters(t *testing.T) {
	got := WithQuery("/p", [][2]string{{"k", "azAZ09-_.~"}})
	if got != "/p?k=azAZ09-_.~" {
		t.Fatalf("unreserved characters were encoded: %q", got)
	}
	got = WithQuery("/p", [][2]string{{"k", "`{@[/:"}})
	if got != "/p?k=%60%7B%40%5B%2F%3A" {
		t.Fatalf("reserved characters were kept: %q", got)
	}
}

func TestTheImportSummaryMentionsInvalidLinksAndExamplesOnlyWhenThereAreSome(t *testing.T) {
	none := string(importResult(ImportReport{Added: 2, Duplicates: 1}))
	if strings.Contains(none, "ignored") || strings.Contains(none, "<ul>") {
		t.Fatalf("an empty report mentions what is not there: %s", none)
	}
	one := string(importResult(ImportReport{
		Added: 2, Invalid: 1, Examples: []InvalidExample{{URL: "ftp://x", Reason: "no"}},
	}))
	if !strings.Contains(one, "ignored 1") || !strings.Contains(one, "<ul>") {
		t.Fatalf("a report with one invalid link is missing it: %s", one)
	}
}
