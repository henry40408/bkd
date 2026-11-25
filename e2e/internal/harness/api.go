package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Password is long enough to pass the server's password rule.
const Password = "correct-horse-battery"

// SessionCookie is the name of the cookie that carries a session.
const SessionCookie = "bkd_session"

// Session is a signed-in caller.
type Session struct {
	Token string
}

// NewBookmark is what a scenario needs to exist, not what it is about.
type NewBookmark struct {
	URL   string
	Title string
	Note  string
	Tags  []string
}

// Api posts the same forms the pages post, to set a scenario up quickly. What a
// scenario is about goes through the browser; what it merely needs to exist
// goes through here.
type Api struct {
	client     *http.Client
	baseURL    string
	setupToken string
}

// NewApi returns a client for the server.
func NewApi(server *Server) *Api {
	return &Api{
		client: &http.Client{
			Timeout: 30 * time.Second,
			// A form post is answered with a redirect, and the session cookie
			// and the address that tells accepted from refused are on it.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		baseURL:    server.BaseURL,
		setupToken: server.SetupToken,
	}
}

// Setup creates the first admin, as the setup page does.
func (a *Api) Setup(username, password string) (Session, error) {
	fields := url.Values{"token": {a.setupToken}, "username": {username}, "password": {password}}
	return a.signIn("/setup", fields)
}

// Login signs in and returns the session.
func (a *Api) Login(username, password string) (Session, error) {
	return a.signIn("/login", url.Values{"username": {username}, "password": {password}})
}

func (a *Api) signIn(path string, fields url.Values) (Session, error) {
	response, err := a.send(formPost(path, fields))
	if err != nil {
		return Session{}, err
	}
	if response.status != http.StatusSeeOther {
		return Session{}, fmt.Errorf("sign-in at %s was refused with %d", path, response.status)
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

// CreateUser creates an account as the admin. The admin's last password
// confirmation must be recent, as for the Manage users page.
func (a *Api) CreateUser(admin Session, username, password string, isAdmin bool) error {
	fields := url.Values{"username": {username}, "password": {password}}
	if isAdmin {
		// The form's checkbox sends a value when it is ticked and nothing otherwise.
		fields.Set("is_admin", "on")
	}
	return a.expectRedirect("creating a user", "/users?notice=user-changed", formPost("/users", fields).as(admin))
}

// CreateBookmark adds a bookmark to the account.
func (a *Api) CreateBookmark(who Session, bookmark NewBookmark) error {
	fields := url.Values{
		"url": {bookmark.URL}, "title": {bookmark.Title}, "note": {bookmark.Note},
		"tags": {strings.Join(bookmark.Tags, ",")},
	}
	return a.expectRedirect("creating a bookmark", "/?notice=bookmark-saved", formPost("/bookmarks", fields).as(who))
}

// Bookmarks lists what the account has, as the JSON export gives it.
func (a *Api) Bookmarks(who Session) ([]map[string]any, error) {
	body, err := a.read("listing bookmarks", "/api/export?format=json", who)
	if err != nil {
		return nil, err
	}
	var list struct {
		Bookmarks []map[string]any `json:"bookmarks"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		return nil, fmt.Errorf("reading the bookmark list: %w", err)
	}
	return list.Bookmarks, nil
}

// ExportHTML returns the account's bookmarks as the HTML file the export route
// serves.
func (a *Api) ExportHTML(who Session) (string, error) {
	return a.read("exporting", "/api/export?format=html", who)
}

// ImportReport is what the page says an import did.
type ImportReport struct {
	Added      int
	Duplicates int
	Invalid    int
}

var importSummary = regexp.MustCompile(`Added (\d+), skipped (\d+) you already had(?:, and ignored (\d+) that are not web links)?`)

// ImportHTML uploads an HTML bookmarks file and returns the report the page
// shows.
func (a *Api) ImportHTML(who Session, bookmarksFile string) (ImportReport, error) {
	upload, err := uploadOf("/transfer/import", bookmarksFile)
	if err != nil {
		return ImportReport{}, err
	}
	response, err := a.send(upload.as(who))
	if err != nil {
		return ImportReport{}, err
	}
	if response.status != http.StatusOK {
		return ImportReport{}, fmt.Errorf("importing failed with %d", response.status)
	}
	found := importSummary.FindStringSubmatch(response.body)
	if found == nil {
		return ImportReport{}, fmt.Errorf("the page after the import has no report")
	}
	// The digits always parse; the count of ignored links is absent when it is 0.
	added, _ := strconv.Atoi(found[1])
	duplicates, _ := strconv.Atoi(found[2])
	invalid, _ := strconv.Atoi(found[3])
	return ImportReport{Added: added, Duplicates: duplicates, Invalid: invalid}, nil
}

// SignedInUser is the username the server still sees behind the session, or
// "" once the session no longer works.
func (a *Api) SignedInUser(who Session) (string, error) {
	response, err := a.send(request{method: http.MethodGet, path: "/account"}.as(who))
	if err != nil {
		return "", err
	}
	if response.status == http.StatusSeeOther && strings.HasPrefix(response.header.Get("Location"), "/login") {
		return "", nil
	}
	if response.status != http.StatusOK {
		return "", fmt.Errorf("reading the account page failed with %d", response.status)
	}
	const marker = "Signed in as <strong>"
	_, rest, found := strings.Cut(response.body, marker)
	name, _, closed := strings.Cut(rest, "</strong>")
	if !found || !closed {
		return "", fmt.Errorf("the account page does not say who is signed in")
	}
	return html.UnescapeString(name), nil
}

type request struct {
	method      string
	path        string
	who         *Session
	contentType string
	body        string
}

func formPost(path string, fields url.Values) request {
	return request{
		method: http.MethodPost, path: path,
		contentType: "application/x-www-form-urlencoded", body: fields.Encode(),
	}
}

// uploadOf is the multipart post the import form sends.
func uploadOf(path, contents string) (request, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "bookmarks.html")
	if err != nil {
		return request{}, err
	}
	if _, err := io.WriteString(part, contents); err != nil {
		return request{}, err
	}
	if err := writer.Close(); err != nil {
		return request{}, err
	}
	return request{method: http.MethodPost, path: path, contentType: writer.FormDataContentType(), body: body.String()}, nil
}

func (r request) as(who Session) request {
	r.who = &who
	return r
}

type reply struct {
	status int
	header http.Header
	body   string
}

// expectRedirect checks that a form was accepted. A refusal is another status,
// or a redirect to the sign-in or the confirmation page, so the status alone
// does not tell.
func (a *Api) expectRedirect(what, location string, req request) error {
	response, err := a.send(req)
	if err != nil {
		return err
	}
	if got := response.header.Get("Location"); response.status != http.StatusSeeOther || got != location {
		return fmt.Errorf("%s was answered with %d and a redirect to %q, not to %q", what, response.status, got, location)
	}
	return nil
}

// read fetches a page or a download and returns its body.
func (a *Api) read(what, path string, who Session) (string, error) {
	response, err := a.send(request{method: http.MethodGet, path: path}.as(who))
	if err != nil {
		return "", err
	}
	if response.status != http.StatusOK {
		return "", fmt.Errorf("%s failed with %d", what, response.status)
	}
	return response.body, nil
}

func (a *Api) send(req request) (reply, error) {
	httpRequest, err := http.NewRequest(req.method, a.baseURL+req.path, strings.NewReader(req.body))
	if err != nil {
		return reply{}, err
	}
	if req.contentType != "" {
		httpRequest.Header.Set("Content-Type", req.contentType)
	}
	// The server refuses a form post that does not say it comes from its own site.
	httpRequest.Header.Set("Origin", a.baseURL)
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
