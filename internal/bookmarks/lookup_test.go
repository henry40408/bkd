package bookmarks

import (
	"testing"

	"github.com/henry40408/bkd/internal/dbtest"
)

func at(value int64) *int64 { return &value }

func idsOf(list []Bookmark) []int64 {
	ids := []int64{}
	for _, bookmark := range list {
		ids = append(ids, bookmark.ID)
	}
	return ids
}

func TestSinceKeepsOnlyWhatChangedAfterTheTime(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "alice")
	old := create(t, store, user, valid(t, "old"), 100)
	edited := create(t, store, user, valid(t, "edited"), 100)
	fresh := create(t, store, user, valid(t, "fresh"), 300)
	if _, _, err := store.Update(bg, UpdateRequest{UserID: user, ID: edited.ID, Bookmark: valid(t, "edited again"), Now: 200}); err != nil {
		t.Fatal(err)
	}
	list := func(filter Filter) []int64 {
		got, err := store.List(bg, user, filter, NewPage(nil, nil))
		if err != nil {
			t.Fatal(err)
		}
		return idsOf(got)
	}
	// "After" is strict: a bookmark saved at exactly the time is not newer.
	assertEqual(t, list(Since{AddedAfter: at(100)}), []int64{fresh.ID})
	assertEqual(t, list(Since{AddedAfter: at(99)}), []int64{fresh.ID, edited.ID, old.ID})
	assertEqual(t, list(Since{ModifiedAfter: at(100)}), []int64{fresh.ID, edited.ID})
	assertEqual(t, list(Since{ModifiedAfter: at(200)}), []int64{fresh.ID})
	assertEqual(t, list(Since{ModifiedAfter: at(300)}), []int64{})
	assertEqual(t, list(Since{AddedAfter: at(99), ModifiedAfter: at(150)}), []int64{fresh.ID, edited.ID})
	assertEqual(t, list(Since{}), []int64{fresh.ID, edited.ID, old.ID})
}

func TestSinceAddsToTheFilterItWraps(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "alice")
	create(t, store, user, valid(t, "plain old", "go"), 100)
	wanted := create(t, store, user, valid(t, "plain new", "go"), 300)
	create(t, store, user, valid(t, "other new", "rust"), 300)
	filter := Since{Inner: tagFilter("go"), AddedAfter: at(200)}
	got, err := store.List(bg, user, filter, NewPage(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, idsOf(got), []int64{wanted.ID})
	count, err := store.Count(bg, user, filter)
	if err != nil || count != 1 {
		t.Fatalf("Count = %d, %v", count, err)
	}
}

// tagFilter is the smallest Filter that has arguments of its own.
type tagFilter string

func (tag tagFilter) SQL() (string, []any) {
	return " AND EXISTS (SELECT 1 FROM bookmark_tags WHERE bookmark_id = bookmarks.id AND tag = ?)", []any{string(tag)}
}

func TestFindByURLIsExactTrimmedAndOwnedByTheUser(t *testing.T) {
	store, pool := newStore(t)
	alice := dbtest.InsertUser(t, pool, "alice")
	bob := dbtest.InsertUser(t, pool, "bob")
	mine := create(t, store, alice, valid(t, "mine"), 100)
	create(t, store, bob, valid(t, "mine"), 100)

	found, ok, err := store.FindByURL(bg, alice, "  "+mine.URL+" ")
	if err != nil || !ok || found.ID != mine.ID {
		t.Fatalf("FindByURL = %v, %v, %v", found.ID, ok, err)
	}
	for _, miss := range []string{"HTTPS://example.com/mine", "https://example.com/min", "https://example.com/mine/x", "", "///"} {
		if _, ok, err := store.FindByURL(bg, alice, miss); err != nil || ok {
			t.Errorf("%q: found = %v, %v", miss, ok, err)
		}
	}
	if _, ok, err := store.FindByURL(bg, dbtest.InsertUser(t, pool, "carol"), mine.URL); err != nil || ok {
		t.Fatalf("another user's bookmark was found: %v, %v", ok, err)
	}
}

func TestFindByURLIgnoresATrailingSlashButPrefersTheSpellingAsked(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "alice")
	save := func(url string) Bookmark {
		bookmark, err := Input{URL: url, Title: url}.Validate()
		if err != nil {
			t.Fatal(err)
		}
		return create(t, store, user, bookmark, 100)
	}
	bare := save("https://example.com")
	slashed := save("https://example.org/")
	both := save("https://example.net/a")
	bothSlashed := save("https://example.net/a/")

	cases := []struct {
		asked string
		want  int64
	}{
		{"https://example.com/", bare.ID},
		{"https://example.com//", bare.ID},
		{"https://example.org", slashed.ID},
		{" https://example.org/ ", slashed.ID},
		{"https://example.net/a", both.ID},
		{"https://example.net/a/", bothSlashed.ID},
		{"https://example.net/a//", both.ID},
	}
	for _, c := range cases {
		found, ok, err := store.FindByURL(bg, user, c.asked)
		if err != nil || !ok || found.ID != c.want {
			t.Errorf("%q: found %d, %v, %v, want %d", c.asked, found.ID, ok, err, c.want)
		}
	}
}

