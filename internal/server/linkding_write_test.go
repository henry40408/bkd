package server

import (
	"context"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/henry40408/bkd/internal/bookmarks"
)

func bookmarkPath(id int64) string { return "/api/bookmarks/" + strconv.FormatInt(id, 10) + "/" }

func (a *testApp) storedBookmark(userID, id int64) bookmarks.Bookmark {
	a.t.Helper()
	stored, found, err := a.app.bookmarks.Get(context.Background(), userID, id)
	if err != nil || !found {
		a.t.Fatalf("bookmark %d: found %v, error %v", id, found, err)
	}
	return stored
}

func (a *testApp) bookmarkCount(userID int64) int {
	a.t.Helper()
	var count int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM bookmarks WHERE user_id = ?1", userID).Scan(&count); err != nil {
		a.t.Fatal(err)
	}
	return count
}

// assertFieldError checks the shape linkding gives a refused field.
func assertFieldError(t *testing.T, r reply, field, message string) {
	t.Helper()
	assertStatus(t, r, http.StatusBadRequest)
	want := map[string]any{field: []any{message}}
	if !reflect.DeepEqual(r.json(), want) {
		t.Errorf("body = %s, want %v", r.text, want)
	}
}

func assertDetail(t *testing.T, r reply, status int, detail string) {
	t.Helper()
	assertStatus(t, r, status)
	if got := r.json()["detail"]; got != detail {
		t.Errorf("detail = %v, want %q (body %s)", got, detail, r.text)
	}
}

func TestLinkdingCreatesABookmarkFromLinkdingsFields(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	r := app.send(withToken(post("/api/bookmarks/", map[string]any{
		"url": "https://example.com/a", "title": "A title", "description": "What it is",
		"notes": "my words", "tag_names": []string{"Go", "web"},
	}), token))
	assertStatus(t, r, http.StatusCreated)
	body := r.json()
	if body["title"] != "A title" || body["description"] != "What it is" || body["notes"] != "my words" ||
		body["website_description"] != "What it is" || body["website_title"] != nil {
		t.Errorf("answer = %s", r.text)
	}
	stored := app.storedBookmark(user, int64(body["id"].(float64)))
	if stored.URL != "https://example.com/a" || stored.Note != "my words" ||
		!reflect.DeepEqual(stored.Tags, []string{"go", "web"}) || stored.Site.Value() != (bookmarks.Site{Description: "What it is"}) {
		t.Errorf("stored = %+v", stored)
	}
}

func TestLinkdingCreateWithoutATitleUsesTheAddress(t *testing.T) {
	app := runningApp(t)
	token, _ := app.linkdingUser("henry")
	for i, body := range []map[string]any{
		{"url": "https://example.com/none"},
		{"url": "https://example.com/blank", "title": "  "},
		{"url": "https://example.com/null", "title": nil},
	} {
		r := app.send(withToken(post("/api/bookmarks/", body), token))
		assertStatus(t, r, http.StatusCreated)
		if r.json()["title"] != body["url"] {
			t.Errorf("case %d: title = %v", i, r.json()["title"])
		}
	}
	long := "https://example.com/" + strings.Repeat("x", 400)
	r := app.send(withToken(post("/api/bookmarks/", map[string]any{"url": long}), token))
	assertStatus(t, r, http.StatusCreated)
	if got, _ := r.json()["title"].(string); got != long[:300] {
		t.Errorf("a long address gave a title of %d characters", len(got))
	}
}

func TestLinkdingCreateOfABookmarkedAddressChangesThatBookmarkAndKeepsWhatIsNotSent(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	first := app.saveBookmark(user, bookmarks.Input{
		URL: "https://example.com/a", Title: "old", Note: "keep me", Tags: []string{"a"},
		Site: bookmarks.Site{Title: "Page", Description: "old description"},
	}, 5)

	r := app.send(withToken(post("/api/bookmarks/", map[string]any{"url": "https://example.com/a/", "title": "new"}), token))
	assertStatus(t, r, http.StatusCreated)
	if r.json()["id"] != float64(first.ID) || r.json()["url"] != "https://example.com/a" {
		t.Fatalf("answer = %s", r.text)
	}
	stored := app.storedBookmark(user, first.ID)
	if stored.Title != "new" || stored.Note != "keep me" || !reflect.DeepEqual(stored.Tags, []string{"a"}) ||
		stored.Site.Value() != (bookmarks.Site{Title: "Page", Description: "old description"}) {
		t.Errorf("stored = %+v", stored)
	}
	if app.bookmarkCount(user) != 1 {
		t.Errorf("%d bookmarks, want 1", app.bookmarkCount(user))
	}

	r = app.send(withToken(post("/api/bookmarks/", map[string]any{
		"url": "https://example.com/a", "description": "", "notes": "", "tag_names": []string{},
	}), token))
	assertStatus(t, r, http.StatusCreated)
	stored = app.storedBookmark(user, first.ID)
	if stored.Note != "" || len(stored.Tags) != 0 || stored.Site.Value() != (bookmarks.Site{Title: "Page"}) {
		t.Errorf("an empty value did not clear: %+v", stored)
	}
}

