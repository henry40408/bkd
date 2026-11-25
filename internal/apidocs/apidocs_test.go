package apidocs

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func routeFor(t *testing.T, path string) Route {
	t.Helper()
	for _, route := range Routes {
		if route.Path == path {
			return route
		}
	}
	t.Fatalf("no route for %s", path)
	return Route{}
}

func serve(route Route, method string, header http.Header) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, route.Path, nil)
	for name, values := range header {
		request.Header[name] = values
	}
	recorder := httptest.NewRecorder()
	Handler(route).ServeHTTP(recorder, request)
	return recorder
}

func TestEveryRouteServesItsFileWithTheDocsHeaders(t *testing.T) {
	for _, route := range Routes {
		answer := serve(route, http.MethodGet, nil)
		if answer.Code != http.StatusOK {
			t.Errorf("%s answered %d", route.Path, answer.Code)
		}
		if got := answer.Header().Get("Content-Type"); got != route.ContentType {
			t.Errorf("%s Content-Type = %q, want %q", route.Path, got, route.ContentType)
		}
		if answer.Body.Len() == 0 {
			t.Errorf("%s answered an empty body", route.Path)
		}
		for name, want := range map[string]string{
			"Cache-Control":          "no-cache",
			"X-Content-Type-Options": "nosniff",
			"Referrer-Policy":        "no-referrer",
		} {
			if got := answer.Header().Get(name); got != want {
				t.Errorf("%s %s = %q, want %q", route.Path, name, got, want)
			}
		}
	}
}

// The policy has to let the page run its own scripts and draw Swagger UI's data:
// images, and nothing from another site.
func TestThePolicyAllowsOnlyThisOriginAndDataImages(t *testing.T) {
	policy := serve(routeFor(t, "/api/docs"), http.MethodGet, nil).Header().Get("Content-Security-Policy")
	for _, want := range []string{
		"default-src 'none'", "script-src 'self'", "style-src 'self'", "img-src 'self' data:",
		"connect-src 'self'", "frame-ancestors 'none'",
	} {
		if !strings.Contains(policy, want) {
			t.Errorf("policy %q lacks %q", policy, want)
		}
	}
	if strings.Contains(policy, "unsafe") || strings.Contains(policy, "http") {
		t.Errorf("policy %q allows more than this origin", policy)
	}
}

func TestAnUnchangedFileIsAnswered304(t *testing.T) {
	route := routeFor(t, "/api/docs/swagger-ui-bundle.js")
	etag := serve(route, http.MethodGet, nil).Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	answer := serve(route, http.MethodGet, http.Header{"If-None-Match": {etag}})
	if answer.Code != http.StatusNotModified || answer.Body.Len() != 0 {
		t.Errorf("a browser that has the file got %d with %d bytes", answer.Code, answer.Body.Len())
	}
	if other := serve(routeFor(t, "/api/docs/init.js"), http.MethodGet, nil).Header().Get("ETag"); other == etag {
		t.Error("two different files share an ETag")
	}
}

func TestTheIndexPageUsesOnlyFilesTheRoutesServe(t *testing.T) {
	served := map[string]bool{}
	for _, route := range Routes {
		served[route.Path] = true
	}
	page := serve(routeFor(t, "/api/docs"), http.MethodGet, nil).Body.String()
	script := serve(routeFor(t, "/api/docs/init.js"), http.MethodGet, nil).Body.String()
	pattern := regexp.MustCompile(`(?:src|href|url)[=:]\s*"(/[^"]*)"`)
	found := append(pattern.FindAllStringSubmatch(page, -1), pattern.FindAllStringSubmatch(script, -1)...)
	if len(found) < 4 {
		t.Fatalf("found only %d addresses in index.html and init.js: %v", len(found), found)
	}
	for _, match := range found {
		path := match[1]
		if path == "/favicon.svg" || strings.HasPrefix(path, "/api/docs/openapi.json") {
			continue
		}
		if !served[path] {
			t.Errorf("index.html or init.js uses %s, which no route serves", path)
		}
	}
	if strings.Contains(page, "<script>") || strings.Contains(page, "style=") {
		t.Error("index.html has inline script or style, which the policy would block")
	}
}

func TestSpecReferencesAllResolveAndOperationIdsAreUnique(t *testing.T) {
	var spec map[string]any
	if err := json.Unmarshal(Spec(), &spec); err != nil {
		t.Fatal(err)
	}
	operationIDs := map[string]bool{}
	var walk func(node any)
	walk = func(node any) {
		switch value := node.(type) {
		case map[string]any:
			if ref, ok := value["$ref"].(string); ok {
				target := any(spec)
				for _, step := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
					target, _ = target.(map[string]any)[step]
				}
				if target == nil {
					t.Errorf("%s points at nothing", ref)
				}
			}
			if id, ok := value["operationId"].(string); ok {
				if operationIDs[id] {
					t.Errorf("operationId %s is used twice", id)
				}
				operationIDs[id] = true
			}
			for _, child := range value {
				walk(child)
			}
		case []any:
			for _, child := range value {
				walk(child)
			}
		}
	}
	walk(spec)
	if len(operationIDs) == 0 {
		t.Error("the spec has no operations")
	}
}

func TestHeadHasNoBody(t *testing.T) {
	answer := serve(routeFor(t, "/api/docs/openapi.json"), http.MethodHead, nil)
	if answer.Code != http.StatusOK || answer.Body.Len() != 0 {
		t.Errorf("HEAD answered %d with %d bytes", answer.Code, answer.Body.Len())
	}
}
