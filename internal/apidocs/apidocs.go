// Package apidocs serves the OpenAPI description of the linkding-compatible API
// and the Swagger UI that shows it, all embedded, so the binary stays alone.
package apidocs

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"net/http"
	"time"
)

//go:embed openapi.json static/*
var files embed.FS

// The page and the script come from this origin only. Swagger UI draws its
// icons with data: images in its style sheet, which is why img-src has them.
const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; " +
	"img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// Route is one served file: the URL path, the embedded file and its type.
type Route struct {
	Path        string
	File        string
	ContentType string
}

// Routes lists every address the documentation answers for. The page is the
// same with and without the trailing slash. Its files are named by absolute
// path in index.html, so either works.
var Routes = []Route{
	{Path: "/api/docs", File: "static/index.html", ContentType: "text/html; charset=utf-8"},
	{Path: "/api/docs/{$}", File: "static/index.html", ContentType: "text/html; charset=utf-8"},
	{Path: "/api/docs/openapi.json", File: "openapi.json", ContentType: "application/json"},
	{Path: "/api/docs/init.js", File: "static/init.js", ContentType: "text/javascript; charset=utf-8"},
	{Path: "/api/docs/swagger-ui-bundle.js", File: "static/swagger-ui-bundle.js", ContentType: "text/javascript; charset=utf-8"},
	{Path: "/api/docs/swagger-ui.css", File: "static/swagger-ui.css", ContentType: "text/css; charset=utf-8"},
	// The bundle's first line points to its licence list, and Apache-2.0 wants
	// the licence and the NOTICE to travel with the files.
	{Path: "/api/docs/swagger-ui-bundle.js.LICENSE.txt", File: "static/swagger-ui-bundle.js.LICENSE.txt", ContentType: "text/plain; charset=utf-8"},
	{Path: "/api/docs/LICENSE", File: "static/LICENSE", ContentType: "text/plain; charset=utf-8"},
	{Path: "/api/docs/NOTICE", File: "static/NOTICE", ContentType: "text/plain; charset=utf-8"},
}

// Spec is the OpenAPI document, as served at /api/docs/openapi.json.
func Spec() []byte {
	return read("openapi.json")
}

func read(name string) []byte {
	body, err := files.ReadFile(name)
	if err != nil {
		panic("apidocs: embedded file missing: " + err.Error())
	}
	return body
}

// Handler serves one embedded file. The Swagger UI script is 1.7 MB, so the
// answer carries an ETag, and a browser that has the file is told so with 304.
func Handler(route Route) http.HandlerFunc {
	body := read(route.File)
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	return func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("Content-Type", route.ContentType)
		header.Set("Cache-Control", "no-cache")
		header.Set("ETag", etag)
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Content-Security-Policy", contentSecurityPolicy)
		header.Set("Referrer-Policy", "no-referrer")
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
	}
}