func TestLinkdingCreateRefusesWhatBkdWouldRefuseInLinkdingsWords(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	tags := make([]string, 21)
	for i := range tags {
		tags[i] = "t" + strconv.Itoa(i)
	}
	cases := []struct {
		name          string
		body          map[string]any
		field, wanted string
	}{
		{"no url", map[string]any{"title": "t"}, "url", messageRequired},
		{"null url", map[string]any{"url": nil}, "url", messageRequired},
		{"blank url", map[string]any{"url": "  "}, "url", messageBlank},
		{"scheme", map[string]any{"url": "ftp://example.com/a"}, "url", messageBadURL},
		{"no host", map[string]any{"url": "https://"}, "url", messageBadURL},
		{"long url", map[string]any{"url": "https://example.com/" + strings.Repeat("a", 2048)}, "url", messageTooLong},
		{"long title", map[string]any{"url": "https://example.com/a", "title": strings.Repeat("t", 301)}, "title", messageTooLong},
		{"long notes", map[string]any{"url": "https://example.com/a", "notes": strings.Repeat("n", 5001)}, "notes", messageTooLong},
		{"long tag", map[string]any{"url": "https://example.com/a", "tag_names": []string{strings.Repeat("t", 51)}}, "tag_names", "A tag name is too long."},
		{"control tag", map[string]any{"url": "https://example.com/a", "tag_names": []string{"a\x07b"}}, "tag_names", "A tag name may not contain control characters."},
		{"many tags", map[string]any{"url": "https://example.com/a", "tag_names": tags}, "tag_names", "Too many tags."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFieldError(t, app.send(withToken(post("/api/bookmarks/", tc.body), token)), tc.field, tc.wanted)
		})
	}
	if app.bookmarkCount(user) != 0 {
		t.Errorf("a refused request saved %d bookmarks", app.bookmarkCount(user))
	}
}

func TestLinkdingWritesTakeTheLimitsAtTheirExactValue(t *testing.T) {
	app := runningApp(t)
	token, _ := app.linkdingUser("henry")
	atLimit := "https://example.com/" + strings.Repeat("a", 2048-len("https://example.com/"))
	tags := make([]string, 20)
	for i := range tags {
		tags[i] = "t" + strconv.Itoa(i)
	}
	r := app.send(withToken(post("/api/bookmarks/", map[string]any{
		"url": atLimit, "title": strings.Repeat("t", 300), "notes": strings.Repeat("n", 5000),
		"tag_names": append(tags[:19:19], strings.Repeat("t", 50)),
	}), token))
	assertStatus(t, r, http.StatusCreated)
}

func TestLinkdingWritesAnswerAMalformedBodyInDetailJSON(t *testing.T) {
	app := runningApp(t)
	token, _ := app.linkdingUser("henry")
	for _, tc := range []struct {
		name   string
		body   sent
		status int
	}{
		{"not json type", post("/api/bookmarks/", nil).withRaw("text/plain", `{"url":"https://a.b"}`), http.StatusUnsupportedMediaType},
		{"syntax", post("/api/bookmarks/", nil).withRaw("application/json", `{"url":`), http.StatusBadRequest},
		{"shape", post("/api/bookmarks/", nil).withRaw("application/json", `{"url": 5}`), http.StatusUnprocessableEntity},
		{"too large", post("/api/bookmarks/", nil).withRaw("application/json", strings.Repeat("a", maxBodyBytes+1)), http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := app.send(withToken(tc.body, token))
			assertStatus(t, r, tc.status)
			if detail, _ := r.json()["detail"].(string); detail == "" {
				t.Errorf("body = %q, want a detail", r.text)
			}
		})
	}
}

