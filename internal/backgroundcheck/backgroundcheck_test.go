package backgroundcheck

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/henry40408/bkd/internal/dbtest"
	"github.com/henry40408/bkd/internal/linkcheck"
	"github.com/henry40408/bkd/internal/users"
)

var now = time.Unix(10_000_000, 0)

func assertEqual(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func addBookmark(t *testing.T, pool *sql.DB, userID int64, address string, checkedAt *int64) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(
		`INSERT INTO bookmarks (user_id, url, title, created_at, updated_at, link_checked_at)
		 VALUES (?1, ?2, 't', 0, 0, ?3) RETURNING id`, userID, address, checkedAt).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func optIn(t *testing.T, pool *sql.DB, userID int64) {
	t.Helper()
	if err := users.SetLinkCheckEnabled(context.Background(), pool, userID, true); err != nil {
		t.Fatal(err)
	}
}

// chooseUserAgent sets the User-Agent and keeps the user opted in.
func chooseUserAgent(t *testing.T, pool *sql.DB, userID int64, agent string) {
	t.Helper()
	change := users.LinkCheckChange{Enabled: true, UserAgent: &agent}
	if err := users.SetLinkCheck(context.Background(), pool, userID, change); err != nil {
		t.Fatal(err)
	}
}

func at(seconds int64) *int64 { return &seconds }

func ids(items []due) []int64 {
	found := []int64{}
	for _, item := range items {
		found = append(found, item.id)
	}
	return found
}

func dueIDs(t *testing.T, pool *sql.DB, limit int) []int64 {
	t.Helper()
	items, err := dueBookmarks(context.Background(), pool, now, limit)
	if err != nil {
		t.Fatal(err)
	}
	return ids(items)
}

func newTestWorker(pool *sql.DB, checker *linkcheck.Checker) *worker {
	return newWorker(Config{DB: pool, Checker: checker, Batch: 10, Now: func() time.Time { return now }})
}

func TestOnlyUsersWhoOptedInAndAreActiveAreChecked(t *testing.T) {
	pool := dbtest.Open(t)
	off := dbtest.InsertUser(t, pool, "quiet")
	on := dbtest.InsertUser(t, pool, "opted")
	disabled := dbtest.InsertUser(t, pool, "gone")
	optIn(t, pool, on)
	optIn(t, pool, disabled)
	if _, err := pool.Exec("UPDATE users SET disabled_at = 1 WHERE id = ?1", disabled); err != nil {
		t.Fatal(err)
	}
	addBookmark(t, pool, off, "https://a.test/", nil)
	addBookmark(t, pool, disabled, "https://b.test/", nil)
	wanted := addBookmark(t, pool, on, "https://c.test/", nil)

	assertEqual(t, dueIDs(t, pool, 10), []int64{wanted})
}

func TestARecentResultIsLeftAloneUntilAWeekHasPassed(t *testing.T) {
	pool := dbtest.Open(t)
	user := dbtest.InsertUser(t, pool, "mei")
	optIn(t, pool, user)
	week := int64(recheckAfter / time.Second)
	addBookmark(t, pool, user, "https://fresh.test/", at(now.Unix()-week+1))
	old := addBookmark(t, pool, user, "https://stale.test/", at(now.Unix()-week-1))

	assertEqual(t, dueIDs(t, pool, 10), []int64{old})
}

func TestNeverCheckedGoesFirstThenTheLongestUnchecked(t *testing.T) {
	pool := dbtest.Open(t)
	user := dbtest.InsertUser(t, pool, "mei")
	optIn(t, pool, user)
	older := addBookmark(t, pool, user, "https://a.test/", at(100))
	oldest := addBookmark(t, pool, user, "https://b.test/", at(50))
	never := addBookmark(t, pool, user, "https://c.test/", nil)

	assertEqual(t, dueIDs(t, pool, 10), []int64{never, oldest, older})
}

func TestUsersTakeTurnsAndTheBatchIsALimit(t *testing.T) {
	pool := dbtest.Open(t)
	big := dbtest.InsertUser(t, pool, "big")
	small := dbtest.InsertUser(t, pool, "small")
	optIn(t, pool, big)
	optIn(t, pool, small)
	var bigIDs []int64
	for n := range 5 {
		bigIDs = append(bigIDs, addBookmark(t, pool, big, "https://big"+strconv.Itoa(n)+".test/", nil))
	}
	only := addBookmark(t, pool, small, "https://small.test/", nil)

	due := dueIDs(t, pool, 3)
	assertEqual(t, len(due), 3)
	assertEqual(t, due[:2], []int64{bigIDs[0], only})
}

func TestASiteThatSaysNothingIsLeftAloneForTwiceAsLongEachTime(t *testing.T) {
	var waits []int64
	for strikes := 1; strikes <= 5; strikes++ {
		waits = append(waits, int64(cooldownFor(strikes, nil)/time.Second))
	}
	assertEqual(t, waits, []int64{60, 120, 240, 480, 960})
	assertEqual(t, cooldownFor(100, nil), maxCooldown)
}

func TestWhatASiteAsksForIsUsedButNeverMoreThanADay(t *testing.T) {
	asked := func(seconds int) time.Duration {
		wait := time.Duration(seconds) * time.Second
		return cooldownFor(3, &wait)
	}
	assertEqual(t, asked(30), 30*time.Second)
	assertEqual(t, asked(7*24*3600), maxCooldown)
	assertEqual(t, asked(0), time.Duration(0))
}

func TestASiteIsForgottenADayAfterItsWaitEnds(t *testing.T) {
	w := newTestWorker(dbtest.Open(t), linkcheck.New(linkcheck.Config{}))
	w.cooldowns["recent.test"] = cooldown{strikes: 3, until: now.Add(-time.Minute)}
	w.cooldowns["old.test"] = cooldown{strikes: 3, until: now.Add(-forgetAfter - time.Minute)}

	w.forgetOldCooldowns()
	if _, kept := w.cooldowns["recent.test"]; !kept {
		t.Error("a recent site still doubles")
	}
	if _, kept := w.cooldowns["old.test"]; kept {
		t.Error("an old site must be forgotten")
	}
}

func TestAnAnswerStartsTheWaitingOver(t *testing.T) {
	w := newTestWorker(dbtest.Open(t), linkcheck.New(linkcheck.Config{}))
	ask := linkcheck.Backoff{Host: "slow.test"}
	w.leaveAlone(ask)
	w.leaveAlone(ask)
	assertEqual(t, w.cooldowns["slow.test"].strikes, 2)
	assertEqual(t, w.isCooling("slow.test"), true)
	assertEqual(t, w.isCooling("other.test"), false)

	w.learnFrom(linkcheck.Checked{Target: linkcheck.Target{URL: "https://slow.test/x"}, Answered: true})
	w.leaveAlone(ask)
	assertEqual(t, w.cooldowns["slow.test"].strikes, 1)
}

func TestATimeoutDoesNotEndTheWaiting(t *testing.T) {
	w := newTestWorker(dbtest.Open(t), linkcheck.New(linkcheck.Config{}))
	w.leaveAlone(linkcheck.Backoff{Host: "slow.test"})
	w.learnFrom(linkcheck.Checked{Target: linkcheck.Target{URL: "https://slow.test/x"}})
	assertEqual(t, w.cooldowns["slow.test"].strikes, 1)
}

func TestACoolingSiteDoesNotFillTheBatchAndStarveOtherSites(t *testing.T) {
	pool := dbtest.Open(t)
	user := dbtest.InsertUser(t, pool, "mei")
	optIn(t, pool, user)
	for page := range 4 {
		addBookmark(t, pool, user, "http://10.0.0.1/"+strconv.Itoa(page), nil)
	}
	elsewhere := addBookmark(t, pool, user, "http://10.0.0.2/", nil)
	w := newTestWorker(pool, linkcheck.New(linkcheck.Config{}))
	w.config.Batch = 2
	w.leaveAlone(linkcheck.Backoff{Host: "10.0.0.1"})

	items, err := w.dueNow(context.Background())
	assertEqual(t, err, nil)
	assertEqual(t, ids(items), []int64{elsewhere})
}

func TestASiteWithANonAsciiNameCoolsUnderItsPunycodeBackoff(t *testing.T) {
	pool := dbtest.Open(t)
	user := dbtest.InsertUser(t, pool, "mei")
	optIn(t, pool, user)
	addBookmark(t, pool, user, "https://b\u00fccher.example/a", nil)
	elsewhere := addBookmark(t, pool, user, "https://example.org/", nil)
	w := newTestWorker(pool, linkcheck.New(linkcheck.Config{}))
	// The host of a final URL is read from the serialized, punycode address.
	w.leaveAlone(linkcheck.Backoff{Host: linkcheck.HostOf("https://xn--bcher-kva.example/a")})

	items, err := w.dueNow(context.Background())
	assertEqual(t, err, nil)
	assertEqual(t, ids(items), []int64{elsewhere})
}

func TestAResultIsStoredOnlyWhileTheUserStillWantsTheCheck(t *testing.T) {
	pool := dbtest.Open(t)
	user := dbtest.InsertUser(t, pool, "mei")
	id := addBookmark(t, pool, user, "http://10.0.0.1/", nil)
	w := newTestWorker(pool, linkcheck.New(linkcheck.Config{}))
	checked := linkcheck.Checked{
		Target:  linkcheck.Target{ID: id, URL: "http://10.0.0.1/"},
		Verdict: linkcheck.Verdict{Status: linkcheck.StatusOK},
	}
	ctx := context.Background()

	stored, err := w.store(ctx, user, checked)
	assertEqual(t, err, nil)
	assertEqual(t, stored, 0)
	optIn(t, pool, user)
	stored, err = w.store(ctx, user, checked)
	assertEqual(t, err, nil)
	assertEqual(t, stored, 1)
}

func TestARoundStoresResultsAndTheNextRoundSkipsThem(t *testing.T) {
	pool := dbtest.Open(t)
	user := dbtest.InsertUser(t, pool, "mei")
	optIn(t, pool, user)
	// Not public, so no request is made and the verdict is "unknown".
	id := addBookmark(t, pool, user, "http://10.0.0.1/", nil)
	w := newTestWorker(pool, linkcheck.New(linkcheck.Config{}))
	ctx := context.Background()

	count, err := w.runRound(ctx)
	assertEqual(t, err, nil)
	assertEqual(t, count, 1)
	var status string
	var checkedAt int64
	err = pool.QueryRow("SELECT link_status, link_checked_at FROM bookmarks WHERE id = ?1", id).Scan(&status, &checkedAt)
	assertEqual(t, err, nil)
	assertEqual(t, status, "unknown")
	assertEqual(t, checkedAt, now.Unix())

	count, err = w.runRound(ctx)
	assertEqual(t, err, nil)
	assertEqual(t, count, 0)
}

func TestASiteThatSaysSlowDownIsAskedOnceAndThenLeftAlone(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	pool := dbtest.Open(t)
	user := dbtest.InsertUser(t, pool, "mei")
	optIn(t, pool, user)
	for _, page := range []string{"a", "b", "c"} {
		addBookmark(t, pool, user, server.URL+"/"+page, nil)
	}
	checker := linkcheck.New(linkcheck.Config{Allow: netip.Addr.IsLoopback})
	w := newTestWorker(pool, checker)
	ctx := context.Background()

	count, err := w.runRound(ctx)
	assertEqual(t, err, nil)
	assertEqual(t, count, 1)
	assertEqual(t, requests.Load(), int32(1))

	count, err = w.runRound(ctx)
	assertEqual(t, err, nil)
	assertEqual(t, count, 0)
	assertEqual(t, requests.Load(), int32(1))
}

func TestARoundAsksAsEachOwnerChose(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.Header.Get("User-Agent")
		mu.Unlock()
	}))
	defer server.Close()
	pool := dbtest.Open(t)
	mei := dbtest.InsertUser(t, pool, "mei")
	ana := dbtest.InsertUser(t, pool, "ana")
	chooseUserAgent(t, pool, mei, "Mei/1")
	optIn(t, pool, ana)
	// Two names for this machine are two sites, so the 1.5 second pause between
	// requests to one site does not slow the test.
	addBookmark(t, pool, mei, server.URL+"/mei", nil)
	addBookmark(t, pool, ana, strings.Replace(server.URL, "127.0.0.1", "localhost", 1)+"/ana", nil)
	w := newTestWorker(pool, linkcheck.New(linkcheck.Config{Allow: netip.Addr.IsLoopback}))

	count, err := w.runRound(context.Background())
	assertEqual(t, err, nil)
	assertEqual(t, count, 2)
	assertEqual(t, seen, map[string]string{"/mei": "Mei/1", "/ana": linkcheck.DefaultUserAgent()})
}

