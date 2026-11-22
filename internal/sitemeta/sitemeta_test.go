package sitemeta

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func parse(page string) Meta { return Parse(strings.NewReader(page)) }

func TestOpenGraphWinsOverTheTitleAndMetaDescription(t *testing.T) {
	got := parse(`<html><head>
		<title>Plain title</title>
		<meta name="description" content="Plain description">
		<meta property="og:title" content="Og title">
		<meta property="og:description" content="Og description">
	</head><body></body></html>`)
	if got != (Meta{Title: "Og title", Description: "Og description"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestWithoutOpenGraphTheTitleAndMetaDescriptionAreUsed(t *testing.T) {
	got := parse(`<head><title>Plain title</title><meta name="description" content="Plain description"></head>`)
	if got != (Meta{Title: "Plain title", Description: "Plain description"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestEachFieldFallsBackOnItsOwn(t *testing.T) {
	got := parse(`<head><title>Plain title</title><meta property="og:description" content="Only og"></head>`)
	if got != (Meta{Title: "Plain title", Description: "Only og"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestOrderInTheHeadDoesNotMatter(t *testing.T) {
	got := parse(`<head><meta property="og:title" content="Og"><title>Plain</title></head>`)
	if got.Title != "Og" {
		t.Fatalf("title = %q", got.Title)
	}
	got = parse(`<head><title>Plain</title><meta property="og:title" content="Og"></head>`)
	if got.Title != "Og" {
		t.Fatalf("title = %q", got.Title)
	}
}

func TestAttributeNamesAndValuesAreReadTheWayBrowsersReadThem(t *testing.T) {
	got := parse(`<HEAD><META NAME="Description" CONTENT="  Shouting  "><META PROPERTY=" OG:TITLE " content='Single &amp; quoted'/></HEAD>`)
	if got != (Meta{Title: "Single & quoted", Description: "Shouting"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestOgTagsMayUseNameInsteadOfProperty(t *testing.T) {
	got := parse(`<head><meta name="og:title" content="By name"></head>`)
	if got.Title != "By name" {
		t.Fatalf("title = %q", got.Title)
	}
}

func TestTheFirstValueOfASourceWins(t *testing.T) {
	got := parse(`<head><meta property="og:title" content="First"><meta property="og:title" content="Second">
		<title>One</title><title>Two</title></head>`)
	if got.Title != "First" {
		t.Fatalf("title = %q", got.Title)
	}
	got = parse(`<head><title>One</title><title>Two</title></head>`)
	if got.Title != "One" {
		t.Fatalf("title = %q", got.Title)
	}
}

func TestAnEmptyValueDoesNotHideALaterOne(t *testing.T) {
	got := parse(`<head><meta property="og:title" content="  "><meta property="og:title" content="Real"></head>`)
	if got.Title != "Real" {
		t.Fatalf("title = %q", got.Title)
	}
	got = parse(`<head><title>  </title><meta property="og:description" content=""><meta name="description" content="Fallback"></head>`)
	if got != (Meta{Description: "Fallback"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestTagsAfterTheBodyStartsAreNotRead(t *testing.T) {
	got := parse(`<head></head><body><meta property="og:title" content="Late"><title>Late</title></body>`)
	if got != (Meta{}) {
		t.Fatalf("got %+v", got)
	}
}

func TestAPageWithoutAnHeadIsStillRead(t *testing.T) {
	got := parse(`<title>No head</title><p>text`)
	if got.Title != "No head" {
		t.Fatalf("title = %q", got.Title)
	}
}

func TestTextIsOneCleanLine(t *testing.T) {
	got := parse("<title>\n  Many\t\tlines \r\n here \u202eevil\u202c\x07 </title>" +
		`<meta name="description" content="a&#10;b&#9;c">`)
	if got != (Meta{Title: "Many lines here evil", Description: "a b c"}) {
		t.Fatalf("got %q", got)
	}
}

func TestEntitiesInTheTitleAreDecodedButTagsAreNotMarkup(t *testing.T) {
	got := parse(`<title>Tom &amp; Jerry &lt;b&gt;bold&lt;/b&gt; &eacute;</title>`)
	if got.Title != "Tom & Jerry <b>bold</b> é" {
		t.Fatalf("title = %q", got.Title)
	}
	got = parse(`<title>Not <b>markup</b></title>`)
	if got.Title != "Not <b>markup</b>" {
		t.Fatalf("title = %q", got.Title)
	}
}

func TestInvalidUTF8IsReplacedNotKept(t *testing.T) {
	got := parse("<title>caf\xe9</title>")
	if got.Title != "caf�" {
		t.Fatalf("title = %q", got.Title)
	}
}

func TestAPageThatTellsNothingIsEmpty(t *testing.T) {
	for _, page := range []string{"", "plain text", "<html><head></head><body>hi</body></html>",
		`<head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><meta content="orphan"></head>`} {
		if got := parse(page); got != (Meta{}) {
			t.Errorf("%q gave %+v", page, got)
		}
	}
}

func TestMetaTagWithoutAttributesIsSkipped(t *testing.T) {
	if got := parse(`<head><meta><title>T</title></head>`); got.Title != "T" {
		t.Fatalf("title = %q", got.Title)
	}
}

func TestATitleCutOffByTheEndOfThePageIsStillRead(t *testing.T) {
	if got := parse(`<head><title>Cut off`); got.Title != "Cut off" {
		t.Fatalf("title = %q", got.Title)
	}
}

func TestAReadErrorEndsTheParseWithWhatWasFound(t *testing.T) {
	page := io.MultiReader(
		strings.NewReader(`<head><title>Before</title><meta name="description" content="Then">`),
		iotest.ErrReader(errors.New("connection reset")),
		strings.NewReader(`<meta property="og:title" content="After the error">`),
	)
	got := Parse(page)
	if got != (Meta{Title: "Before", Description: "Then"}) {
		t.Fatalf("got %+v", got)
	}
}