func TestLinkdingPutNeedsTheAddressAndKeepsWhatIsNotSent(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	saved := app.saveBookmark(user, bookmarks.Input{URL: "https://example.com/a", Title: "old", Note: "keep", Tags: []string{"a"}}, 5)

	assertFieldError(t, app.send(withToken(put(bookmarkPath(saved.ID), map[string]any{"title": "new"}), token)), "url", messageRequired)
	assertFieldError(t, app.send(withToken(put(bookmarkPath(saved.ID), map[string]any{"url": " "}), token)), "url", messageBlank)
	if app.storedBookmark(user, saved.ID).Title != "old" {
		t.Fatal("a refused PUT changed the bookmark")
	}

	r := app.send(withToken(put(bookmarkPath(saved.ID), map[string]any{"url": "https://example.com/a", "title": "new"}), token))
	assertStatus(t, r, http.StatusOK)
	stored := app.storedBookmark(user, saved.ID)
	if stored.Title != "new" || stored.Note != "keep" || !reflect.DeepEqual(stored.Tags, []string{"a"}) {
		t.Errorf("stored = %+v", stored)
	}
}

func TestLinkdingPatchChangesOnlyWhatIsSent(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	saved := app.saveBookmark(user, bookmarks.Input{URL: "https://example.com/a", Title: "title", Note: "note", Tags: []string{"a", "b"}}, 5)
	path := bookmarkPath(saved.ID)

	for _, body := range []map[string]any{
		{}, {"title": "  "}, {"title": nil, "notes": nil, "tag_names": nil, "description": nil, "url": nil},
		{"is_archived": true, "unread": true, "shared": true},
	} {
		assertStatus(t, app.send(withToken(patch(path, body), token)), http.StatusOK)
		stored := app.storedBookmark(user, saved.ID)
		if stored.URL != saved.URL || stored.Title != "title" || stored.Note != "note" || len(stored.Tags) != 2 {
			t.Fatalf("PATCH %v changed the bookmark: %+v", body, stored)
		}
	}

	r := app.send(withToken(patch(path, map[string]any{"notes": "", "tag_names": []string{"c"}}), token))
	assertStatus(t, r, http.StatusOK)
	stored := app.storedBookmark(user, saved.ID)
	if stored.Note != "" || !reflect.DeepEqual(stored.Tags, []string{"c"}) || stored.Title != "title" {
		t.Errorf("stored = %+v", stored)
	}
	if r.json()["notes"] != "" || r.json()["title"] != "title" {
		t.Errorf("answer = %s", r.text)
	}
}

func TestLinkdingPatchRefusesABlankAddressButNotAMissingOne(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	saved := app.saveBookmark(user, bookmarks.Input{URL: "https://example.com/a", Title: "t"}, 5)
	assertFieldError(t, app.send(withToken(patch(bookmarkPath(saved.ID), map[string]any{"url": ""}), token)), "url", messageBlank)
	assertFieldError(t, app.send(withToken(patch(bookmarkPath(saved.ID), map[string]any{"url": "mailto:a@b.c"}), token)), "url", messageBadURL)
}

func TestLinkdingChangingTheDescriptionKeepsThePageTitleAndAnEmptyOneClears(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	saved := app.saveBookmark(user, bookmarks.Input{
		URL: "https://example.com/a", Title: "t", Site: bookmarks.Site{Title: "Page", Description: "old"},
	}, 5)
	path := bookmarkPath(saved.ID)

	assertStatus(t, app.send(withToken(patch(path, map[string]any{"description": "new"}), token)), http.StatusOK)
	if got := app.storedBookmark(user, saved.ID).Site.Value(); got != (bookmarks.Site{Title: "Page", Description: "new"}) {
		t.Errorf("site = %+v", got)
	}
	assertStatus(t, app.send(withToken(put(path, map[string]any{"url": saved.URL, "description": ""}), token)), http.StatusOK)
	if got := app.storedBookmark(user, saved.ID).Site.Value(); got != (bookmarks.Site{Title: "Page"}) {
		t.Errorf("site = %+v", got)
	}
	assertStatus(t, app.send(withToken(patch(path, map[string]any{"title": "other"}), token)), http.StatusOK)
	if got := app.storedBookmark(user, saved.ID).Site.Value(); got != (bookmarks.Site{Title: "Page"}) {
		t.Errorf("a change without a description touched the site: %+v", got)
	}
}

func TestLinkdingChangingTheAddressDropsTheDetailsOfTheOldPage(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	saved := app.saveBookmark(user, bookmarks.Input{
		URL: "https://example.com/a", Title: "t", Site: bookmarks.Site{Title: "Page", Description: "old"},
	}, 5)
	path := bookmarkPath(saved.ID)

	assertStatus(t, app.send(withToken(patch(path, map[string]any{"url": "https://example.com/b"}), token)), http.StatusOK)
	stored := app.storedBookmark(user, saved.ID)
	if stored.URL != "https://example.com/b" || stored.Site != nil {
		t.Fatalf("stored = %+v, site %+v", stored, stored.Site)
	}

	assertStatus(t, app.send(withToken(patch(path, map[string]any{"url": "https://example.com/c", "description": "fresh"}), token)), http.StatusOK)
	if got := app.storedBookmark(user, saved.ID).Site.Value(); got != (bookmarks.Site{Description: "fresh"}) {
		t.Errorf("site = %+v", got)
	}
}

