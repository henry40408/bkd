package server

import (
	"net/http"
	"strings"
	"testing"
)

const importFile = `<!DOCTYPE NETSCAPE-Bookmark-file-1>
<DL><p>
<DT><A HREF="https://example.org/a" TAGS="web,tools">Alpha</A>
<DT><A HREF="javascript:alert(1)">Bad</A>
</DL>`

func importRequest(body string) sent {
	return sent{method: "POST", uri: "/api/import"}.withRaw("text/html", body)
}

func TestImportReportsWhatItAddedAndSkipped(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	r := app.send(importRequest(importFile).as(token))
	assertStatus(t, r, http.StatusOK)
	report := r.json()
	if report["added"] != float64(1) {
		t.Fatalf("report = %s", r.text)
	}
	again := app.send(importRequest(importFile).as(token)).json()
	if again["added"] != float64(0) {
		t.Fatalf("second import = %v", again)
	}
}

func TestImportTakesOnlyHTMLButChecksTheSizeFirst(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	wrong := sent{method: "POST", uri: "/api/import"}.withRaw("application/json", "{}")
	assertStatus(t, app.send(wrong.as(token)), http.StatusUnsupportedMediaType)
	huge := sent{method: "POST", uri: "/api/import"}.withRaw("application/json", strings.Repeat("a", maxImportBytes+1))
	assertStatus(t, app.send(huge.as(token)), http.StatusRequestEntityTooLarge)
}

func TestExportGivesJSONAndHTML(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	app.createBookmark(token, "Alpha", "web")

	asJSON := app.send(get("/api/export?format=json").as(token))
	assertStatus(t, asJSON, http.StatusOK)
	if asJSON.headers.Get("Content-Type") != "application/json" ||
		asJSON.headers.Get("Content-Disposition") != `attachment; filename="bkd-bookmarks.json"` ||
		!strings.Contains(asJSON.text, "\n  \"bookmarks\": [") {
		t.Fatalf("json export: %v %s", asJSON.headers, asJSON.text)
	}
	asHTML := app.send(get("/api/export?format=html").as(token))
	assertStatus(t, asHTML, http.StatusOK)
	if asHTML.headers.Get("Content-Type") != "text/html; charset=utf-8" || !strings.Contains(asHTML.text, "Alpha") {
		t.Fatalf("html export: %v %s", asHTML.headers, asHTML.text)
	}
	assertStatus(t, app.send(get("/api/export?format=xml").as(token)), http.StatusBadRequest)
	assertStatus(t, app.send(get("/api/export").as(token)), http.StatusBadRequest)
}

func TestExportedHTMLCanBeImportedAgain(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	app.createBookmark(token, "Alpha", "web")
	exported := app.send(get("/api/export?format=html").as(token)).text
	other := app.signIn("mei", false)
	r := app.send(importRequest(exported).as(other))
	assertStatus(t, r, http.StatusOK)
	if r.json()["added"] != float64(1) {
		t.Fatalf("report = %s", r.text)
	}
}

func TestDecodeLossyMatchesRust(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"valid":              {"héllo", "héllo"},
		"lone continuation":  {"a\x80\x80b", "a��b"},
		"truncated 3 byte":   {"a\xE2\x82b", "a�b"},
		"truncated 4 byte":   {"\xF0\x9F\x98", "�"},
		"overlong lead":      {"\xC0\x80", "��"},
		"surrogate":          {"\xED\xA0\x80", "���"},
		"too high":           {"\xF4\x90\x80\x80", "����"},
		"ff":                 {"\xFFx", "�x"},
		"truncated at end":   {"ab\xE2", "ab�"},
		"e0 bad second":      {"\xE0\x80\x80", "���"},
		"valid after broken": {"\xE2\x82\xE2\x82\xAC", "�€"},
	} {
		if got := decodeLossy([]byte(tc.in)); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}
