package search

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/henry40408/bkd/internal/bookmarks"
	"github.com/henry40408/bkd/internal/dbtest"
)

var bg = context.Background()

type fixture struct {
	t     *testing.T
	pool  *sql.DB
	store *bookmarks.Store
	user  int64
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool := dbtest.Open(t)
	return fixture{t: t, pool: pool, store: bookmarks.NewStore(pool), user: dbtest.InsertUser(t, pool, "henry")}
}

func (f fixture) add(title, url, note string, tags ...string) {
	f.addFor(f.user, title, url, note, tags...)
}

func (f fixture) addFor(user int64, title, url, note string, tags ...string) {
	f.t.Helper()
	valid, err := bookmarks.Input{URL: url, Title: title, Note: note, Tags: tags}.Validate()
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.store.Create(bg, user, valid, 0); err != nil {
		f.t.Fatal(err)
	}
}

func (f fixture) titlesFor(user int64, filter Filter) []string {
	f.t.Helper()
	list, err := f.store.List(bg, user, filter, bookmarks.NewPage(nil, nil))
	if err != nil {
		f.t.Fatal(err)
	}
	titles := []string{}
	for _, bookmark := range list {
		titles = append(titles, bookmark.Title)
	}
	slices.Sort(titles)
	return titles
}

func (f fixture) titles(query, tag string) []string {
	f.t.Helper()
	return f.titlesFor(f.user, NewFilter(query, tag))
}

