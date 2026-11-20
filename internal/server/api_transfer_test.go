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

func TestImportAcceptsAFileThatIsNotUTF8(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	file := "<DL><p>\n<DT><A HREF=\"https://example.org/caf\xe9\">caf\xe9</A>\n</DL>"
	r := app.send(importRequest(file).as(token))
	assertStatus(t, r, http.StatusOK)
	if r.json()["added"] != float64(1) {
		t.Fatalf("report = %s", r.text)
	}
}
