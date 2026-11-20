package netscape

import (
	"reflect"
	"testing"

	"github.com/henry40408/bkd/internal/bookmarks"
)

func only(t *testing.T, html string) Link {
	t.Helper()
	links := Parse(html)
	if len(links) != 1 {
		t.Fatalf("%q: got %d links", html, len(links))
	}
	return links[0]
}

func assertEqual(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func seconds(value int64) *int64 { return &value }

func TestAChromeStyleFileTurnsFoldersIntoTags(t *testing.T) {
	html := `<!DOCTYPE NETSCAPE-Bookmark-file-1>
<!-- This is an automatically generated file. -->
<META HTTP-EQUIV="Content-Type" CONTENT="text/html; charset=UTF-8">
<TITLE>Bookmarks</TITLE>
<H1>Bookmarks</H1>
<DL><p>
    <DT><H3 ADD_DATE="1" LAST_MODIFIED="2" PERSONAL_TOOLBAR_FOLDER="true">Bookmarks bar</H3>
    <DL><p>
        <DT><A HREF="https://a.test/" ADD_DATE="1700000000" ICON="data:image/png;base64,AAA=">A</A>
        <DT><H3>Dev</H3>
        <DL><p>
            <DT><A HREF="https://b.test/">B</A>
        </DL><p>
        <DT><A HREF="https://c.test/">C</A>
    </DL><p>
    <DT><A HREF="https://d.test/">D</A>
</DL><p>`
	links := Parse(html)
	type summary struct {
		URL  string
		Tags []string
	}
	var got []summary
	for _, link := range links {
		got = append(got, summary{link.URL, link.Tags})
	}
	assertEqual(t, got, []summary{
		{"https://a.test/", []string{"Bookmarks bar"}},
		{"https://b.test/", []string{"Bookmarks bar", "Dev"}},
		{"https://c.test/", []string{"Bookmarks bar"}},
		{"https://d.test/", []string{}},
	})
	assertEqual(t, links[0].Added, seconds(1_700_000_000))
	assertEqual(t, links[1].Added, (*int64)(nil))
}

func TestTheTagsAttributeFollowsTheFolderTags(t *testing.T) {
	html := `<DL><DT><H3>Reading</H3><DL>
            <DT><A HREF="https://a.test/" TAGS="rust,web dev,,">A</A></DL></DL>`
	assertEqual(t, only(t, html).Tags, []string{"Reading", "rust", "web dev"})
}

func TestADescriptionAfterAnItemBecomesItsNote(t *testing.T) {
	html := "<DL><DT><A HREF=\"https://a.test/\">A</A>\n<DD>first line<br>second\n" +
		"<DT><A HREF=\"https://b.test/\">B</A></DL>"
	links := Parse(html)
	assertEqual(t, links[0].Note, "first line\nsecond")
	assertEqual(t, links[1].Note, "")
}

func TestAFolderDescriptionIsNotANote(t *testing.T) {
	html := "<DL><DT><A HREF=\"https://a.test/\">A</A>\n" +
		"<DT><H3>F</H3>\n<DD>about the folder\n<DL><DT><A HREF=\"https://b.test/\">B</A></DL></DL>"
	links := Parse(html)
	assertEqual(t, links[0].Note, "")
	assertEqual(t, links[1].Note, "")
}

func TestEntitiesQuotesAndOddMarkupAreHandled(t *testing.T) {
	html := "<dt><a href='https://a.test/?x=1&amp;y=2' add_date=5 private>" +
		"Tom &amp; Jerry &#x4E2D;&#25991; &unknown; &</a>"
	link := only(t, html)
	assertEqual(t, link.URL, "https://a.test/?x=1&y=2")
	assertEqual(t, link.Title, "Tom & Jerry 中文 &unknown; &")
	assertEqual(t, link.Added, seconds(5))
}

func TestAGreaterThanInsideAnAttributeDoesNotEndTheTag(t *testing.T) {
	link := only(t, `<A HREF="https://a.test/?q=a>b">t</A>`)
	assertEqual(t, link.URL, "https://a.test/?q=a>b")
}

func TestAnApostropheInAnUnquotedValueDoesNotSwallowLaterLinks(t *testing.T) {
	links := Parse(`<A HREF=https://x.test/?q=bob's>one</A><DT><A HREF="https://y.test/">two</A>`)
	var urls []string
	for _, link := range links {
		urls = append(urls, link.URL)
	}
	assertEqual(t, urls, []string{"https://x.test/?q=bob's", "https://y.test/"})
}

func TestAPocketStyleDateIsRead(t *testing.T) {
	link := only(t, `<a href="https://a.test/" time_added="1700000000" tags="x,y">t</a>`)
	assertEqual(t, link.Added, seconds(1_700_000_000))
	assertEqual(t, link.Tags, []string{"x", "y"})
}

func TestAnUnreadableAddDateDoesNotFallBackToTimeAdded(t *testing.T) {
	link := only(t, `<a href="https://a.test/" add_date="soon" time_added="1700000000">t</a>`)
	assertEqual(t, link.Added, (*int64)(nil))
}

func TestGarbageAndTruncatedInputDoNotPanic(t *testing.T) {
	for _, input := range []string{"", "<", "<A", `<A HREF="x`, "<!--", "<!DOCTYPE", "</", "&", "&#;",
		"&#99999999999;", "&#xD800;", "&#+65;", "<DL><DL></DL></DL></DL>", "plain text 中文 <3", "<a =x>"} {
		Parse(input)
	}
}

func TestNumericEntityEdges(t *testing.T) {
	assertEqual(t, decodeEntities("&#+65;&#x41;&#X42;&#xD800;&#;&#x;"), "AAB&#xD800;&#;&#x;")
	assertEqual(t, decodeEntities("&amp;&lt;&gt;&quot;&apos;&nbsp;"), `&<>"' `)
	// The semicolon must come within 12 characters.
	assertEqual(t, decodeEntities("&aaaaaaaaaaaa;"), "&aaaaaaaaaaaa;")
}

func TestExportedBookmarksReadBackTheSame(t *testing.T) {
	bookmark := bookmarks.Bookmark{
		ID:        1,
		URL:       `https://a.test/?x=1&y="2"`,
		Title:     "<b>Tom</b> & Jerry",
		Note:      "line one\nline <two>",
		Tags:      []string{"a,b", "c"},
		CreatedAt: 1_700_000_000,
		UpdatedAt: 1_700_000_001,
	}
	links := Parse(Render([]bookmarks.Bookmark{bookmark}))
	assertEqual(t, links, []Link{{
		URL:   `https://a.test/?x=1&y="2"`,
		Title: "<b>Tom</b> & Jerry",
		Note:  "line one\nline <two>",
		Tags:  []string{"a b", "c"},
		Added: seconds(1_700_000_000),
	}})
}

func TestRenderWritesTheHeaderAndOmitsEmptyParts(t *testing.T) {
	got := Render([]bookmarks.Bookmark{{URL: "https://a.test/", Title: "A", CreatedAt: 5}})
	want := `<!DOCTYPE NETSCAPE-Bookmark-file-1>
<META HTTP-EQUIV="Content-Type" CONTENT="text/html; charset=UTF-8">
<TITLE>Bookmarks</TITLE>
<H1>Bookmarks</H1>
<DL><p>
    <DT><A HREF="https://a.test/" ADD_DATE="5">A</A>
</DL><p>
`
	assertEqual(t, got, want)
}
