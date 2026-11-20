package server

import (
	"net/http"
	"testing"
)

func TestReadTokenFindsTheSessionCookie(t *testing.T) {
	cases := []struct {
		name        string
		secure      bool
		headers     []string
		wantToken   string
		wantPresent bool
	}{
		{name: "no header"},
		{name: "empty header", headers: []string{""}},
		{name: "only the cookie", headers: []string{"bkd_session=abc"}, wantToken: "abc", wantPresent: true},
		{name: "empty value", headers: []string{"bkd_session="}, wantPresent: true},
		{name: "other cookies around it", headers: []string{"a=1; bkd_session=abc; b=2"}, wantToken: "abc", wantPresent: true},
		{name: "no space after the semicolon", headers: []string{"a=1;bkd_session=abc"}, wantToken: "abc", wantPresent: true},
		{name: "only other cookies", headers: []string{"a=1; b=2"}},
		{name: "similar name", headers: []string{"xbkd_session=abc; bkd_session2=abc"}},
		{name: "name is case sensitive", headers: []string{"BKD_SESSION=abc"}},
		{name: "same name twice in one header: the first wins", headers: []string{"bkd_session=a; bkd_session=b"}, wantToken: "a", wantPresent: true},
		{name: "same name in two headers: the first wins", headers: []string{"bkd_session=a", "bkd_session=b"}, wantToken: "a", wantPresent: true},
		{name: "second header carries it", headers: []string{"a=1", "bkd_session=b"}, wantToken: "b", wantPresent: true},
		{name: "empty first value still wins", headers: []string{"bkd_session=; bkd_session=b"}, wantPresent: true},
		{name: "name without equals sign", headers: []string{"bkd_session"}},
		{name: "empty pairs before it", headers: []string{";;bkd_session=abc"}, wantToken: "abc", wantPresent: true},
		{name: "garbage pair before it", headers: []string{"garbage; bkd_session=abc"}, wantToken: "abc", wantPresent: true},
		{name: "spaces around the equals sign", headers: []string{"bkd_session = abc"}},
		{name: "value with an equals sign", headers: []string{"bkd_session=a=b"}, wantToken: "a=b", wantPresent: true},
		{name: "quoted value", headers: []string{`bkd_session="abc"`}, wantToken: `"abc"`, wantPresent: true},
		{name: "secure name", secure: true, headers: []string{"__Host-bkd_session=abc"}, wantToken: "abc", wantPresent: true},
		{name: "secure mode ignores the plain name", secure: true, headers: []string{"bkd_session=abc"}},
		{name: "plain mode ignores the secure name", headers: []string{"__Host-bkd_session=abc"}},
	}
	for _, c := range cases {
		app := &App{secureCookies: c.secure}
		request, err := http.NewRequest(http.MethodGet, "/", nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, header := range c.headers {
			request.Header.Add("Cookie", header)
		}
		token, present := app.readToken(request)
		if token != c.wantToken || present != c.wantPresent {
			t.Errorf("%s: readToken = (%q, %v), want (%q, %v)", c.name, token, present, c.wantToken, c.wantPresent)
		}
	}
}
