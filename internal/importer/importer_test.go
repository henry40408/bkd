package importer

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/henry40408/bkd/internal/bookmarks"
	"github.com/henry40408/bkd/internal/dbtest"
)

const now int64 = 1_800_000_000

func assertEqual(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func ptr(value int64) *int64 { return &value }

func TestAnUnusableDateMeansNow(t *testing.T) {
	assertEqual(t, createdAt(ptr(1_700_000_000), now), int64(1_700_000_000))
	for _, unusable := range []*int64{nil, ptr(0), ptr(-5), ptr(now + 1)} {
		assertEqual(t, createdAt(unusable, now), now)
	}
}

func TestTimestampsInMillisecondsAndMicrosecondsBecomeSeconds(t *testing.T) {
	assertEqual(t, toSeconds(1_700_000_000), int64(1_700_000_000))
	assertEqual(t, toSeconds(1_700_000_000_000), int64(1_700_000_000))
	assertEqual(t, toSeconds(1_700_000_000_000_000), int64(1_700_000_000))
}

func TestLinksAreAddedSkippedOrReported(t *testing.T) {
	pool := dbtest.Open(t)
	store := bookmarks.NewStore(pool)
	user := dbtest.InsertUser(t, pool, "alice")
	html := `<DL>
            <DT><H3>Work</H3><DL>
              <DT><A HREF="https://a.test/" ADD_DATE="1700000000" TAGS="Rust">A</A>
              <DD>a note
              <DT><A HREF="https://a.test/">A again</A>
              <DT><A HREF="javascript:alert(1)">bad</A>
              <DT><A HREF="place:sort=8">bad</A>
              <DT><A HREF="https://no-title.test/"></A>
            </DL></DL>`
	ctx := context.Background()

	report, err := ImportHTML(ctx, store, Request{UserID: user, HTML: html, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, []int{report.Added, report.Duplicates, report.Invalid}, []int{2, 1, 2})
	stored, err := store.All(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, stored[0].CreatedAt, int64(1_700_000_000))
	assertEqual(t, stored[0].Tags, []string{"rust", "work"})
	assertEqual(t, stored[0].Note, "a note")
	assertEqual(t, stored[1].Title, "https://no-title.test/")
	assertEqual(t, stored[1].CreatedAt, now)

	again, err := ImportHTML(ctx, store, Request{UserID: user, HTML: html, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, []int{again.Added, again.Duplicates}, []int{0, 3})
}

func TestTheReportNamesAtMostFiveInvalidLinksAndCutsLongURLs(t *testing.T) {
	pool := dbtest.Open(t)
	store := bookmarks.NewStore(pool)
	user := dbtest.InsertUser(t, pool, "alice")
	var html strings.Builder
	for range 7 {
		html.WriteString(`<DT><A HREF="ftp://` + strings.Repeat("x", 300) + `">bad</A>`)
	}
	report, err := ImportHTML(context.Background(), store, Request{UserID: user, HTML: html.String(), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, report.Invalid, 7)
	assertEqual(t, len(report.InvalidExamples), 5)
	assertEqual(t, len([]rune(report.InvalidExamples[0].URL)), 200)
	assertEqual(t, report.InvalidExamples[0].Reason, "url must start with http:// or https://")
}

func TestAnEmptyFileGivesAnEmptyReport(t *testing.T) {
	pool := dbtest.Open(t)
	user := dbtest.InsertUser(t, pool, "alice")
	report, err := ImportHTML(context.Background(), bookmarks.NewStore(pool), Request{UserID: user, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, report, Report{InvalidExamples: []InvalidLink{}})
}