func TestDueBookmarksCarryTheirOwnersUserAgent(t *testing.T) {
	pool := dbtest.Open(t)
	mei := dbtest.InsertUser(t, pool, "mei")
	chooseUserAgent(t, pool, mei, "Mei/1")
	id := addBookmark(t, pool, mei, "http://example.test/", nil)

	items, err := dueBookmarks(context.Background(), pool, now, 10)
	assertEqual(t, err, nil)
	assertEqual(t, items, []due{{id: id, userID: mei, url: "http://example.test/", userAgent: "Mei/1"}})
}

func TestRunStopsWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() {
		Run(ctx, Config{DB: dbtest.Open(t), Checker: linkcheck.New(linkcheck.Config{}), Interval: time.Hour, Batch: 1})
		close(finished)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestAPanicInARoundDoesNotEndRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		Run(ctx, Config{
			DB: dbtest.Open(t), Checker: linkcheck.New(linkcheck.Config{}), Interval: 5 * time.Millisecond, Batch: 1,
			Now: func() time.Time {
				if calls.Add(1) == 1 {
					panic("boom")
				}
				return now
			},
		})
	}()
	deadline := time.After(5 * time.Second)
	for calls.Load() < 3 {
		select {
		case <-finished:
			t.Fatal("Run ended after a panic")
		case <-deadline:
			t.Fatal("no round followed the panic")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-finished
}
