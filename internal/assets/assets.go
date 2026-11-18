// Package assets embeds the hand-written front-end files, so the program stays
// a single binary with nothing to deploy next to it.
package assets

import (
	"embed"
	"net/http"
)

//go:embed static/*
var files embed.FS

// Only files from this origin may run or load. The pages have no inline script
// or style, so a stray <script> injected through bookmark text still would not
// execute.
const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; " +
	"connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// Route is one served file: the URL path, the embedded file and its type.
type Route struct {
	Path        string
	File        string
	ContentType string
}

// Routes lists every asset the server answers for. There is no ?v= hash in the
// URLs: the files carry no version, so Cache-Control: no-cache makes browsers
// ask again before reuse.
var Routes = []Route{
	{Path: "/app.css", File: "app.css", ContentType: "text/css; charset=utf-8"},
	{Path: "/app.js", File: "app.js", ContentType: "text/javascript; charset=utf-8"},
	{Path: "/favicon.svg", File: "favicon.svg", ContentType: "image/svg+xml"},
	// Browsers ask for this path on their own, even when the page links the SVG.
	{Path: "/favicon.ico", File: "favicon.ico", ContentType: "image/x-icon"},
	{Path: "/apple-touch-icon.png", File: "apple-touch-icon.png", ContentType: "image/png"},
}

// Handler serves one embedded file with the asset headers.
func Handler(route Route) http.HandlerFunc {
	body, err := files.ReadFile("static/" + route.File)
	if err != nil {
		panic("assets: embedded file missing: " + err.Error())
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		header := w.Header()
		header.Set("Content-Type", route.ContentType)
		header.Set("Cache-Control", "no-cache")
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Content-Security-Policy", contentSecurityPolicy)
		header.Set("Referrer-Policy", "no-referrer")
		_, _ = w.Write(body)
	}
}