func TestLinkdingChangeRefusesTheAddressOfAnotherBookmark(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	_, other := app.linkdingUser("other")
	first := app.saveBookmark(user, bookmarks.Input{URL: "https://example.com/a", Title: "first"}, 5)
	second := app.saveBookmark(user, bookmarks.Input{URL: "https://example.com/b", Title: "second"}, 5)
	app.saveBookmark(other, bookmarks.Input{URL: "https://example.com/c", Title: "theirs"}, 5)
	path := bookmarkPath(second.ID)

	for _, address := range []string{first.URL, first.URL + "/"} {
		assertFieldError(t, app.send(withToken(put(path, map[string]any{"url": address}), token)), "url", messageDuplicate)
		assertFieldError(t, app.send(withToken(patch(path, map[string]any{"url": address}), token)), "url", messageDuplicate)
	}
	if app.storedBookmark(user, second.ID).URL != second.URL {
		t.Fatal("a refused change moved the bookmark")
	}

	assertStatus(t, app.send(withToken(put(path, map[string]any{"url": second.URL, "title": "same address"}), token)), http.StatusOK)
	assertStatus(t, app.send(withToken(put(path, map[string]any{"url": "https://example.com/c"}), token)), http.StatusOK)
}

func TestLinkdingDeletes(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	_, other := app.linkdingUser("other")
	mine := app.saveBookmark(user, bookmarks.Input{URL: "https://example.com/a", Title: "mine", Tags: []string{"x"}}, 5)
	theirs := app.saveBookmark(other, bookmarks.Input{URL: "https://example.com/b", Title: "theirs"}, 5)

	r := app.send(withToken(del(bookmarkPath(mine.ID)), token))
	assertStatus(t, r, http.StatusNoContent)
	if r.text != "" {
		t.Errorf("body = %q, want none", r.text)
	}
	if app.bookmarkCount(user) != 0 {
		t.Error("the bookmark is still there")
	}
	for _, path := range []string{bookmarkPath(mine.ID), bookmarkPath(theirs.ID), "/api/bookmarks/abc/", "/api/bookmarks/0/"} {
		assertDetail(t, app.send(withToken(del(path), token)), http.StatusNotFound, messageNotFound)
	}
	if app.bookmarkCount(other) != 1 {
		t.Error("a token deleted another user's bookmark")
	}
}

func TestLinkdingWritesNeverTouchAnotherUsersBookmark(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	_, other := app.linkdingUser("other")
	theirs := app.saveBookmark(other, bookmarks.Input{URL: "https://example.com/a", Title: "theirs"}, 5)

	for _, request := range []sent{
		put(bookmarkPath(theirs.ID), map[string]any{"url": "https://example.com/a", "title": "mine now"}),
		patch(bookmarkPath(theirs.ID), map[string]any{"title": "mine now"}),
		patch("/api/bookmarks/abc/", map[string]any{}),
	} {
		assertDetail(t, app.send(withToken(request, token)), http.StatusNotFound, messageNotFound)
	}
	if app.storedBookmark(other, theirs.ID).Title != "theirs" {
		t.Fatal("another user's bookmark was changed")
	}

	r := app.send(withToken(post("/api/bookmarks/", map[string]any{"url": "https://example.com/a", "title": "mine"}), token))
	assertStatus(t, r, http.StatusCreated)
	if r.json()["id"] == float64(theirs.ID) || app.bookmarkCount(user) != 1 || app.storedBookmark(other, theirs.ID).Title != "theirs" {
		t.Errorf("the same address of another user was taken as a duplicate: %s", r.text)
	}
}

func TestLinkdingWritesAcceptWhatBkdHasNoCounterpartForAndIgnoreIt(t *testing.T) {
	app := runningApp(t)
	token, _ := app.linkdingUser("henry")
	r := app.send(withToken(post("/api/bookmarks/?disable_scraping", map[string]any{
		"url": "https://example.com/a", "is_archived": true, "unread": true, "shared": true,
	}), token))
	assertStatus(t, r, http.StatusCreated)
	body := r.json()
	if body["is_archived"] != false || body["unread"] != false || body["shared"] != false {
		t.Errorf("answer = %s", r.text)
	}
}

