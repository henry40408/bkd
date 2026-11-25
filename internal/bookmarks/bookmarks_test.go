package bookmarks

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/henry40408/bkd/internal/dbtest"
)

var bg = context.Background()

func input(url, title string) Input { return Input{URL: url, Title: title} }

// valid is a bookmark with the given title and tags and a URL built from the title.
func valid(t *testing.T, title string, tags ...string) Valid {
	t.Helper()
	bookmark, err := Input{
		URL:   "https://example.com/" + strings.ReplaceAll(title, " ", "-"),
		Title: title,
		Tags:  tags,
	}.Validate()
	if err != nil {
		t.Fatal(err)
	}
	return bookmark
}

func newStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	pool := dbtest.Open(t)
	return NewStore(pool), pool
}

func create(t *testing.T, store *Store, user int64, bookmark Valid, now int64) Bookmark {
	t.Helper()
	created, err := store.Create(bg, user, bookmark, now)
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func listTitles(t *testing.T, store *Store, user int64) []string {
	t.Helper()
	list, err := store.List(bg, user, nil, NewPage(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	titles := []string{}
	for _, bookmark := range list {
		titles = append(titles, bookmark.Title)
	}
	return titles
}

func assertEqual(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestOnlyWebURLsAreAccepted(t *testing.T) {
	for _, good := range []string{"https://example.com", "HTTP://Example.com/a?b=c#d", "  https://a.b  "} {
		if _, err := input(good, "t").Validate(); err != nil {
			t.Errorf("%q: %v", good, err)
		}
	}
	for _, bad := range []string{"", "example.com", "javascript:alert(1)", "data:text/html,x",
		"ftp://example.com", "https://", "https://exa mple.com", "https://a.com/\nb"} {
		if _, err := input(bad, "t").Validate(); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestTheURLIsTrimmedAndTheTitleIsRequired(t *testing.T) {
	ok, err := input("  https://a.b  ", "  Title  ").Validate()
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, []string{ok.URL, ok.Title}, []string{"https://a.b", "Title"})
	if _, err := input("https://a.b", "   ").Validate(); err == nil {
		t.Fatal("blank title accepted")
	}
}

func TestLengthsAreLimited(t *testing.T) {
	longURL := "https://a.b/" + strings.Repeat("x", maxURLChars)
	if _, err := input(longURL, "t").Validate(); err == nil {
		t.Error("long url accepted")
	}
	if _, err := input("https://a.b", strings.Repeat("t", maxTitleChars+1)).Validate(); err == nil {
		t.Error("long title accepted")
	}
	longNote := Input{URL: "https://a.b", Title: "t", Note: strings.Repeat("n", maxNoteChars+1)}
	if _, err := longNote.Validate(); err == nil {
		t.Error("long note accepted")
	}
}

func TestValidationMessages(t *testing.T) {
	cases := map[string]Input{
		"url is too long":                         input("https://a.b/"+strings.Repeat("x", maxURLChars), "t"),
		"url must start with http:// or https://": input("ftp://a", "t"),
		"url is not valid":                        input("https://", "t"),
		"title is required":                       input("https://a.b", " "),
		"title is too long":                       input("https://a.b", strings.Repeat("t", maxTitleChars+1)),
		"note is too long":                        {URL: "https://a.b", Title: "t", Note: strings.Repeat("n", maxNoteChars+1)},
		"tag is too long":                         {URL: "https://a.b", Title: "t", Tags: []string{strings.Repeat("x", maxTagChars+1)}},
		"tag contains a control character":        {URL: "https://a.b", Title: "t", Tags: []string{"a\x1fb"}},
	}
	for want, in := range cases {
		_, err := in.Validate()
		if err == nil || err.Error() != want {
			t.Errorf("want %q, got %v", want, err)
		}
	}
}

func TestTitleFromURLIsTheTrimmedAddressCutToATitle(t *testing.T) {
	assertEqual(t, TitleFromURL("  https://a.b/x  "), "https://a.b/x")
	exact := "https://a.b/" + strings.Repeat("x", maxTitleChars-len("https://a.b/"))
	assertEqual(t, TitleFromURL(exact), exact)
	cut := TitleFromURL(exact + "y")
	assertEqual(t, cut, exact)
	if _, err := input("https://a.b", cut).Validate(); err != nil {
		t.Errorf("the cut title is not a valid title: %v", err)
	}
}

func TestTagsAreLowercasedTrimmedAndDeduplicated(t *testing.T) {
	in := input("https://a.b", "t")
	in.Tags = []string{"Rust", " rust ", "ÉCOLE", "", "  ", "日本語", "Go"}
	ok, err := in.Validate()
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, ok.Tags, []string{"rust", "école", "日本語", "go"})
}

func TestBadTagsAreRejected(t *testing.T) {
	tooLong := input("https://a.b", "t")
	tooLong.Tags = []string{strings.Repeat("x", maxTagChars+1)}
	control := input("https://a.b", "t")
	control.Tags = []string{"a\x1fb"}
	many := input("https://a.b", "t")
	for n := 0; n <= MaxTagsPerBookmark; n++ {
		many.Tags = append(many.Tags, fmt.Sprintf("tag%d", n))
	}
	for name, in := range map[string]Input{"too long": tooLong, "control": control, "many": many} {
		if _, err := in.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestFittedKeepsWhatItCan(t *testing.T) {
	tags := []string{"Rust", "rust", "a\x1fb", strings.Repeat("x", 99)}
	for n := 0; n < 30; n++ {
		tags = append(tags, fmt.Sprintf("t%d", n))
	}
	fitted := Fitted("https://a.b", "  ", strings.Repeat("n", maxNoteChars+5), tags)
	assertEqual(t, fitted.Title, "https://a.b")
	assertEqual(t, len([]rune(fitted.Note)), maxNoteChars)
	assertEqual(t, len(fitted.Tags), MaxTagsPerBookmark)
	assertEqual(t, fitted.Tags[0], "rust")
	assertEqual(t, fitted.Tags[1], "t0")
	long := Fitted("https://a.b", strings.Repeat("学", maxTitleChars+1), "", nil)
	assertEqual(t, len([]rune(long.Title)), maxTitleChars)
}

func TestPageSizeIsCapped(t *testing.T) {
	zero, big := uint32(0), uint32(10_000)
	assertEqual(t, NewPage(nil, nil).Limit, DefaultPageSize)
	assertEqual(t, NewPage(&zero, nil).Limit, uint32(1))
	assertEqual(t, NewPage(&big, nil).Limit, MaxPageSize)
}

func TestACreatedBookmarkReadsBackWithSortedTags(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	created := create(t, store, user, valid(t, "Rust book", "rust", "book"), 100)

	assertEqual(t, created.Tags, []string{"book", "rust"})
	assertEqual(t, []int64{created.CreatedAt, created.UpdatedAt}, []int64{100, 100})
	got, found, err := store.Get(bg, user, created.ID)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	assertEqual(t, got, created)
}

func TestABookmarkWithoutTagsHasAnEmptyList(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	created := create(t, store, user, valid(t, "Plain"), 100)
	if created.Tags == nil || len(created.Tags) != 0 {
		t.Fatalf("tags: %#v", created.Tags)
	}
}

func TestUpdateReplacesFieldsAndTagsAndKeepsCreatedAt(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	created := create(t, store, user, valid(t, "Old", "a", "b"), 100)

	updated, found, err := store.Update(bg, UpdateRequest{UserID: user, ID: created.ID, Bookmark: valid(t, "New", "c"), Now: 200})
	if err != nil || !found {
		t.Fatal(found, err)
	}
	assertEqual(t, updated.Title, "New")
	assertEqual(t, updated.Tags, []string{"c"})
	assertEqual(t, []int64{updated.CreatedAt, updated.UpdatedAt}, []int64{100, 200})
}

func TestUpdateKeepsTheLinkCheckOnlyWhileTheURLIsTheSame(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	created := create(t, store, user, valid(t, "Checked"), 100)
	if _, err := pool.Exec("UPDATE bookmarks SET link_status='ok', link_checked_at=5, link_detail='d' WHERE id=?", created.ID); err != nil {
		t.Fatal(err)
	}

	same, _, err := store.Update(bg, UpdateRequest{UserID: user, ID: created.ID, Bookmark: valid(t, "Checked"), Now: 200})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, same.Link, &LinkCheck{Status: "ok", CheckedAt: 5, Detail: "d"})

	moved := valid(t, "Checked")
	moved.URL = "https://elsewhere.test/"
	changed, _, err := store.Update(bg, UpdateRequest{UserID: user, ID: created.ID, Bookmark: moved, Now: 300})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Link != nil {
		t.Fatalf("link survived a URL change: %#v", changed.Link)
	}
}

func TestAnotherUsersBookmarkCannotBeReadChangedOrDeleted(t *testing.T) {
	store, pool := newStore(t)
	owner := dbtest.InsertUser(t, pool, "henry")
	other := dbtest.InsertUser(t, pool, "mei")
	created := create(t, store, owner, valid(t, "Mine", "x"), 100)

	if _, found, _ := store.Get(bg, other, created.ID); found {
		t.Error("read someone else's bookmark")
	}
	_, found, err := store.Update(bg, UpdateRequest{UserID: other, ID: created.ID, Bookmark: valid(t, "Hijacked"), Now: 200})
	if err != nil || found {
		t.Errorf("update: found=%v err=%v", found, err)
	}
	if deleted, _ := store.Delete(bg, other, created.ID); deleted {
		t.Error("deleted someone else's bookmark")
	}
	untouched, _, _ := store.Get(bg, owner, created.ID)
	assertEqual(t, untouched.Title, "Mine")
	assertEqual(t, untouched.Tags, []string{"x"})
}

func TestDeleteRemovesTheBookmarkAndItsTags(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	created := create(t, store, user, valid(t, "Gone", "t"), 100)

	first, _ := store.Delete(bg, user, created.ID)
	second, _ := store.Delete(bg, user, created.ID)
	assertEqual(t, []bool{first, second}, []bool{true, false})
	counts, err := store.TagCounts(bg, user)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, len(counts), 0)
}

func TestListIsNewestFirstAndPrivateToTheOwner(t *testing.T) {
	store, pool := newStore(t)
	henry := dbtest.InsertUser(t, pool, "henry")
	mei := dbtest.InsertUser(t, pool, "mei")
	create(t, store, henry, valid(t, "first"), 100)
	create(t, store, henry, valid(t, "second"), 200)
	create(t, store, mei, valid(t, "hers"), 150)

	assertEqual(t, listTitles(t, store, henry), []string{"second", "first"})
}

func TestBookmarksMadeInTheSameSecondKeepAStableOrder(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	for _, title := range []string{"a", "b", "c"} {
		create(t, store, user, valid(t, title), 100)
	}
	assertEqual(t, listTitles(t, store, user), []string{"c", "b", "a"})
}

func TestPagesDoNotOverlap(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	for n := range 5 {
		create(t, store, user, valid(t, fmt.Sprintf("bookmark %d", n)), int64(n))
	}
	seen := map[int64]bool{}
	two := uint32(2)
	for _, offset := range []uint32{0, 2, 4} {
		page, err := store.List(bg, user, nil, NewPage(&two, &offset))
		if err != nil {
			t.Fatal(err)
		}
		for _, bookmark := range page {
			seen[bookmark.ID] = true
		}
	}
	assertEqual(t, len(seen), 5)
}

func TestTagCountsCoverOnlyTheOwner(t *testing.T) {
	store, pool := newStore(t)
	henry := dbtest.InsertUser(t, pool, "henry")
	mei := dbtest.InsertUser(t, pool, "mei")
	create(t, store, henry, valid(t, "one", "rust", "web"), 1)
	create(t, store, henry, valid(t, "two", "rust"), 2)
	create(t, store, mei, valid(t, "three", "secret"), 3)

	counts, err := store.TagCounts(bg, henry)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, counts, []TagCount{{"rust", 2}, {"web", 1}})
}

func TestLinkCountsCoverOnlyTheOwnerAndNameUnchecked(t *testing.T) {
	store, pool := newStore(t)
	henry := dbtest.InsertUser(t, pool, "henry")
	mei := dbtest.InsertUser(t, pool, "mei")
	cases := []struct {
		owner  int64
		title  string
		status any
	}{{henry, "one", "broken"}, {henry, "two", "broken"}, {henry, "three", nil}, {mei, "four", "ok"}}
	for _, c := range cases {
		id := create(t, store, c.owner, valid(t, c.title), 1).ID
		if _, err := pool.Exec("UPDATE bookmarks SET link_status = ?1 WHERE id = ?2", c.status, id); err != nil {
			t.Fatal(err)
		}
	}
	counts, err := store.LinkCounts(bg, henry)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, counts, []LinkCount{{"broken", 2}, {"unchecked", 1}})
}

func TestCountCoversOnlyTheOwnerAndTheFilter(t *testing.T) {
	store, pool := newStore(t)
	henry := dbtest.InsertUser(t, pool, "henry")
	mei := dbtest.InsertUser(t, pool, "mei")
	count := func(user int64, filter Filter) int64 {
		t.Helper()
		got, err := store.Count(bg, user, filter)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	assertEqual(t, count(henry, nil), int64(0))
	create(t, store, henry, valid(t, "one"), 1)
	create(t, store, henry, valid(t, "two"), 2)
	create(t, store, mei, valid(t, "one"), 3)
	assertEqual(t, count(henry, nil), int64(2))
	assertEqual(t, count(mei, nil), int64(1))
	assertEqual(t, count(henry, titleIs("one")), int64(1))
	assertEqual(t, count(henry, titleIs("none")), int64(0))
}

func TestRandomPicksOneOfTheOwnersBookmarks(t *testing.T) {
	store, pool := newStore(t)
	henry := dbtest.InsertUser(t, pool, "henry")
	mei := dbtest.InsertUser(t, pool, "mei")
	create(t, store, mei, valid(t, "hers"), 1)
	if _, found, err := store.Random(bg, henry, nil); found || err != nil {
		t.Fatalf("found=%v err=%v", found, err)
	}
	create(t, store, henry, valid(t, "mine"), 1)
	picked, found, err := store.Random(bg, henry, nil)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	assertEqual(t, picked.Title, "mine")
}

func TestAllIsOldestFirst(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	create(t, store, user, valid(t, "late"), 200)
	create(t, store, user, valid(t, "early"), 100)
	all, err := store.All(bg, user)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, []string{all[0].Title, all[1].Title}, []string{"early", "late"})
}

func TestImportSkipsKnownAndRepeatedURLsInOneTransaction(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	create(t, store, user, valid(t, "known"), 1)
	entries := []ImportEntry{
		{Bookmark: valid(t, "known"), CreatedAt: 5},
		{Bookmark: valid(t, "new", "x"), CreatedAt: 6},
		{Bookmark: valid(t, "new", "y"), CreatedAt: 7},
	}
	added, err := store.Import(bg, user, entries)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, added, 1)
	assertEqual(t, len(listTitles(t, store, user)), 2)
}

func TestCountsNarrowedByTheOtherFilter(t *testing.T) {
	store, pool := newStore(t)
	henry := dbtest.InsertUser(t, pool, "henry")
	cases := []struct {
		title  string
		tags   []string
		status any
	}{
		{"one", []string{"rust", "web"}, "broken"},
		{"two", []string{"rust"}, nil},
		{"three", []string{"web"}, "ok"},
	}
	for _, c := range cases {
		id := create(t, store, henry, valid(t, c.title, c.tags...), 1).ID
		if _, err := pool.Exec("UPDATE bookmarks SET link_status = ?1 WHERE id = ?2", c.status, id); err != nil {
			t.Fatal(err)
		}
	}

	tags, err := store.TagCountsWithStatus(bg, henry, " Broken ")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, tags, []TagCount{{"rust", 1}, {"web", 1}})
	tags, err = store.TagCountsWithStatus(bg, henry, "unchecked")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, tags, []TagCount{{"rust", 1}})

	links, err := store.LinkCountsWithTag(bg, henry, "rust")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, links, []LinkCount{{"broken", 1}, {"unchecked", 1}})
	links, err = store.LinkCountsWithTag(bg, henry, "web")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, links, []LinkCount{{"broken", 1}, {"ok", 1}})
}
