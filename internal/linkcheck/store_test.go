package linkcheck

import (
	"context"
	"testing"

	"github.com/henry40408/bkd/internal/bookmarks"
	"github.com/henry40408/bkd/internal/dbtest"
)

const now int64 = 1_800_000_000

func addBookmark(t *testing.T, store *bookmarks.Store, userID int64, address string) int64 {
	t.Helper()
	created, err := store.Create(context.Background(), userID,
		bookmarks.Valid{URL: address, Title: "t", Tags: []string{}}, now)
	if err != nil {
		t.Fatal(err)
	}
	return created.ID
}

func TestTargetsAreFoundOnlyWhenEveryIDIsTheUsers(t *testing.T) {
	pool := dbtest.Open(t)
	books := bookmarks.NewStore(pool)
	alice, bob := dbtest.InsertUser(t, pool, "alice"), dbtest.InsertUser(t, pool, "bob")
	mine := addBookmark(t, books, alice, "https://a.test/")
	theirs := addBookmark(t, books, bob, "https://b.test/")
	store := NewStore(pool)
	ctx := context.Background()

	targets, found, err := store.Targets(ctx, alice, ValidCheck{IDs: []int64{mine}})
	assertEqual(t, err, nil)
	assertEqual(t, found, true)
	assertEqual(t, targets, []Target{{ID: mine, URL: "https://a.test/"}})

	for _, ids := range [][]int64{{mine, theirs}, {mine, 9999}} {
		_, found, err = store.Targets(ctx, alice, ValidCheck{IDs: ids})
		assertEqual(t, err, nil)
		assertEqual(t, found, false)
	}
}

func TestVerdictsAreStoredAndShownOnTheBookmark(t *testing.T) {
	pool := dbtest.Open(t)
	books := bookmarks.NewStore(pool)
	user := dbtest.InsertUser(t, pool, "alice")
	id := addBookmark(t, books, user, "https://a.test/")
	ctx := context.Background()

	stored, err := NewStore(pool).Store(ctx, StoreRequest{
		UserID: user, Now: now + 5,
		Checked: []Checked{{Target: Target{ID: id, URL: "https://a.test/"}, Verdict: Verdict{StatusBroken, "HTTP 404"}}},
	})
	assertEqual(t, err, nil)
	want := bookmarks.LinkCheck{Status: "broken", CheckedAt: now + 5, Detail: "HTTP 404"}
	assertEqual(t, stored, []Stored{{ID: id, Check: want}})

	bookmark, _, err := books.Get(ctx, user, id)
	assertEqual(t, err, nil)
	assertEqual(t, bookmark.Link, &want)
}

func TestAVerdictForAnEditedURLIsDropped(t *testing.T) {
	pool := dbtest.Open(t)
	books := bookmarks.NewStore(pool)
	user := dbtest.InsertUser(t, pool, "alice")
	id := addBookmark(t, books, user, "https://new.test/")

	stored, err := NewStore(pool).Store(context.Background(), StoreRequest{
		UserID: user, Now: now,
		Checked: []Checked{{Target: Target{ID: id, URL: "https://old.test/"}, Verdict: Verdict{StatusOK, ""}}},
	})
	assertEqual(t, err, nil)
	assertEqual(t, len(stored), 0)
	bookmark, _, _ := books.Get(context.Background(), user, id)
	assertEqual(t, bookmark.Link, (*bookmarks.LinkCheck)(nil))
}

func TestAVerdictForSomeoneElsesBookmarkIsDropped(t *testing.T) {
	pool := dbtest.Open(t)
	books := bookmarks.NewStore(pool)
	alice, bob := dbtest.InsertUser(t, pool, "alice"), dbtest.InsertUser(t, pool, "bob")
	id := addBookmark(t, books, bob, "https://b.test/")

	stored, err := NewStore(pool).Store(context.Background(), StoreRequest{
		UserID: alice, Now: now,
		Checked: []Checked{{Target: Target{ID: id, URL: "https://b.test/"}, Verdict: Verdict{StatusOK, ""}}},
	})
	assertEqual(t, err, nil)
	assertEqual(t, len(stored), 0)
}
