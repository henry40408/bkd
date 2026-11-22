package bookmarks

import (
	"strings"
	"testing"

	"github.com/henry40408/bkd/internal/dbtest"
)

// siteRequest asks to store site for the bookmark, read from its own address.
func siteRequest(user int64, saved Bookmark, site Site) SiteRequest {
	return SiteRequest{UserID: user, ID: saved.ID, URL: saved.URL, Site: site}
}

func TestSiteIsNilUntilItIsRead(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	saved := create(t, store, user, valid(t, "Page"), 100)
	if saved.Site != nil {
		t.Fatalf("site = %+v, want nil", saved.Site)
	}
}

func TestSetSiteStoresWhatThePageSaysAndLeavesTheRestAlone(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	input := valid(t, "Page", "reading")
	input.Note = "my note"
	saved := create(t, store, user, input, 100)

	updated, found, err := store.SetSite(bg, siteRequest(user, saved, Site{Title: "Og title", Description: "About it", FetchedAt: 500}))
	if err != nil || !found {
		t.Fatalf("found = %v, err = %v", found, err)
	}
	assertEqual(t, *updated.Site, Site{Title: "Og title", Description: "About it", FetchedAt: 500})
	assertEqual(t, []any{updated.Title, updated.Note, updated.Tags, updated.UpdatedAt},
		[]any{"Page", "my note", []string{"reading"}, int64(100)})
	listed, _, err := store.Get(bg, user, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, listed.Site, updated.Site)
}

func TestSetSiteReplacesWhatWasStoredBefore(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	saved := create(t, store, user, valid(t, "Page"), 100)
	for _, site := range []Site{{Title: "old", Description: "old text", FetchedAt: 1}, {Title: "new", FetchedAt: 2}} {
		if _, _, err := store.SetSite(bg, siteRequest(user, saved, site)); err != nil {
			t.Fatal(err)
		}
	}
	got, _, _ := store.Get(bg, user, saved.ID)
	assertEqual(t, *got.Site, Site{Title: "new", Description: "", FetchedAt: 2})
}

func TestSetSiteCutsOnlyWhatIsOverTheLimits(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	saved := create(t, store, user, valid(t, "Page"), 100)
	cases := []struct {
		name                       string
		title, description         int
		wantTitleLen, wantDescrLen int
	}{
		{"at the limits", maxSiteTitleChars, maxSiteDescriptionChars, maxSiteTitleChars, maxSiteDescriptionChars},
		{"one over", maxSiteTitleChars + 1, maxSiteDescriptionChars + 1, maxSiteTitleChars, maxSiteDescriptionChars},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			site := Site{Title: strings.Repeat("語", c.title), Description: strings.Repeat("d", c.description), FetchedAt: 9}
			updated, _, err := store.SetSite(bg, siteRequest(user, saved, site))
			if err != nil {
				t.Fatal(err)
			}
			assertEqual(t, []int{len([]rune(updated.Site.Title)), len([]rune(updated.Site.Description))},
				[]int{c.wantTitleLen, c.wantDescrLen})
		})
	}
}

func TestSetSiteIgnoresAnotherUsersBookmark(t *testing.T) {
	store, pool := newStore(t)
	owner := dbtest.InsertUser(t, pool, "owner")
	other := dbtest.InsertUser(t, pool, "other")
	saved := create(t, store, owner, valid(t, "Page"), 100)

	_, found, err := store.SetSite(bg, siteRequest(other, saved, Site{Title: "x", FetchedAt: 1}))
	if err != nil || found {
		t.Fatalf("found = %v, err = %v", found, err)
	}
	got, _, _ := store.Get(bg, owner, saved.ID)
	if got.Site != nil {
		t.Fatalf("another user stored %+v", got.Site)
	}
}

func TestSetSiteIgnoresAMissingBookmark(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	_, found, err := store.SetSite(bg, SiteRequest{UserID: user, ID: 999, URL: "https://a.test/", Site: Site{Title: "x"}})
	if err != nil || found {
		t.Fatalf("found = %v, err = %v", found, err)
	}
}

func TestSetSiteIgnoresAPageReadFromAnotherAddress(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	saved := create(t, store, user, valid(t, "Page"), 100)
	request := siteRequest(user, saved, Site{Title: "x", FetchedAt: 1})
	request.URL = "https://elsewhere.test/"

	_, found, err := store.SetSite(bg, request)
	if err != nil || found {
		t.Fatalf("found = %v, err = %v", found, err)
	}
	got, _, _ := store.Get(bg, user, saved.ID)
	if got.Site != nil {
		t.Fatalf("stored %+v for a page that is not the bookmark's", got.Site)
	}
}

func TestChangingTheURLForgetsTheSiteButEditingTheTitleKeepsIt(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	saved := create(t, store, user, valid(t, "Page"), 100)
	if _, _, err := store.SetSite(bg, siteRequest(user, saved, Site{Title: "Og", Description: "d", FetchedAt: 5})); err != nil {
		t.Fatal(err)
	}

	renamed := valid(t, "Page")
	renamed.Title = "Renamed"
	kept, _, err := store.Update(bg, UpdateRequest{UserID: user, ID: saved.ID, Bookmark: renamed, Now: 200})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, kept.Site, &Site{Title: "Og", Description: "d", FetchedAt: 5})

	moved := renamed
	moved.URL = "https://example.com/moved"
	dropped, _, err := store.Update(bg, UpdateRequest{UserID: user, ID: saved.ID, Bookmark: moved, Now: 300})
	if err != nil {
		t.Fatal(err)
	}
	if dropped.Site != nil {
		t.Fatalf("site = %+v after the URL changed", dropped.Site)
	}
}