func assertTitles(t *testing.T, got []string, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSpecialCharactersAreMadeLiteral(t *testing.T) {
	if got := containsPattern(`100%_\`); got != `%100\%\_\\%` {
		t.Fatal(got)
	}
}

type wantTerm struct {
	field   string
	text    string
	negated bool
}

// The cases are what the Rust parser answered. They are never regenerated from
// Go output.
func TestParseAgreesWithTheRustParser(t *testing.T) {
	tests := []struct {
		query string
		terms []wantTerm
	}{
		{"", []wantTerm{}},
		{"   \t ", []wantTerm{}},
		{"  a   b\tc ", []wantTerm{{"any", "a", false}, {"any", "b", false}, {"any", "c", false}}},
		{"\"two words\" x", []wantTerm{{"any", "two words", false}, {"any", "x", false}}},
		{"\"say \\\"hi\\\"\"", []wantTerm{{"any", "say \"hi\"", false}}},
		{"\"never closed here", []wantTerm{{"any", "never closed here", false}}},
		{"\"\"", []wantTerm{}},
		{"-spam", []wantTerm{{"any", "spam", true}}},
		{"-\"big spam\"", []wantTerm{{"any", "big spam", true}}},
		{"-", []wantTerm{{"any", "-", false}}},
		{"a - b", []wantTerm{{"any", "a", false}, {"any", "-", false}, {"any", "b", false}}},
		{"well-known", []wantTerm{{"any", "well-known", false}}},
		{"tag:rust", []wantTerm{{"tag", "rust", false}}},
		{"TAG:Rust", []wantTerm{{"tag", "Rust", false}}},
		{"tag:\"to read\"", []wantTerm{{"tag", "to read", false}}},
		{"-tag:old", []wantTerm{{"tag", "old", true}}},
		{"title:zebra url:a.test note:\"two words\"", []wantTerm{{"title", "zebra", false}, {"url", "a.test", false}, {"note", "two words", false}}},
		{"tag:", []wantTerm{{"any", "tag:", false}}},
		{"-tag:", []wantTerm{{"any", "-tag:", false}}},
		{"tag:\"\"", []wantTerm{{"any", "tag:\"\"", false}}},
		{"tag:\" \"", []wantTerm{{"any", "tag:\" \"", false}}},
		{"-tag:\" \"", []wantTerm{{"any", "-tag:\" \"", false}}},
		{"\" \"", []wantTerm{}},
		{"https://x.test", []wantTerm{{"any", "https://x.test", false}}},
		{"foo:bar", []wantTerm{{"any", "foo:bar", false}}},
		{"title:a:b", []wantTerm{{"title", "a:b", false}}},
		{"it's", []wantTerm{{"any", "it's", false}}},
		{"學習 tag:\"教學 筆記\"", []wantTerm{{"any", "學習", false}, {"tag", "教學 筆記", false}}},
		{"\"a\"b", []wantTerm{{"any", "a", false}, {"any", "b", false}}},
		{"status:broken", []wantTerm{{"status", "broken", false}}},
		{"-STATUS:Unchecked x", []wantTerm{{"status", "Unchecked", true}, {"any", "x", false}}},
		{"status:", []wantTerm{{"any", "status:", false}}},
	}
	for _, tt := range tests {
		got := []wantTerm{}
		for _, term := range Parse(tt.query) {
			got = append(got, wantTerm{term.Field.String(), term.Text, term.Negated})
		}
		if !reflect.DeepEqual(got, tt.terms) {
			t.Errorf("query %q: got %v, want %v", tt.query, got, tt.terms)
		}
	}
}

func TestTermsAndTheirLengthAreLimited(t *testing.T) {
	var many []string
	for n := range 20 {
		many = append(many, fmt.Sprint(n))
	}
	if got := len(Parse(strings.Join(many, " "))); got != maxTerms {
		t.Fatal(got)
	}
	long := strings.Repeat("x", 500)
	if got := len(Parse(long)[0].Text); got != maxTermChars {
		t.Fatal(got)
	}
	if got := len(Parse(`title:"` + long + `"`)[0].Text); got != maxTermChars {
		t.Fatal(got)
	}
}

func TestTheTagFilterIsNormalized(t *testing.T) {
	if got := NewFilter("", "  Rust ").Tag(); got != "rust" {
		t.Fatal(got)
	}
	if got := NewFilter("", "   ").Tag(); got != "" {
		t.Fatal(got)
	}
}

func TestHighlightsPickTheTermsThatMatchedThePart(t *testing.T) {
	filter := NewFilter(`any title:t url:u note:n tag:" Rust " -gone status:ok`, "")
	assertTitles(t, filter.Highlights(Highlight{Part: Title}), "any", "t")
	assertTitles(t, filter.Highlights(Highlight{Part: URL}), "any", "u")
	assertTitles(t, filter.Highlights(Highlight{Part: Note}), "any", "n")
	assertTitles(t, filter.Highlights(Highlight{Part: Tag, Tag: "rust"}), "any", " Rust ")
	assertTitles(t, filter.Highlights(Highlight{Part: Tag, Tag: "go"}), "any")
}

func TestTermsMatchTitleURLNoteAndTags(t *testing.T) {
	f := newFixture(t)
	f.add("by title zebra", "https://a.test/1", "")
	f.add("by url", "https://zebra.test/2", "")
	f.add("by note", "https://a.test/3", "a zebra here")
	f.add("by tag", "https://a.test/4", "", "zebra")
	f.add("unrelated", "https://a.test/5", "", "horse")

	assertTitles(t, f.titles("zebra", ""), "by note", "by tag", "by title zebra", "by url")
}

func TestEveryTermMustMatchButInAnyField(t *testing.T) {
	f := newFixture(t)
	f.add("rust async", "https://a.test/1", "", "book")
	f.add("rust only", "https://a.test/2", "")
	f.add("book only", "https://a.test/3", "", "book")

	assertTitles(t, f.titles("rust book", ""), "rust async")
}

func TestChineseTextMatchesWithoutWordBreaks(t *testing.T) {
	f := newFixture(t)
	f.add("學習程式設計", "https://a.test/1", "", "教學")
	f.add("烹飪", "https://a.test/2", "")

	assertTitles(t, f.titles("程式", ""), "學習程式設計")
	assertTitles(t, f.titles("教學", ""), "學習程式設計")
}

func TestASCIICaseIsIgnored(t *testing.T) {
	f := newFixture(t)
	f.add("Rust Book", "https://a.test/1", "")
	assertTitles(t, f.titles("rUsT", ""), "Rust Book")
}

func TestLikeWildcardsInTheQueryAreLiteral(t *testing.T) {
	f := newFixture(t)
	f.add("100% done", "https://a.test/1", "")
	f.add("1000 done", "https://a.test/2", "")
	f.add("snake_case", "https://a.test/3", "")
	f.add("snakeXcase", "https://a.test/4", "")
	f.add(`back\slash`, "https://a.test/5", "")

	assertTitles(t, f.titles("100%", ""), "100% done")
	assertTitles(t, f.titles("snake_case", ""), "snake_case")
	assertTitles(t, f.titles(`\`, ""), `back\slash`)
	assertTitles(t, f.titles("%", ""), "100% done")
}

func TestQuotesInTheQueryCannotChangeTheSQL(t *testing.T) {
	f := newFixture(t)
	f.add("it's fine", "https://a.test/1", "")
	assertTitles(t, f.titles("it's", ""), "it's fine")
	assertTitles(t, f.titles("' OR 1=1 --", ""))
}

func TestPhrasesMatchAsOneTerm(t *testing.T) {
	f := newFixture(t)
	f.add("the quick fox", "https://a.test/1", "")
	f.add("quick the fox", "https://a.test/2", "")

	assertTitles(t, f.titles(`"quick fox"`, ""), "the quick fox")
	if got := len(f.titles("quick fox", "")); got != 2 {
		t.Fatal(got)
	}
}

func TestExcludedTermsRemoveMatchesFromEveryField(t *testing.T) {
	f := newFixture(t)
	f.add("rust a", "https://a.test/1", "")
	f.add("rust b", "https://a.test/2", "old news")
	f.add("rust c", "https://a.test/3", "", "old")
	f.add("rust d", "https://old.test/4", "")

	assertTitles(t, f.titles("rust -old", ""), "rust a")
	assertTitles(t, f.titles(`-"old news" rust`, ""), "rust a", "rust c", "rust d")
	// Only an exclusion: everything that does not match.
	assertTitles(t, f.titles("-rust", ""))
}

func TestStatusFindsBookmarksByTheirLinkCheck(t *testing.T) {
	f := newFixture(t)
	for _, c := range []struct {
		title  string
		status any
	}{{"fine", "ok"}, {"dead", "broken"}, {"fresh", nil}} {
		f.add(c.title, "https://a.test/"+c.title, "")
		if _, err := f.pool.Exec("UPDATE bookmarks SET link_status = ?1 WHERE title = ?2", c.status, c.title); err != nil {
			t.Fatal(err)
		}
	}

	assertTitles(t, f.titles("status:broken", ""), "dead")
	assertTitles(t, f.titles("STATUS:OK", ""), "fine")
	assertTitles(t, f.titles("status:unchecked", ""), "fresh")
	// A bookmark that was never checked is not "not broken" by accident of NULL.
	assertTitles(t, f.titles("-status:broken", ""), "fine", "fresh")
	assertTitles(t, f.titles("status:nonsense", ""))

	only := func(status, query string) []string {
		return f.titlesFor(f.user, NewFilter(query, "").WithStatus(status))
	}
	assertTitles(t, only("broken", ""), "dead")
	assertTitles(t, only(" Unchecked ", ""), "fresh")
	assertTitles(t, only("ok", "dead"))
	assertTitles(t, only("", ""), "dead", "fine", "fresh")
	assertTitles(t, only("nonsense", ""))
}

func TestFieldPrefixesSearchOnlyThatField(t *testing.T) {
	f := newFixture(t)
	f.add("zebra", "https://a.test/1", "")
	f.add("by url", "https://zebra.test/2", "")
	f.add("by note", "https://a.test/3", "zebra")
	f.add("by tag", "https://a.test/4", "", "zebra")

	assertTitles(t, f.titles("title:zebra", ""), "zebra")
	assertTitles(t, f.titles("url:zebra", ""), "by url")
	assertTitles(t, f.titles("note:zebra", ""), "by note")
	assertTitles(t, f.titles("tag:zebra", ""), "by tag")
	assertTitles(t, f.titles("zebra -title:zebra", ""), "by note", "by tag", "by url")
}

func TestTheTagPrefixIsExactCaseInsensitiveAndMayContainSpaces(t *testing.T) {
	f := newFixture(t)
	f.add("exact", "https://a.test/1", "", "rust")
	f.add("longer", "https://a.test/2", "", "rustacean")
	f.add("spaced", "https://a.test/3", "", "to read")

	assertTitles(t, f.titles("tag:Rust", ""), "exact")
	assertTitles(t, f.titles(`tag:"to read"`, ""), "spaced")
	assertTitles(t, f.titles("-tag:rust -tag:rustacean", ""), "spaced")
	assertTitles(t, f.titles("tag:"+strings.Repeat("x", 200), ""))
}

func TestAnEmptyPrefixAndURLsArePlainText(t *testing.T) {
	f := newFixture(t)
	f.add("has https", "https://x.test/1", "")
	f.add("tag: colon", "https://a.test/2", "")

	assertTitles(t, f.titles("https://x.test", ""), "has https")
	assertTitles(t, f.titles("tag:", ""), "tag: colon")
}

func TestTheTagFilterIsExactAndCombinesWithTerms(t *testing.T) {
	f := newFixture(t)
	f.add("rust guide", "https://a.test/1", "", "rust")
	f.add("go guide", "https://a.test/2", "", "rustacean")
	f.add("cooking", "https://a.test/3", "", "rust")

	assertTitles(t, f.titles("", "Rust"), "cooking", "rust guide")
	assertTitles(t, f.titles("guide", "rust"), "rust guide")
}

func TestAnImpossibleTagMatchesNothing(t *testing.T) {
	f := newFixture(t)
	f.add("anything", "https://a.test/1", "", "rust")
	assertTitles(t, f.titles("", strings.Repeat("x", 200)))
	assertTitles(t, f.titles("", "a\x1fb"))
}

func TestSearchNeverReturnsAnotherUsersBookmarks(t *testing.T) {
	f := newFixture(t)
	mei := dbtest.InsertUser(t, f.pool, "mei")
	f.addFor(mei, "shared word", "https://a.test/1", "", "shared")

	assertTitles(t, f.titles("shared", ""))
	assertTitles(t, f.titles("", "shared"))
	assertTitles(t, f.titles("", ""))
}
