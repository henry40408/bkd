package bookmarks

import (
	"strings"
	"testing"

	"github.com/henry40408/bkd/internal/dbtest"
)

// withSite is bookmark as the form sends it after reading its page.
func withSite(bookmark Valid, site Site) Valid {
	bookmark.Site = site
	return bookmark
}

func TestSiteIsNilWhenThePageWasNotRead(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	saved := create(t, store, user, valid(t, "Page"), 100)
	if saved.Site != nil {
		t.Fatalf("site = %+v, want nil", saved.Site)
	}
	assertEqual(t, saved.Site.Value(), Site{})
}

func TestCreateStoresWhatThePageSaysApartFromTheTitleAndNote(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	saved := create(t, store, user, withSite(valid(t, "Mine"), Site{Title: "Og title", Description: "About it"}), 100)
	assertEqual(t, *saved.Site, Site{Title: "Og title", Description: "About it"})
	assertEqual(t, []string{saved.Title, saved.Note}, []string{"Mine", ""})

	got, _, err := store.Get(bg, user, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, got.Site, saved.Site)
}

func TestAPageThatSaidOnlyOneThingKeepsItAndOneThatSaidNothingKeepsNothing(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	cases := map[string]struct {
		site Site
		want *Site
	}{
		"title only":       {Site{Title: "T"}, &Site{Title: "T"}},
		"description only": {Site{Description: "D"}, &Site{Description: "D"}},
		"nothing":          {Site{}, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			saved := create(t, store, user, withSite(valid(t, name), c.site), 100)
			assertEqual(t, saved.Site, c.want)
		})
	}
}

func TestValidateCutsOnlyWhatIsOverTheLimitsAndTrimsIt(t *testing.T) {
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
			checked, err := Input{
				URL: "https://example.com/", Title: "Page",
				Site: Site{Title: " " + strings.Repeat("語", c.title) + " ", Description: strings.Repeat("d", c.description) + "\n"},
			}.Validate()
			if err != nil {
				t.Fatal(err)
			}
			assertEqual(t, []int{len([]rune(checked.Site.Title)), len([]rune(checked.Site.Description))},
				[]int{c.wantTitleLen, c.wantDescrLen})
		})
	}
}

func TestUpdateKeepsTheSiteWhenNoneIsSentAndReplacesItWhenTheURLChanges(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	saved := create(t, store, user, withSite(valid(t, "Page"), Site{Title: "Og", Description: "d"}), 100)

	renamed := valid(t, "Page")
	renamed.Title = "Renamed"
	kept, _, err := store.Update(bg, UpdateRequest{UserID: user, ID: saved.ID, Bookmark: renamed, Now: 200})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, kept.Site, &Site{Title: "Og", Description: "d"})

	moved := withSite(renamed, Site{Title: "Moved", Description: "elsewhere"})
	moved.URL = "https://example.com/moved"
	replaced, _, err := store.Update(bg, UpdateRequest{UserID: user, ID: saved.ID, Bookmark: moved, Now: 300})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, replaced.Site, &Site{Title: "Moved", Description: "elsewhere"})
}

// A form that read the page again sends the new details with the unchanged URL.
// Both columns are replaced together, so a page that lost its description does
// not keep the one it had.
func TestUpdateWithTheSameURLReplacesBothSiteColumnsWhenDetailsAreSent(t *testing.T) {
	cases := []struct {
		name string
		sent Site
	}{
		{"both", Site{Title: "New", Description: "fresh"}},
		{"only a title", Site{Title: "New"}},
		{"only a description", Site{Description: "fresh"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store, pool := newStore(t)
			user := dbtest.InsertUser(t, pool, "henry")
			saved := create(t, store, user, withSite(valid(t, "Page"), Site{Title: "Og", Description: "d"}), 100)

			updated, _, err := store.Update(bg, UpdateRequest{
				UserID: user, ID: saved.ID, Bookmark: withSite(valid(t, "Page"), c.sent), Now: 200,
			})
			if err != nil {
				t.Fatal(err)
			}
			assertEqual(t, updated.Site, &c.sent)
		})
	}
}

func TestUpdateWithTheSameURLAndNoDetailsLeavesAnUnreadBookmarkUnread(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	saved := create(t, store, user, valid(t, "Page"), 100)

	updated, _, err := store.Update(bg, UpdateRequest{UserID: user, ID: saved.ID, Bookmark: valid(t, "Page"), Now: 200})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Site != nil {
		t.Fatalf("site = %+v, want none", updated.Site)
	}
}

// ReplaceSite is how a caller says "the page's details are now these", empty or
// not: without it an empty Site means "none were sent" and keeps what is stored.
func TestUpdateWithReplaceSiteStoresAnEmptySiteAndKeepsTheOtherColumnAlone(t *testing.T) {
	cases := []struct {
		name string
		sent Site
		want *Site
	}{
		{"nothing left", Site{}, nil},
		{"only the title left", Site{Title: "Og"}, &Site{Title: "Og"}},
		{"only the description left", Site{Description: "d"}, &Site{Description: "d"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store, pool := newStore(t)
			user := dbtest.InsertUser(t, pool, "henry")
			saved := create(t, store, user, withSite(valid(t, "Page"), Site{Title: "Og", Description: "d"}), 100)

			updated, _, err := store.Update(bg, UpdateRequest{
				UserID: user, ID: saved.ID, Bookmark: withSite(valid(t, "Page"), c.sent), Now: 200, ReplaceSite: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			assertEqual(t, updated.Site, c.want)
		})
	}
}

func TestChangingTheURLToAPageThatSaysNothingForgetsTheOldDetails(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	saved := create(t, store, user, withSite(valid(t, "Page"), Site{Title: "Og", Description: "d"}), 100)

	moved := valid(t, "Page")
	moved.URL = "https://example.com/moved"
	dropped, _, err := store.Update(bg, UpdateRequest{UserID: user, ID: saved.ID, Bookmark: moved, Now: 200})
	if err != nil {
		t.Fatal(err)
	}
	if dropped.Site != nil {
		t.Fatalf("site = %+v after the URL changed", dropped.Site)
	}
}

func TestAnImportedBookmarkHasNoSite(t *testing.T) {
	store, pool := newStore(t)
	user := dbtest.InsertUser(t, pool, "henry")
	added, err := store.Import(bg, user, []ImportEntry{{Bookmark: valid(t, "Imported"), CreatedAt: 50}})
	if err != nil || added != 1 {
		t.Fatalf("added = %d, err = %v", added, err)
	}
	list, err := store.List(bg, user, nil, NewPage(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, len(list), 1)
	if list[0].Site != nil {
		t.Fatalf("site = %+v", list[0].Site)
	}
}
