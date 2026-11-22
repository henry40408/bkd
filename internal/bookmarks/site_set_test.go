package bookmarks

import (
	"strings"
	"testing"

	"github.com/henry40408/bkd/internal/dbtest"
)

// createRead is a bookmark made at time 100 whose page was read once.
func createRead(t *testing.T, store *Store, user int64, note string) Bookmark {
	t.Helper()
	bookmark := withSite(valid(t, "Page", "go"), Site{Title: "Old title", Description: "old"})
	bookmark.Note = note
	return create(t, store, user, bookmark, 100)
}

func reload(t *testing.T, store *Store, user, id int64) Bookmark {
	t.Helper()
	got, found, err := store.Get(bg, user, id)
	if err != nil || !found {
		t.Fatalf("Get: found = %v, err = %v", found, err)
	}
	return got
}

func setSite(t *testing.T, store *Store, user int64, saved Bookmark, site Site) SiteStored {
	t.Helper()
	stored, err := store.SetSite(bg, SetSiteRequest{UserID: user, ID: saved.ID, URL: saved.URL, Site: site})
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func TestSetSiteReplacesBothDetailsAndFillsABlankNote(t *testing.T) {
	cases := []struct {
		name       string
		page       Site
		wantSite   Site
		wantNote   string
		wantFilled bool
	}{
		{"both", Site{Title: "T", Description: "D"}, Site{Title: "T", Description: "D"}, "D", true},
		{"only a title", Site{Title: "T"}, Site{Title: "T"}, "", false},
		{"only a description", Site{Description: "D"}, Site{Description: "D"}, "D", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store, pool := newStore(t)
			user := dbtest.InsertUser(t, pool, "henry")
			saved := createRead(t, store, user, "")

			stored := setSite(t, store, user, saved, c.page)

			assertEqual(t, stored, SiteStored{Stored: true, NoteFilled: c.wantFilled})
			got := reload(t, store, user, saved.ID)
			assertEqual(t, got.Site, &c.wantSite)
			assertEqual(t, []string{got.Title, got.Note}, []string{"Page", c.wantNote})
			assertEqual(t, got.Tags, []string{"go"})
			assertEqual(t, []int64{got.CreatedAt, got.UpdatedAt}, []int64{100, 100})
		})
	}
}

func TestSetSiteKeepsANoteThatHasText(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	saved := createRead(t, store, user, "mine")

	stored := setSite(t, store, user, saved, Site{Title: "T", Description: "D"})

	assertEqual(t, stored, SiteStored{Stored: true, NoteFilled: false})
	got := reload(t, store, user, saved.ID)
	assertEqual(t, got.Site, &Site{Title: "T", Description: "D"})
	assertEqual(t, got.Note, "mine")
	assertEqual(t, got.UpdatedAt, int64(100))
}

func TestSetSiteCutsTheDetailsAtTheLimitsAndFillsTheNoteWithTheCutText(t *testing.T) {
	for name, extra := range map[string]int{"at the limits": 0, "one over": 1} {
		t.Run(name, func(t *testing.T) {
			store, pool := newStore(t)
			user := dbtest.InsertUser(t, pool, "henry")
			saved := createRead(t, store, user, "")
			page := Site{
				Title:       strings.Repeat("題", maxSiteTitleChars+extra),
				Description: " " + strings.Repeat("語", maxSiteDescriptionChars+extra) + "\n",
			}

			stored := setSite(t, store, user, saved, page)

			want := Site{Title: strings.Repeat("題", maxSiteTitleChars), Description: strings.Repeat("語", maxSiteDescriptionChars)}
			assertEqual(t, stored, SiteStored{Stored: true, NoteFilled: true})
			got := reload(t, store, user, saved.ID)
			assertEqual(t, got.Site, &want)
			assertEqual(t, got.Note, want.Description)
		})
	}
}

