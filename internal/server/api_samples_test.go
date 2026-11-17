package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The samples in testdata/api are what the Rust server answered to the requests
// below, replayed in the order they were captured so that each one meets the same
// state. A sample compares by status, header names and values, and the shape of
// the body: ids, timestamps, addresses and tokens differ from run to run.

const samplesDir = "testdata/api/"

// call is one captured request. who names a session in the cast.
type call struct {
	sample  string
	method  string
	path    string
	json    any
	raw     *string
	rawType string
	who     string
	headers map[string]string
	// cookie is a session token that no one holds; keep stores the token this answer sets.
	cookie string
	keep   string
	// anyBody skips the body of an answer that picks at random.
	anyBody bool
}

func text(s string) *string { return &s }

type sample struct {
	status  string
	headers map[string]string
	body    string
}

func readSample(t *testing.T, name string) sample {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(samplesDir, name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	_, rest, _ := strings.Cut(string(raw), "\n")
	head, body, _ := strings.Cut(rest, "\n\n")
	lines := strings.Split(head, "\n")
	out := sample{status: lines[0], headers: map[string]string{}, body: body}
	for _, line := range lines[1:] {
		name, value, _ := strings.Cut(line, ": ")
		out.headers[name] = value
	}
	return out
}

var (
	cookieValue = regexp.MustCompile(`^(bkd_session|__Host-bkd_session)=[0-9a-f]+`)
	bigNumber   = regexp.MustCompile(`\d{9,}`)
	// Headers the test recorder does not set; a real server adds them.
	framing = map[string]bool{"content-length": true, "transfer-encoding": true, "date": true}
)

func (a *testApp) compareSample(t *testing.T, c call, got reply) {
	t.Helper()
	want := readSample(t, c.sample)
	// The reason phrase is the HTTP library's, so only the code is compared.
	statusLine := "HTTP/1.1 " + strconv.Itoa(got.status)
	if !strings.HasPrefix(want.status, statusLine) {
		t.Errorf("%s: status %q, want %q; body %q", c.sample, statusLine, want.status, got.text)
	}
	compareHeaders(t, c.sample, got, want)
	// The capture cut large assets to nothing but kept their content-length.
	if length := want.headers["content-length"]; want.body == "" && length != "" && length != "0" {
		if strconv.Itoa(len(got.text)) != length {
			t.Errorf("%s: body is %d bytes, want %s", c.sample, len(got.text), length)
		}
		return
	}
	if !c.anyBody {
		compareBody(t, c.sample, got.text, want.body)
	}
}

func compareHeaders(t *testing.T, name string, got reply, want sample) {
	t.Helper()
	seen := map[string]string{}
	for key, values := range got.headers {
		seen[strings.ToLower(key)] = strings.Join(values, ", ")
	}
	for key, value := range want.headers {
		if framing[key] {
			continue
		}
		have, ok := seen[key]
		if !ok {
			t.Errorf("%s: header %s missing", name, key)
			continue
		}
		if key == "retry-after" {
			// The wait counts down while the test runs.
			continue
		}
		if key == "set-cookie" {
			have, value = cookieValue.ReplaceAllString(have, "$1=T"), cookieValue.ReplaceAllString(value, "$1=T")
		}
		if have != value {
			t.Errorf("%s: header %s = %q, want %q", name, key, have, value)
		}
	}
	var extra []string
	for key := range seen {
		if _, ok := want.headers[key]; !ok && !framing[key] {
			extra = append(extra, key)
		}
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		t.Errorf("%s: unexpected headers %v", name, extra)
	}
}

func compareBody(t *testing.T, name, got, want string) {
	t.Helper()
	var gotJSON, wantJSON any
	if json.Unmarshal([]byte(want), &wantJSON) == nil && json.Unmarshal([]byte(got), &gotJSON) == nil {
		if problem := sameShape("", gotJSON, wantJSON); problem != "" {
			t.Errorf("%s: body %s\n got: %.400s\nwant: %.400s", name, problem, got, want)
		}
		return
	}
	if parserError(got) != "" && parserError(got) == parserError(want) {
		return
	}
	if bigNumber.ReplaceAllString(got, "N") != bigNumber.ReplaceAllString(want, "N") {
		t.Errorf("%s: body differs\n got: %.400s\nwant: %.400s", name, got, want)
	}
}

// looseKey names the values that change from run to run.
func looseKey(key string) bool {
	return key == "id" || key == "user_id" || key == "ip" || key == "user_agent" ||
		strings.HasSuffix(key, "_at")
}

func sameShape(key string, got, want any) string {
	switch wanted := want.(type) {
	case map[string]any:
		gotten, ok := got.(map[string]any)
		if !ok {
			return key + " is not an object"
		}
		return sameObject(gotten, wanted)
	case []any:
		gotten, ok := got.([]any)
		if !ok || len(gotten) != len(wanted) {
			return key + " is not a list of the same length"
		}
		for i := range wanted {
			if problem := sameShape(key, gotten[i], wanted[i]); problem != "" {
				return problem
			}
		}
		return ""
	}
	if looseKey(key) {
		if reflect.TypeOf(got) != reflect.TypeOf(want) {
			return key + " has another type"
		}
		return ""
	}
	if !reflect.DeepEqual(got, want) {
		return key + " differs"
	}
	return ""
}

func sameObject(got, want map[string]any) string {
	for key, wanted := range want {
		gotten, ok := got[key]
		if !ok {
			return key + " missing"
		}
		if problem := sameShape(key, gotten, wanted); problem != "" {
			return problem
		}
	}
	for key := range got {
		if _, ok := want[key]; !ok {
			return key + " unexpected"
		}
	}
	return ""
}

// parserError is the part of a JSON refusal that does not name the parser's own
// wording, which is an accepted difference from Rust.
func parserError(body string) string {
	for _, lead := range []string{"Failed to parse the request body as JSON", "Failed to deserialize the JSON body into the target type"} {
		if strings.HasPrefix(body, lead) {
			return lead
		}
	}
	return ""
}