func TestFindByURLPicksTheOldestOfSeveralWithTheSameAddress(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "alice")
	first := create(t, store, user, valid(t, "same"), 100)
	create(t, store, user, valid(t, "same"), 200)
	found, ok, err := store.FindByURL(bg, user, first.URL)
	if err != nil || !ok || found.ID != first.ID {
		t.Fatalf("FindByURL = %v, %v, %v, want %d", found.ID, ok, err, first.ID)
	}
}

func TestTagEntriesListTagsByNameWithAStableIDAndTheOldestDate(t *testing.T) {
	store, pool := newStore(t)
	alice := dbtest.InsertUser(t, pool, "alice")
	bob := dbtest.InsertUser(t, pool, "bob")
	create(t, store, alice, valid(t, "one", "go", "web"), 100)
	create(t, store, alice, valid(t, "two", "go", "api"), 50)
	create(t, store, bob, valid(t, "theirs", "private"), 10)

	entries, total, err := store.TagEntries(bg, alice, NewPage(nil, nil))
	if err != nil || total != 3 {
		t.Fatalf("TagEntries = %v, %d, %v", entries, total, err)
	}
	names := []string{}
	ids := map[int64]bool{}
	for _, entry := range entries {
		names = append(names, entry.Name)
		ids[entry.ID] = true
	}
	assertEqual(t, names, []string{"api", "go", "web"})
	if len(ids) != 3 {
		t.Fatalf("ids are not unique: %v", entries)
	}
	assertEqual(t, []int64{entries[0].AddedAt, entries[1].AddedAt, entries[2].AddedAt}, []int64{50, 50, 100})

	again, _, err := store.TagEntries(bg, alice, NewPage(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, again, entries)
}

// A program keeps a tag's id between visits, so it must not depend on which
// bookmark rows exist: an edit rewrites a bookmark's tags.
func TestTagEntryIDsComeFromTheNameAndSurviveAnEdit(t *testing.T) {
	store, pool := newStore(t)
	alice := dbtest.InsertUser(t, pool, "alice")
	bob := dbtest.InsertUser(t, pool, "bob")
	saved := create(t, store, alice, valid(t, "one", "go", "web"), 100)
	create(t, store, bob, valid(t, "theirs", "go"), 100)

	idsOf := func(user int64) map[string]int64 {
		entries, _, err := store.TagEntries(bg, user, NewPage(nil, nil))
		if err != nil {
			t.Fatal(err)
		}
		ids := map[string]int64{}
		for _, entry := range entries {
			ids[entry.Name] = entry.ID
		}
		return ids
	}
	before := idsOf(alice)
	assertEqual(t, before, map[string]int64{"go": 2702720969687282, "web": 2651794393884136})

	if _, _, err := store.Update(bg, UpdateRequest{UserID: alice, ID: saved.ID, Bookmark: valid(t, "one", "go", "web", "api"), Now: 200}); err != nil {
		t.Fatal(err)
	}
	after := idsOf(alice)
	assertEqual(t, after, map[string]int64{"api": 730394954991921, "go": 2702720969687282, "web": 2651794393884136})
	assertEqual(t, idsOf(bob)["go"], before["go"])
	for name, id := range after {
		if id <= 0 || id >= 1<<53 {
			t.Errorf("%s: id %d is not a positive integer a JSON number holds exactly", name, id)
		}
	}
}

func TestTagEntriesPageThroughTheTagsAndCountThemAll(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "alice")
	create(t, store, user, valid(t, "one", "a", "b", "c"), 1)

	page := func(limit, offset uint32) []string {
		entries, total, err := store.TagEntries(bg, user, Page{Limit: limit, Offset: offset})
		if err != nil || total != 3 {
			t.Fatalf("TagEntries = %v, %d, %v", entries, total, err)
		}
		names := []string{}
		for _, entry := range entries {
			names = append(names, entry.Name)
		}
		return names
	}
	assertEqual(t, page(2, 0), []string{"a", "b"})
	assertEqual(t, page(2, 2), []string{"c"})
	assertEqual(t, page(2, 3), []string{})
}

func TestUserWithoutTagsHasNoTagEntries(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "alice")
	create(t, store, user, valid(t, "untagged"), 1)
	entries, total, err := store.TagEntries(bg, user, NewPage(nil, nil))
	if err != nil || total != 0 || entries == nil || len(entries) != 0 {
		t.Fatalf("TagEntries = %#v, %d, %v", entries, total, err)
	}
}
