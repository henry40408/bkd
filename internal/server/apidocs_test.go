package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/henry40408/bkd/internal/apidocs"
	"github.com/henry40408/bkd/internal/bookmarks"
)

// The OpenAPI document is written by hand, so these tests hold it to the server:
// the routes it lists are the routes that exist, and what the server answers has
// the fields it says.

func loadSpec(t *testing.T) map[string]any {
	t.Helper()
	var spec map[string]any
	if err := json.Unmarshal(apidocs.Spec(), &spec); err != nil {
		t.Fatalf("openapi.json is not JSON: %v", err)
	}
	return spec
}

func TestTheSpecListsTheLinkdingRoutesAndNoOthers(t *testing.T) {
	app := runningApp(t)
	served := map[string]bool{}
	for _, route := range app.app.linkdingRoutes() {
		path := strings.TrimSuffix(route.pattern, "{$}")
		for _, handler := range route.handlers {
			served[handler.method+" "+path] = true
		}
	}
	documented := map[string]bool{}
	for path, item := range loadSpec(t)["paths"].(map[string]any) {
		for key := range item.(map[string]any) {
			if key == "parameters" {
				continue
			}
			documented[strings.ToUpper(key)+" "+path] = true
		}
	}
	for operation := range served {
		if !documented[operation] {
			t.Errorf("%s is served but not in openapi.json", operation)
		}
	}
	for operation := range documented {
		if !served[operation] {
			t.Errorf("%s is in openapi.json but not served", operation)
		}
	}
}

func TestTheSpecNamesTheLinkdingVersionTheReadmeDoes(t *testing.T) {
	info := loadSpec(t)["info"].(map[string]any)
	version, _ := info["x-linkding-version"].(string)
	if version == "" {
		t.Fatal("info.x-linkding-version is missing")
	}
	if info["version"] != "linkding-v"+version {
		t.Errorf("info.version = %v, want linkding-v%s", info["version"], version)
	}
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "linkding v"+version) {
		t.Errorf("README.md does not name linkding v%s, the version the API documentation follows", version)
	}
	// A bump that misses one mention would leave the page saying two versions.
	mention := regexp.MustCompile(`linkding v(\d+\.\d+\.\d+)|/blob/v(\d+\.\d+\.\d+)/`)
	for name, text := range map[string][]byte{"openapi.json": apidocs.Spec(), "README.md": readme} {
		for _, found := range mention.FindAllStringSubmatch(string(text), -1) {
			if named := found[1] + found[2]; named != version {
				t.Errorf("%s names %q, but the document follows linkding v%s", name, found[0], version)
			}
		}
	}
}

func TestAnUnknownAddressUnderAPIAnswersAnEmpty404ToEveryMethod(t *testing.T) {
	app := runningApp(t)
	// The ones the document lists as not answered. A script's write carries no
	// Origin, which the page guard would refuse with a 403 page.
	for _, request := range []sent{
		post("/api/bookmarks/1/archive/", nil), post("/api/bookmarks/1/unarchive/", nil),
		del("/api/tags/5/"), post("/api/bundles/", nil), post("/api/user/profile/", nil),
		post("/api/bookmarks/1/assets/upload/", nil), del("/api/bookmarks/1/assets/2/"),
		put("/api/nothing/", nil), get("/api"),
	} {
		answer := app.send(request)
		if answer.status != http.StatusNotFound || answer.text != "" {
			t.Errorf("%s %s = %d %q, want an empty 404", request.method, request.uri, answer.status, answer.text)
		}
	}
}

// followRef returns the schema a $ref (or an allOf of one) points at.
func followRef(t *testing.T, spec map[string]any, schema map[string]any) map[string]any {
	t.Helper()
	if parts, ok := schema["allOf"].([]any); ok && len(parts) == 1 {
		schema = parts[0].(map[string]any)
	}
	ref, ok := schema["$ref"].(string)
	if !ok {
		return schema
	}
	node := any(spec)
	for _, step := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		node = node.(map[string]any)[step]
	}
	return followRef(t, spec, node.(map[string]any))
}

