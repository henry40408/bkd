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
	for _, miss := range []string{mine.URL + "/", "HTTPS://example.com/mine", "https://example.com/min", ""} {
		if _, ok, err := store.FindByURL(bg, alice, miss); err != nil || ok {
			t.Errorf("%q: found = %v, %v", miss, ok, err)
		}
	}
	if _, ok, err := store.FindByURL(bg, dbtest.InsertUser(t, pool, "carol"), mine.URL); err != nil || ok {
		t.Fatalf("another user's bookmark was found: %v, %v", ok, err)
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