func TestLinkdingWritesAnswerOnlyToATokenAndIgnoreTheOrigin(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	saved := app.saveBookmark(user, bookmarks.Input{URL: "https://example.com/a", Title: "t"}, 5)
	session := app.sessionFor("cookie-user")
	body := map[string]any{"url": "https://example.com/z"}
	for _, request := range []sent{
		post("/api/bookmarks/", body), put(bookmarkPath(saved.ID), body), patch(bookmarkPath(saved.ID), body), del(bookmarkPath(saved.ID)),
	} {
		assertStatus(t, app.send(request), http.StatusUnauthorized)
		assertStatus(t, app.send(request.as(session)), http.StatusUnauthorized)
	}
	if app.bookmarkCount(user) != 1 || app.storedBookmark(user, saved.ID).URL != saved.URL {
		t.Fatal("a request without a token changed something")
	}

	r := app.send(withToken(post("/api/bookmarks/", body), token).originated("chrome-extension://abcdef"))
	assertStatus(t, r, http.StatusCreated)
}

func TestLinkdingWritesAnswer500WithoutTheCauseWhenTheDatabaseFails(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	saved := app.saveBookmark(user, bookmarks.Input{URL: "https://example.com/a", Title: "t"}, 5)
	body := map[string]any{"url": "https://example.com/a", "title": "x"}
	requests := []sent{
		post("/api/bookmarks/", map[string]any{"url": "https://example.com/new"}), post("/api/bookmarks/", body),
		put(bookmarkPath(saved.ID), body), patch(bookmarkPath(saved.ID), body), del(bookmarkPath(saved.ID)),
	}

	// Reading works and every change is refused, as a failing disk would.
	for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
		app.exec("CREATE TRIGGER refuse_" + event + " BEFORE " + event + " ON bookmarks BEGIN SELECT RAISE(ABORT, 'refused'); END")
	}
	for _, request := range requests {
		assertDetail(t, app.send(withToken(request, token)), http.StatusInternalServerError, "internal error")
	}

	// Not even reading works.
	app.breakTable("bookmarks")
	for _, request := range requests {
		assertDetail(t, app.send(withToken(request, token)), http.StatusInternalServerError, "internal error")
	}
}

func TestLinkdingTwoPostsOfOneAddressAtOnceSaveOneBookmark(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	const clients = 8
	statuses := make(chan int, clients)
	var wg sync.WaitGroup
	for range clients {
		wg.Go(func() {
			statuses <- app.send(withToken(post("/api/bookmarks/", map[string]any{"url": "https://example.com/a"}), token)).status
		})
	}
	wg.Wait()
	close(statuses)
	for status := range statuses {
		if status != http.StatusCreated {
			t.Errorf("status = %d, want 201", status)
		}
	}
	if app.bookmarkCount(user) != 1 {
		t.Errorf("%d bookmarks, want 1", app.bookmarkCount(user))
	}
}

func TestLinkdingChangeOnlyChecksTheAddressWhenItChanges(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	saved := app.saveBookmark(user, bookmarks.Input{URL: "https://example.com/a", Title: "t", Site: bookmarks.Site{Title: "Page"}}, 5)
	// bkd's own API lets two bookmarks share an address; the later one stays editable.
	later := app.saveBookmark(user, bookmarks.Input{URL: "https://example.com/z", Title: "later"}, 6)
	app.exec("UPDATE bookmarks SET url = ?1 WHERE id = ?2", saved.URL, later.ID)
	assertStatus(t, app.send(withToken(patch(bookmarkPath(later.ID), map[string]any{"url": saved.URL, "title": "edited"}), token)), http.StatusOK)
	if app.storedBookmark(user, later.ID).Title != "edited" {
		t.Error("an unchanged address was refused as a duplicate")
	}

	// The same page under another spelling is the bookmark itself, not a duplicate, and
	// a new address forgets the page details, as in bkd's own API.
	assertStatus(t, app.send(withToken(patch(bookmarkPath(saved.ID), map[string]any{"url": saved.URL + "/"}), token)), http.StatusOK)
	stored := app.storedBookmark(user, saved.ID)
	if stored.URL != saved.URL+"/" || stored.Site != nil {
		t.Errorf("stored = %+v, site %+v", stored, stored.Site)
	}
}

func TestLinkdingFaultOfLeavesWhatItDoesNotKnowAlone(t *testing.T) {
	for _, fault := range []*apiError{badRequest("something new"), internal(nil), conflict("taken")} {
		if got := linkdingFaultOf(fault); got != fault {
			t.Errorf("%+v became %+v", fault, got)
		}
	}
	if got := linkdingFaultOf(notFound()); got.status != http.StatusNotFound || got.message != messageNotFound {
		t.Errorf("not found became %+v", got)
	}
}
