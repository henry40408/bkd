package bookmarks

import (
	"testing"

	"github.com/henry40408/bkd/internal/dbtest"
)

type failureCase struct {
	name string
	run  func(store *Store) error
}

// storeOperations runs every store operation once, on the bookmark with id 1.
func storeOperations(t *testing.T) []failureCase {
	tagged := valid(t, "tagged", "go")
	bulk := func(action BulkAction) func(*Store) error {
		return func(store *Store) error {
			_, err := store.ApplyBulk(bg, 1, ValidBulk{IDs: []int64{1}, Action: action, Tags: []string{"go"}}, 5)
			return err
		}
	}
	return []failureCase{
		{"Create", func(s *Store) error { _, err := s.Create(bg, 1, tagged, 5); return err }},
		{"Update", func(s *Store) error {
			_, _, err := s.Update(bg, UpdateRequest{UserID: 1, ID: 1, Bookmark: tagged, Now: 6})
			return err
		}},
		{"Delete", func(s *Store) error { _, err := s.Delete(bg, 1, 1); return err }},
		{"Get", func(s *Store) error { _, _, err := s.Get(bg, 1, 1); return err }},
		{"List", func(s *Store) error { _, err := s.List(bg, 1, nil, NewPage(nil, nil)); return err }},
		{"Random", func(s *Store) error { _, _, err := s.Random(bg, 1, nil); return err }},
		{"All", func(s *Store) error { _, err := s.All(bg, 1); return err }},
		{"TagCounts", func(s *Store) error { _, err := s.TagCounts(bg, 1); return err }},
		{"LinkCounts", func(s *Store) error { _, err := s.LinkCounts(bg, 1); return err }},
		{"Import", func(s *Store) error {
			_, err := s.Import(bg, 1, []ImportEntry{{Bookmark: tagged, CreatedAt: 1}})
			return err
		}},
		{"ApplyBulk delete", bulk(BulkDelete)},
		{"ApplyBulk add_tags", bulk(BulkAddTags)},
		{"ApplyBulk remove_tags", bulk(BulkRemoveTags)},
	}
}

func expectEveryOperationToFail(t *testing.T, store *Store) {
	t.Helper()
	for _, c := range storeOperations(t) {
		if err := c.run(store); err == nil {
			t.Errorf("%s: no error although the database fails", c.name)
		}
	}
}

func TestEveryStoreOperationReportsAClosedDatabase(t *testing.T) {
	pool := dbtest.Open(t)
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	expectEveryOperationToFail(t, NewStore(pool))
}

// With the tag table gone, the operations that read or write tags fail half
// way, and must report it instead of keeping a bookmark without its tags.
func TestOperationsThatTouchTagsReportAFailingTagTable(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "alice")
	create(t, store, user, valid(t, "existing"), 1)
	if _, err := pool.Exec("ALTER TABLE bookmark_tags RENAME TO bookmark_tags_gone"); err != nil {
		t.Fatal(err)
	}
	for _, c := range storeOperations(t) {
		switch c.name {
		case "Create", "Update", "Import", "TagCounts", "List", "Get", "All", "Random",
			"ApplyBulk add_tags", "ApplyBulk remove_tags":
			if err := c.run(store); err == nil {
				t.Errorf("%s: no error although the tag table fails", c.name)
			}
		}
	}

	var count int64
	if err := pool.QueryRow("SELECT count(*) FROM bookmarks").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("bookmarks = %d after failed writes, want the 1 that existed", count)
	}
}

// A write the database refuses must come back as an error and leave nothing
// half done. Each table is refused alone, so that a failure in the tag writes
// is reached after the bookmark writes worked.
func TestEveryBookmarkWriteReportsARefusedChange(t *testing.T) {
	for _, table := range []string{"bookmarks", "bookmark_tags"} {
		t.Run(table, func(t *testing.T) { expectRefusedWritesToFail(t, table) })
	}
}

func expectRefusedWritesToFail(t *testing.T, table string) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "alice")
	existing := create(t, store, user, valid(t, "existing", "go"), 1)
	for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
		if _, err := pool.Exec("CREATE TRIGGER refuse_" + event + " BEFORE " + event + " ON " + table +
			" BEGIN SELECT RAISE(ABORT, 'refused'); END"); err != nil {
			t.Fatal(err)
		}
	}
	incoming := valid(t, "incoming", "rust")
	ids := []int64{existing.ID}
	cases := []struct {
		name string
		run  func() error
	}{
		{"Create", func() error { _, err := store.Create(bg, user, incoming, 5); return err }},
		{"Update", func() error {
			_, _, err := store.Update(bg, UpdateRequest{UserID: user, ID: existing.ID, Bookmark: incoming, Now: 6})
			return err
		}},
		{"Delete", func() error { _, err := store.Delete(bg, user, existing.ID); return err }},
		{"Import", func() error {
			_, err := store.Import(bg, user, []ImportEntry{{Bookmark: incoming, CreatedAt: 1}})
			return err
		}},
		{"ApplyBulk delete", func() error {
			_, err := store.ApplyBulk(bg, user, ValidBulk{IDs: ids, Action: BulkDelete}, 5)
			return err
		}},
		{"ApplyBulk add_tags", func() error {
			_, err := store.ApplyBulk(bg, user, ValidBulk{IDs: ids, Action: BulkAddTags, Tags: []string{"new"}}, 5)
			return err
		}},
		{"ApplyBulk remove_tags", func() error {
			_, err := store.ApplyBulk(bg, user, ValidBulk{IDs: ids, Action: BulkRemoveTags, Tags: []string{"go"}}, 5)
			return err
		}},
	}
	for _, c := range cases {
		if err := c.run(); err == nil {
			t.Errorf("%s: no error although the database refused the write", c.name)
		}
	}
	assertEqual(t, listTitles(t, store, user), []string{"existing"})
}

// titleIs is a filter as the search package builds them: SQL conditions with
// bind arguments.
type titleIs string

func (f titleIs) SQL() (string, []any) { return " AND bookmarks.title = ?", []any{string(f)} }

func TestListAndRandomApplyTheFilterTheyAreGiven(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "alice")
	create(t, store, user, valid(t, "keep"), 1)
	create(t, store, user, valid(t, "drop"), 2)

	list, err := store.List(bg, user, titleIs("keep"), NewPage(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Title != "keep" {
		t.Fatalf("List = %+v, want only the bookmark named keep", list)
	}
	random, found, err := store.Random(bg, user, titleIs("drop"))
	if err != nil || !found || random.Title != "drop" {
		t.Fatalf("Random = %+v, %v, %v, want the bookmark named drop", random, found, err)
	}
	_, found, err = store.Random(bg, user, titleIs("nothing"))
	if err != nil || found {
		t.Fatalf("Random with no match: found = %v, err = %v", found, err)
	}
}