func TestSetSiteStoresNothingWhenThePageSaidNothing(t *testing.T) {
	for name, page := range map[string]Site{"empty": {}, "blank": {Title: "  ", Description: "\n\t"}} {
		t.Run(name, func(t *testing.T) {
			store, pool := newStore(t)
			user := dbtest.InsertUser(t, pool, "henry")
			saved := createRead(t, store, user, "")
			before := reload(t, store, user, saved.ID)

			stored := setSite(t, store, user, saved, page)

			assertEqual(t, stored, SiteStored{})
			assertEqual(t, reload(t, store, user, saved.ID), before)
		})
	}
}

func TestSetSiteLeavesABookmarkAloneThatIsNotTheOneThePageWasReadFor(t *testing.T) {
	cases := []struct {
		name    string
		request func(owner, other int64, saved Bookmark) SetSiteRequest
	}{
		{"another user's", func(_, other int64, saved Bookmark) SetSiteRequest {
			return SetSiteRequest{UserID: other, ID: saved.ID, URL: saved.URL}
		}},
		{"a URL that changed", func(owner, _ int64, saved Bookmark) SetSiteRequest {
			return SetSiteRequest{UserID: owner, ID: saved.ID, URL: "https://example.com/moved"}
		}},
		{"a missing one", func(owner, _ int64, saved Bookmark) SetSiteRequest {
			return SetSiteRequest{UserID: owner, ID: saved.ID + 1000, URL: saved.URL}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store, pool := newStore(t)
			owner := dbtest.InsertUser(t, pool, "henry")
			other := dbtest.InsertUser(t, pool, "mei")
			saved := createRead(t, store, owner, "")
			before := reload(t, store, owner, saved.ID)
			request := c.request(owner, other, saved)
			request.Site = Site{Title: "T", Description: "D"}

			stored, err := store.SetSite(bg, request)

			if err != nil {
				t.Fatal(err)
			}
			assertEqual(t, stored, SiteStored{})
			assertEqual(t, reload(t, store, owner, saved.ID), before)
		})
	}
}

func TestASecondSetSiteReplacesTheFirstAsAUnit(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	saved := createRead(t, store, user, "")
	steps := []struct {
		page       Site
		wantFilled bool
	}{
		{Site{Title: "T1", Description: "D1"}, true},
		{Site{Title: "T2"}, false},
		{Site{Description: "D3"}, false},
	}
	for _, step := range steps {
		stored := setSite(t, store, user, saved, step.page)

		assertEqual(t, stored, SiteStored{Stored: true, NoteFilled: step.wantFilled})
		got := reload(t, store, user, saved.ID)
		assertEqual(t, got.Site, &step.page)
		assertEqual(t, got.Note, "D1")
	}
}

func TestSetSiteReportsADatabaseFailureAndStoresNothing(t *testing.T) {
	page := Site{Title: "T", Description: "D"}
	set := func(store *Store, user int64, saved Bookmark) (SiteStored, error) {
		return store.SetSite(bg, SetSiteRequest{UserID: user, ID: saved.ID, URL: saved.URL, Site: page})
	}
	t.Run("closed", func(t *testing.T) {
		store, pool := newStore(t)
		user := dbtest.InsertUser(t, pool, "henry")
		saved := createRead(t, store, user, "")
		if err := pool.Close(); err != nil {
			t.Fatal(err)
		}
		stored, err := set(store, user, saved)
		if err == nil {
			t.Fatal("no error although the database is closed")
		}
		assertEqual(t, stored, SiteStored{})
	})
	t.Run("refused write", func(t *testing.T) {
		store, pool := newStore(t)
		user := dbtest.InsertUser(t, pool, "henry")
		saved := createRead(t, store, user, "")
		before := reload(t, store, user, saved.ID)
		if _, err := pool.Exec("CREATE TRIGGER refuse BEFORE UPDATE ON bookmarks BEGIN SELECT RAISE(ABORT, 'refused'); END"); err != nil {
			t.Fatal(err)
		}
		stored, err := set(store, user, saved)
		if err == nil {
			t.Fatal("no error although the database refused the write")
		}
		assertEqual(t, stored, SiteStored{})
		assertEqual(t, reload(t, store, user, saved.ID), before)
	})
}
