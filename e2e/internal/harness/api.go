package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Password is long enough to pass the server's password rule.
const Password = "correct-horse-battery"

// SessionCookie is the name of the cookie that carries a session.
const SessionCookie = "bkd_session"

// Session is a signed-in API caller.
type Session struct {
	Token string
}

// NewBookmark is what a scenario needs to exist, not what it is about.
type NewBookmark struct {
	URL   string   `json:"url"`
	Title string   `json:"title"`
	Note  string   `json:"note"`
	Tags  []string `json:"tags"`
}

// Api makes direct JSON calls to set a scenario up quickly. What a scenario is
// about goes through the browser; what it merely needs to exist goes through
// here.
type Api struct {
	client     *http.Client
	baseURL    string
	setupToken string
}

// NewApi returns a client for the server.
func NewApi(server *Server) *Api {
	return &Api{
		client:     &http.Client{Timeout: 30 * time.Second},
		baseURL:    server.BaseURL + "/api",
		setupToken: server.SetupToken,
	}
}

// Setup creates the first admin, as the setup page does.
func (a *Api) Setup(username, password string) (Session, error) {
	body := map[string]string{"token": a.setupToken, "username": username, "password": password}
	return a.signIn("/setup", body)
}

// Login signs in and returns the session.
func (a *Api) Login(username, password string) (Session, error) {
	return a.signIn("/login", map[string]string{"username": username, "password": password})
}

func (a *Api) signIn(path string, body any) (Session, error) {
	response, err := a.send(request{method: http.MethodPost, path: path, json: body})
	if err != nil {
		return Session{}, err
	}
	if response.status/100 != 2 {
		return Session{}, fmt.Errorf("sign-in at %s was refused with %d: %s", path, response.status, response.body)
	}
	token := sessionTokenFrom(response.header)
	if token == "" {
		return Session{}, fmt.Errorf("the response to %s set no session cookie", path)
	}
	return Session{Token: token}, nil
}

func sessionTokenFrom(header http.Header) string {
	for _, cookie := range (&http.Response{Header: header}).Cookies() {
		if cookie.Name == SessionCookie {
			return cookie.Value
		}
	}
	return ""
}

// CreateUser creates an account as the admin.
func (a *Api) CreateUser(admin Session, username, password string, isAdmin bool) error {
	body := map[string]any{"username": username, "password": password, "is_admin": isAdmin}
	return a.expectSuccess("creating a user", request{method: http.MethodPost, path: "/users", json: body, who: &admin})
}

// CreateBookmark adds a bookmark to the account.
func (a *Api) CreateBookmark(who Session, bookmark NewBookmark) error {
	if bookmark.Tags == nil {
		bookmark.Tags = []string{}
	}
	return a.expectSuccess("creating a bookmark", request{method: http.MethodPost, path: "/bookmarks", json: bookmark, who: &who})
}

// Bookmarks lists what the account sees, newest first, as the API returns it.
func (a *Api) Bookmarks(who Session) ([]map[string]any, error) {
	response, err := a.send(request{method: http.MethodGet, path: "/bookmarks?limit=200", who: &who})
	if err != nil {
		return nil, err
	}
	if response.status/100 != 2 {
		return nil, fmt.Errorf("listing bookmarks failed with %d", response.status)
	}
	var list struct {
		Bookmarks []map[string]any `json:"bookmarks"`
	}
	if err := json.Unmarshal([]byte(response.body), &list); err != nil {
		return nil, fmt.Errorf("reading the bookmark list: %w", err)
	}
	return list.Bookmarks, nil
}

// ExportHTML returns the account's bookmarks as the HTML file the export route
// serves.
func (a *Api) ExportHTML(who Session) (string, error) {
	response, err := a.send(request{method: http.MethodGet, path: "/export?format=html", who: &who})
	if err != nil {
		return "", err
	}
	if response.status/100 != 2 {
		return "", fmt.Errorf("exporting failed with %d", response.status)
	}
	return response.body, nil
}

// ImportReport is the server's answer to an import.
type ImportReport struct {
	Added      int `json:"added"`
	Duplicates int `json:"duplicates"`
	Invalid    int `json:"invalid"`
}

// ImportHTML uploads an HTML bookmarks file and returns the import report.
func (a *Api) ImportHTML(who Session, html string) (ImportReport, error) {
	response, err := a.send(request{
		method: http.MethodPost, path: "/import", who: &who,
		contentType: "text/html", raw: html,
	})
	if err != nil {
		return ImportReport{}, err
	}
	if response.status/100 != 2 {
		return ImportReport{}, fmt.Errorf("importing failed with %d: %s", response.status, response.body)
	}
	var report ImportReport
	if err := json.Unmarshal([]byte(response.body), &report); err != nil {
		return ImportReport{}, fmt.Errorf("reading the import report %q: %w", response.body, err)
	}
	return report, nil
}

// SignedInUser is the username the server still sees behind the session, or
// "" once the session no longer works.
func (a *Api) SignedInUser(who Session) (string, error) {
	response, err := a.send(request{method: http.MethodGet, path: "/state", who: &who})
	if err != nil {
		return "", err
	}
	if response.status/100 != 2 {
		return "", fmt.Errorf("reading the state failed with %d", response.status)
	}
	var state struct {
		User *struct {
			Username string `json:"username"`
		} `json:"user"`
	}
	if err := json.Unmarshal([]byte(response.body), &state); err != nil {
		return "", fmt.Errorf("reading the state: %w", err)
	}
	if state.User == nil {
		return "", nil
	}
	return state.User.Username, nil
}

type request struct {
	method      string
	path        string
	who         *Session
	json        any
	raw         string
	contentType string
}

type reply struct {
	status int
	header http.Header
	body   string
}

func (a *Api) expectSuccess(what string, req request) error {
	response, err := a.send(req)
	if err != nil {
		return err
	}
	if response.status/100 != 2 {
		return fmt.Errorf("%s failed with %d: %s", what, response.status, response.body)
	}
	return nil
}

func (a *Api) send(req request) (reply, error) {
	body, contentType, err := req.payload()
	if err != nil {
		return reply{}, err
	}
	httpRequest, err := http.NewRequest(req.method, a.baseURL+req.path, body)
	if err != nil {
		return reply{}, err
	}
	if contentType != "" {
		httpRequest.Header.Set("Content-Type", contentType)
	}
	if req.who != nil {
		httpRequest.AddCookie(&http.Cookie{Name: SessionCookie, Value: req.who.Token})
	}
	response, err := a.client.Do(httpRequest)
	if err != nil {
		return reply{}, err
	}
	defer func() { _ = response.Body.Close() }()
	text, err := io.ReadAll(response.Body)
	if err != nil {
		return reply{}, err
	}
	return reply{status: response.StatusCode, header: response.Header, body: string(text)}, nil
}

func (r request) payload() (io.Reader, string, error) {
	if r.json != nil {
		encoded, err := json.Marshal(r.json)
		return bytes.NewReader(encoded), "application/json", err
	}
	if r.raw != "" {
		return strings.NewReader(r.raw), r.contentType, nil
	}
	return nil, "", nil
}
