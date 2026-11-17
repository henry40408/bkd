package bookmarks

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/henry40408/bkd/internal/dbtest"
)

func parseBulk(t *testing.T, body string) (BulkInput, error) {
	t.Helper()
	var in BulkInput
	err := json.Unmarshal([]byte(body), &in)
	return in, err
}

func TestBulkJSONFollowsTheTaggedShape(t *testing.T) {
	in, err := parseBulk(t, `{"ids":[1,2],"action":"add_tags","tags":["a"]}`)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, in, BulkInput{IDs: []int64{1, 2}, Action: BulkAddTags, Tags: []string{"a"}})

	del, err := parseBulk(t, `{"ids":[1],"action":"delete","tags":"ignored"}`)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, del.Action, BulkDelete)

	for _, bad := range []string{
		`{"action":"delete"}`,
		`{"ids":[1]}`,
		`{"ids":[1],"action":"explode"}`,
		`{"ids":[1],"action":"add_tags"}`,
		`{"ids":"1","action":"delete"}`,
	} {
		if _, err := parseBulk(t, bad); err == nil {
			t.Errorf("%s was accepted", bad)
		}
	}
}

func TestBulkValidation(t *testing.T) {
	cases := map[string]BulkInput{
		"ids must not be empty":  {Action: BulkDelete},
		"too many ids":           {IDs: seq(MaxBulkIDs + 1), Action: BulkDelete},
		"tags must not be empty": {IDs: []int64{1}, Action: BulkAddTags, Tags: []string{" ", ""}},
		"tag is too long":        {IDs: []int64{1}, Action: BulkRemoveTags, Tags: []string{strings.Repeat("x", 51)}},
	}
	for want, in := range cases {
		_, err := in.Validate()
		if err == nil || err.Error() != want {
			t.Errorf("want %q, got %v", want, err)
		}
	}
	valid, err := BulkInput{IDs: []int64{3, 3, 4, 3}, Action: BulkDelete}.Validate()
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, valid.IDs, []int64{3, 4})
	// Repeats do not count toward the limit.
	if _, err := (BulkInput{IDs: append(seq(MaxBulkIDs), 1, 2), Action: BulkDelete}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func seq(n int) []int64 {
	ids := make([]int64, n)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	return ids
}

func apply(store *Store, user int64, bulk BulkInput, now int64) (int, error) {
	valid, err := bulk.Validate()
	if err != nil {
		return 0, err
	}
	return store.ApplyBulk(bg, user, valid, now)
}

func TestBulkAddAndRemoveTagsCountOnlyRealChanges(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	a := create(t, store, user, valid(t, "a", "x"), 1)
	b := create(t, store, user, valid(t, "b"), 1)

	changed, err := apply(store, user, BulkInput{IDs: []int64{a.ID, b.ID}, Action: BulkAddTags, Tags: []string{"X", "y"}}, 50)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, changed, 2)
	got, _, _ := store.Get(bg, user, a.ID)
	assertEqual(t, got.Tags, []string{"x", "y"})
	assertEqual(t, got.UpdatedAt, int64(50))

	again, _ := apply(store, user, BulkInput{IDs: []int64{a.ID, b.ID}, Action: BulkAddTags, Tags: []string{"x"}}, 60)
	assertEqual(t, again, 0)
	untouched, _, _ := store.Get(bg, user, a.ID)
	assertEqual(t, untouched.UpdatedAt, int64(50))

	removed, err := apply(store, user, BulkInput{IDs: []int64{a.ID, b.ID}, Action: BulkRemoveTags, Tags: []string{"x"}}, 70)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, removed, 2)
	missing, _ := apply(store, user, BulkInput{IDs: []int64{a.ID}, Action: BulkRemoveTags, Tags: []string{"nope"}}, 80)
	assertEqual(t, missing, 0)
}

func TestBulkDeleteRemovesEveryBookmark(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	a := create(t, store, user, valid(t, "a", "x"), 1)
	b := create(t, store, user, valid(t, "b"), 1)
	create(t, store, user, valid(t, "c"), 1)

	deleted, err := apply(store, user, BulkInput{IDs: []int64{a.ID, b.ID}, Action: BulkDelete}, 2)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, deleted, 2)
	assertEqual(t, listTitles(t, store, user), []string{"c"})
}

func TestBulkRefusesAnotherUsersOrMissingIDAndChangesNothing(t *testing.T) {
	store, pool := newStore(t)
	henry := dbtest.InsertUser(t, pool, "henry")
	mei := dbtest.InsertUser(t, pool, "mei")
	mine := create(t, store, henry, valid(t, "mine"), 1)
	hers := create(t, store, mei, valid(t, "hers"), 1)

	for _, ids := range [][]int64{{mine.ID, hers.ID}, {mine.ID, 9999}} {
		_, err := apply(store, henry, BulkInput{IDs: ids, Action: BulkDelete}, 2)
		if !errors.Is(err, ErrBulkNotFound) {
			t.Fatalf("ids %v: %v", ids, err)
		}
	}
	assertEqual(t, listTitles(t, store, henry), []string{"mine"})
	assertEqual(t, listTitles(t, store, mei), []string{"hers"})
}

func TestBulkRollsBackWhenABookmarkWouldGetTooManyTags(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	var full []string
	for n := range MaxTagsPerBookmark {
		full = append(full, fmt.Sprintf("t%d", n))
	}
	crowded := create(t, store, user, valid(t, "crowded", full...), 1)
	roomy := create(t, store, user, valid(t, "roomy"), 1)

	_, err := apply(store, user, BulkInput{IDs: []int64{roomy.ID, crowded.ID}, Action: BulkAddTags, Tags: []string{"extra"}}, 9)
	if !errors.Is(err, ErrBulkTooManyTags) {
		t.Fatalf("got %v", err)
	}
	got, _, _ := store.Get(bg, user, roomy.ID)
	assertEqual(t, got.Tags, []string{})
	assertEqual(t, got.UpdatedAt, int64(1))
}
