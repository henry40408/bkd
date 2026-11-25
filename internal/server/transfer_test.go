package server

import (
	"net/http"
	"strings"
	"testing"
)

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

func TestExportNeedsASession(t *testing.T) {
	app := runningApp(t)
	assertStatus(t, app.send(get("/api/export?format=json")), http.StatusUnauthorized)
}

func TestExportedHTMLCanBeImportedAgain(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	app.createBookmark(token, "Alpha", "web")
	exported := app.send(get("/api/export?format=html").as(token)).text
	other := app.signIn("mei", false)
	mustContain(t, app.send(upload(other, "file", exported)), "Added 1, skipped 0")
}

func TestImportAcceptsAFileThatIsNotUTF8(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	file := "<DL><p>\n<DT><A HREF=\"https://example.org/caf\xe9\">caf\xe9</A>\n</DL>"
	mustContain(t, app.send(upload(token, "file", file)), "Added 1")
	mustContain(t, app.send(get("/").as(token)), "caf�")
}
