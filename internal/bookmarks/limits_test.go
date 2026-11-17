package bookmarks

import (
	"fmt"
	"strings"
	"testing"
)

func tagList(count int) []string {
	tags := make([]string, count)
	for index := range tags {
		tags[index] = fmt.Sprintf("tag%d", index)
	}
	return tags
}

func TestValidateAcceptsExactlyTheLimitsAndRefusesOneMore(t *testing.T) {
	longURL := func(chars int) string { return "https://a.test/" + strings.Repeat("x", chars-len("https://a.test/")) }
	cases := []struct {
		name  string
		input Input
		valid bool
	}{
		{"url at the limit", Input{URL: longURL(maxURLChars), Title: "t"}, true},
		{"url over the limit", Input{URL: longURL(maxURLChars + 1), Title: "t"}, false},
		{"title at the limit", Input{URL: "https://a.test/", Title: strings.Repeat("t", maxTitleChars)}, true},
		{"title over the limit", Input{URL: "https://a.test/", Title: strings.Repeat("t", maxTitleChars+1)}, false},
		{"note at the limit", Input{URL: "https://a.test/", Title: "t", Note: strings.Repeat("n", maxNoteChars)}, true},
		{"note over the limit", Input{URL: "https://a.test/", Title: "t", Note: strings.Repeat("n", maxNoteChars+1)}, false},
		{"tag at the limit", Input{URL: "https://a.test/", Title: "t", Tags: []string{strings.Repeat("g", maxTagChars)}}, true},
		{"tag over the limit", Input{URL: "https://a.test/", Title: "t", Tags: []string{strings.Repeat("g", maxTagChars+1)}}, false},
		{"tags at the limit", Input{URL: "https://a.test/", Title: "t", Tags: tagList(MaxTagsPerBookmark)}, true},
		{"tags over the limit", Input{URL: "https://a.test/", Title: "t", Tags: tagList(MaxTagsPerBookmark + 1)}, false},
		{"url with nothing after the scheme", Input{URL: "https://", Title: "t"}, false},
		{"url with one character after the scheme", Input{URL: "http://a", Title: "t"}, true},
		{"scheme in capitals", Input{URL: "HTTPS://A.TEST/", Title: "t"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := c.input.Validate()
			if (err == nil) != c.valid {
				t.Fatalf("valid = %v, err = %v", c.valid, err)
			}
		})
	}
}

func TestFittedKeepsExactlyTheLimitsAndCutsTheRest(t *testing.T) {
	atLimit := Fitted("https://a.test/", strings.Repeat("t", maxTitleChars), strings.Repeat("n", maxNoteChars), tagList(MaxTagsPerBookmark))
	if len([]rune(atLimit.Title)) != maxTitleChars || len([]rune(atLimit.Note)) != maxNoteChars || len(atLimit.Tags) != MaxTagsPerBookmark {
		t.Fatalf("something at the limit was cut: %d %d %d",
			len([]rune(atLimit.Title)), len([]rune(atLimit.Note)), len(atLimit.Tags))
	}
	over := Fitted("https://a.test/", strings.Repeat("t", maxTitleChars+1), strings.Repeat("n", maxNoteChars+1), tagList(MaxTagsPerBookmark+1))
	if len([]rune(over.Title)) != maxTitleChars || len([]rune(over.Note)) != maxNoteChars || len(over.Tags) != MaxTagsPerBookmark {
		t.Fatalf("something over the limit was kept: %d %d %d",
			len([]rune(over.Title)), len([]rune(over.Note)), len(over.Tags))
	}
}

func TestFittedKeepsAMultibyteTitleWholeAtTheLimit(t *testing.T) {
	title := strings.Repeat("書", maxTitleChars)
	if got := Fitted("https://a.test/", title, "", nil).Title; got != title {
		t.Fatal("a title of exactly the limit in characters was cut")
	}
}

func TestAsciiLowerFoldsOnlyCapitalLetters(t *testing.T) {
	if got := asciiLower("AZ@[az`{É"); got != "az@[az`{É" {
		t.Fatalf("got %q", got)
	}
}
