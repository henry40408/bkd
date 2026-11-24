// Command screenshots regenerates the four screenshots README.md embeds, into
// ../screenshots/:
//
//	cd e2e && go run ./cmd/screenshots
//
// The bookmarks are made-up and written through the server's own API into a
// throwaway database, with their ages and link checks set in place, so no
// picture needs the network or changes with the date. Font rendering and the
// Chrome version differ by machine: an unrelated image changing in the diff
// means the browser changed, not the page.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/henry40408/bkd/e2e/internal/harness"
)

// screen is the bookmark list, searched on a monitor, or on a phone, which is
// where the side index folds into a menu.
type screen struct {
	name     string
	path     string
	viewport harness.Viewport
}

var screens = []screen{
	{"web-search", "/?q=sqlite", harness.Viewport{Width: 1280, Height: 1080}},
	{"web-phone", "/", harness.Viewport{Width: 390, Height: 770}},
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "screenshots:", err)
		os.Exit(1)
	}
}

func run() error {
	output, err := screenshotDirectory()
	if err != nil {
		return err
	}
	binary, err := harness.EnsureBinary()
	if err != nil {
		return err
	}
	server, err := harness.StartServer(binary)
	if err != nil {
		return err
	}
	defer server.Stop()
	session, err := writeDemo(server)
	if err != nil {
		return err
	}
	for _, scheme := range []string{"light", "dark"} {
		if err := take(server, session, scheme, output); err != nil {
			return err
		}
	}
	fmt.Printf("screenshots: wrote four images to %s\n", output)
	return nil
}

// screenshotDirectory is where README.md looks for the images: the repository
// root, one above the e2e module the command runs in.
func screenshotDirectory() (string, error) {
	here, err := os.Getwd()
	if err != nil {
		return "", err
	}
	output := filepath.Join(here, "..", "screenshots")
	return output, os.MkdirAll(output, 0o755)
}

// writeDemo creates the first account and its bookmarks, and returns the
// account's session for the browser to sign in with.
func writeDemo(server *harness.Server) (harness.Session, error) {
	api := harness.NewApi(server)
	session, err := api.Setup(demoUser, harness.Password)
	if err != nil {
		return harness.Session{}, err
	}
	for _, bookmark := range demoBookmarks {
		if err := writeBookmark(server, api, session, bookmark); err != nil {
			return harness.Session{}, err
		}
	}
	return session, nil
}

func writeBookmark(server *harness.Server, api *harness.Api, session harness.Session, bookmark demoBookmark) error {
	created := harness.NewBookmark{URL: bookmark.url, Title: bookmark.title, Note: bookmark.note, Tags: bookmark.tags}
	if err := api.CreateBookmark(session, created); err != nil {
		return err
	}
	if err := server.Backdate(bookmark.url, bookmark.age); err != nil {
		return err
	}
	if bookmark.link.status == "" {
		return nil
	}
	return server.SetLinkCheck(bookmark.url, bookmark.link.status, bookmark.link.detail)
}

// take shoots both screens in one colour scheme.
func take(server *harness.Server, session harness.Session, scheme, output string) error {
	browser, err := harness.Open()
	if err != nil {
		return err
	}
	defer browser.Close()
	if err := browser.PreferScheme(scheme); err != nil {
		return err
	}
	if err := browser.SetSessionCookie(server.BaseURL, session.Token); err != nil {
		return err
	}
	suffix := ""
	if scheme == "dark" {
		suffix = "-dark"
	}
	for _, shot := range screens {
		if err := capture(browser, server.BaseURL, shot, filepath.Join(output, shot.name+suffix+".png")); err != nil {
			return err
		}
	}
	return nil
}

func capture(browser *harness.Browser, baseURL string, shot screen, path string) error {
	if err := browser.SetViewport(shot.viewport); err != nil {
		return err
	}
	if err := browser.Goto(baseURL + shot.path); err != nil {
		return err
	}
	if _, err := browser.Await(harness.CSS("ul.list li.item")); err != nil {
		return err
	}
	if err := waitUntilSettled(browser); err != nil {
		return err
	}
	return browser.Screenshot(path)
}

// waitUntilSettled waits for the fonts and for every transition to end, so a
// picture is never taken mid-fade.
func waitUntilSettled(browser *harness.Browser) error {
	return harness.Eventually("the page to be drawn", func() (bool, error) {
		return harness.Eval[bool](browser,
			`document.readyState === 'complete' && document.fonts.status === 'loaded' && document.getAnimations().length === 0`)
	})
}