// conforms reports where a decoded JSON value does not fit its schema: another
// type, a missing or an extra field, a null where the schema is not nullable.
func conforms(t *testing.T, spec, schema map[string]any, value any, where string) {
	t.Helper()
	nullable, _ := schema["nullable"].(bool)
	// OpenAPI 3.0 ignores `nullable` in a schema object that has no `type`, so a
	// generated client would refuse the null this schema seems to allow.
	if _, typed := schema["type"]; nullable && !typed {
		t.Errorf("%s: nullable has no effect without a type beside it", where)
	}
	schema = followRef(t, spec, schema)
	if value == nil {
		if nullable || schema["nullable"] == true {
			return
		}
		t.Errorf("%s is null, and the schema is not nullable", where)
		return
	}
	if allowed, limited := schema["enum"].([]any); limited && !slices.Contains(allowed, value) {
		t.Errorf("%s is %v, the schema allows only %v", where, value, allowed)
	}
	switch schema["type"] {
	case "object":
		fields, ok := value.(map[string]any)
		if !ok {
			t.Errorf("%s is %T, want an object", where, value)
			return
		}
		properties := schema["properties"].(map[string]any)
		for name, property := range properties {
			field, present := fields[name]
			if !present {
				t.Errorf("%s.%s is documented but missing", where, name)
				continue
			}
			conforms(t, spec, property.(map[string]any), field, where+"."+name)
		}
		for name := range fields {
			if _, documented := properties[name]; !documented {
				t.Errorf("%s.%s is sent but not documented", where, name)
			}
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			t.Errorf("%s is %T, want an array", where, value)
			return
		}
		for i, item := range items {
			conforms(t, spec, schema["items"].(map[string]any), item, fmt.Sprintf("%s[%d]", where, i))
		}
	default:
		want := map[string]string{"string": "string", "boolean": "bool", "integer": "float64"}[schema["type"].(string)]
		if got := fmt.Sprintf("%T", value); got != want {
			t.Errorf("%s is %s, the schema says %v", where, got, schema["type"])
		}
	}
}

func TestWhatTheServerAnswersHasTheFieldsTheSpecSays(t *testing.T) {
	app := runningApp(t)
	token, userID := app.linkdingUser("docs")
	app.saveBookmark(userID, bookmarks.Input{
		URL: "https://example.com/a", Title: "A", Note: "note", Tags: []string{"go"},
		Site: bookmarks.Site{Title: "Site", Description: "About"},
	}, 1_700_000_000)
	app.saveBookmark(userID, bookmarks.Input{URL: "https://example.com/b", Title: "B"}, 1_700_000_100)
	spec := loadSpec(t)
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)

	cases := []struct {
		name   string
		uri    string
		schema string
	}{
		{"bookmark list", "/api/bookmarks/", "BookmarkList"},
		{"archived list", "/api/bookmarks/archived/", "BookmarkList"},
		{"shared list", "/api/bookmarks/shared/", "BookmarkList"},
		{"a bookmark with page details", "/api/bookmarks/1/", "Bookmark"},
		{"a bookmark without", "/api/bookmarks/2/", "Bookmark"},
		{"tag list", "/api/tags/", "TagList"},
		{"check of a saved address", "/api/bookmarks/check/?url=https://example.com/a", "CheckResult"},
		{"check of another address", "/api/bookmarks/check/?url=https://example.com/zzz", "CheckResult"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			answer := app.send(withToken(get(c.uri), token))
			if answer.status != http.StatusOK {
				t.Fatalf("status %d: %s", answer.status, answer.text)
			}
			var value any
			if err := json.Unmarshal([]byte(answer.text), &value); err != nil {
				t.Fatal(err)
			}
			conforms(t, spec, schemas[c.schema].(map[string]any), value, c.schema)
		})
	}
}

func TestTheDocumentationNeedsNoToken(t *testing.T) {
	app := runningApp(t)
	for _, route := range apidocs.Routes {
		path := strings.TrimSuffix(route.Path, "{$}")
		answer := app.send(get(path))
		if answer.status != http.StatusOK {
			t.Errorf("GET %s answered %d", path, answer.status)
		}
		if !strings.Contains(answer.headers.Get("Content-Security-Policy"), "script-src 'self'") {
			t.Errorf("GET %s has no script policy: %q", path, answer.headers.Get("Content-Security-Policy"))
		}
	}
}

func TestTheDocumentationAnswersOnlyItsOwnAddressesAndOnlyGet(t *testing.T) {
	app := runningApp(t)
	if answer := app.send(get("/api/docs/nothing")); answer.status != http.StatusNotFound || answer.text != "" {
		t.Errorf("an unknown address under /api/docs answered %d %q", answer.status, answer.text)
	}
	answer := app.send(post("/api/docs", map[string]any{}))
	if answer.status != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/docs answered %d", answer.status)
	}
	if allow := answer.headers.Get("Allow"); !slices.Contains(strings.Split(allow, ","), "GET") {
		t.Errorf("Allow = %q", allow)
	}
}

func TestTheServedSpecIsTheEmbeddedOne(t *testing.T) {
	app := runningApp(t)
	answer := app.send(get("/api/docs/openapi.json"))
	if answer.text != string(apidocs.Spec()) {
		t.Error("/api/docs/openapi.json is not openapi.json")
	}
	if got := answer.headers.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
}
