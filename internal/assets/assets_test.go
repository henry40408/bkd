package assets

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(route Route) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	Handler(route).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, route.Path, nil))
	return rec
}

func TestEveryRouteServesItsFileWithAssetHeaders(t *testing.T) {
	for _, route := range Routes {
		rec := serve(route)
		if rec.Body.Len() == 0 {
			t.Errorf("%s: empty body", route.Path)
		}
		want := map[string]string{
			"Content-Type":           route.ContentType,
			"Cache-Control":          "no-cache",
			"X-Content-Type-Options": "nosniff",
			"Referrer-Policy":        "no-referrer",
		}
		for name, value := range want {
			if got := rec.Header().Get(name); got != value {
				t.Errorf("%s: %s = %q, want %q", route.Path, name, got, value)
			}
		}
		if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "form-action 'none'") {
			t.Errorf("%s: unexpected CSP %q", route.Path, csp)
		}
	}
}

func TestTouchIconIsAPNG(t *testing.T) {
	for _, route := range Routes {
		if route.File != "apple-touch-icon.png" {
			continue
		}
		if !strings.HasPrefix(serve(route).Body.String(), "\x89PNG") {
			t.Errorf("%s is not a PNG", route.File)
		}
	}
}
