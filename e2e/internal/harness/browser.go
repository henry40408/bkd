package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// ScenarioTimeout bounds one scenario, so a wedged Chrome cannot hang the run.
const ScenarioTimeout = 5 * time.Minute

const closeTimeout = 10 * time.Second

// ChromeEnv names the variable that pins the browser binary.
const ChromeEnv = "E2E_CHROME"

// Viewport is the size of the page area, in CSS pixels.
type Viewport struct {
	Width  int64
	Height int64
}

// Desktop is the window size a scenario starts with.
func Desktop() Viewport { return Viewport{Width: 1280, Height: 800} }

// Phone is narrower than the layout breakpoint.
func Phone() Viewport { return Viewport{Width: 390, Height: 800} }

// Browser is a headless Chrome of its own, scoped to one scenario.
//
// One process per scenario isolates cookies and storage, and lets a scenario
// switch scripts off before it opens its first page.
type Browser struct {
	ctx    context.Context
	cancel context.CancelFunc
	// scriptsOff is set by SetScripts. Clicks are then not verified: the
	// recorder that verifies them is page script.
	scriptsOff bool
}

// Open starts a headless Chrome with a desktop-sized viewport.
func Open() (*Browser, error) {
	timeoutCtx, cancelTimeout := context.WithTimeout(context.Background(), ScenarioTimeout)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(timeoutCtx, allocatorOptions()...)
	tabCtx, cancelTab := chromedp.NewContext(allocCtx)
	browser := &Browser{
		ctx: tabCtx,
		cancel: func() {
			cancelTab()
			cancelAlloc()
			cancelTimeout()
		},
	}
	if err := chromedp.Do(tabCtx); err != nil {
		browser.cancel()
		return nil, fmt.Errorf("could not start a browser (%s): a local Chromium or Chrome is required "+
			"(`brew install --cask ungoogled-chromium`), or set %s to its path: %w", BrowserChoice(), ChromeEnv, err)
	}
	if err := browser.SetViewport(Desktop()); err != nil {
		browser.cancel()
		return nil, err
	}
	return browser, nil
}

// BrowserChoice says which browser Open will start, for the run's log.
func BrowserChoice() string {
	if path := os.Getenv(ChromeEnv); path != "" {
		return ChromeEnv + "=" + path
	}
	return "chromedp's default search (" + ChromeEnv + " is unset)"
}

func allocatorOptions() []chromedp.ExecAllocatorOption {
	// The defaults already carry what the suite needs: headless, no /dev/shm
	// use, and no throttling of a backgrounded renderer.
	options := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	options = append(options,
		// Headless Linux reports `hover: none`, which would hide the hover-only
		// actions. Blink enums: kHoverHoverType=2, kPointerFine=4.
		chromedp.Flag("blink-settings",
			"primaryHoverType=2,availableHoverTypes=2,primaryPointerType=4,availablePointerTypes=4"),
		// Classic scrollbars would take width off the viewport on Linux only.
		chromedp.Flag("hide-scrollbars", true),
		chromedp.WindowSize(int(Desktop().Width), int(Desktop().Height)),
	)
	// chromedp's own search prefers a chromium on PATH over google-chrome, so
	// CI pins the binary it reports.
	if path := os.Getenv(ChromeEnv); path != "" {
		options = append(options, chromedp.ExecPath(path))
	}
	return options
}

// Close ends the browser, and the profile directory goes with it.
func (b *Browser) Close() {
	closing, cancel := context.WithTimeout(b.ctx, closeTimeout)
	defer cancel()
	_ = chromedp.Cancel(closing)
	b.cancel()
}

func send[P any](b *Browser, command cdp.Command[P, cdp.Empty], params P) error {
	_, err := chromedp.Call(b.ctx, command, params)
	return err
}

// SetViewport resizes the viewport exactly. A window size includes the browser
// chrome, which would miss the layout breakpoint.
func (b *Browser) SetViewport(viewport Viewport) error {
	return send(b, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{
		Width:             viewport.Width,
		Height:            viewport.Height,
		DeviceScaleFactor: 1,
	})
}

// Touch makes the browser report a touch screen: a coarse pointer and no hover.
func (b *Browser) Touch() error {
	return send(b, emulation.SetTouchEmulationEnabled, emulation.SetTouchEmulationEnabledParams{Enabled: true})
}

// AllowClipboard lets the pages of origin read and write the clipboard, so a
// step can check what a Copy button put there.
func (b *Browser) AllowClipboard(origin string) error {
	for _, name := range []string{"clipboard-read", "clipboard-write"} {
		err := send(b, browser.SetPermission, browser.SetPermissionParams{
			Permission: &browser.PermissionDescriptor{Name: name},
			Setting:    browser.PermissionSettingGranted,
			Origin:     origin,
		})
		if err != nil {
			return fmt.Errorf("could not allow %s: %w", name, err)
		}
	}
	return nil
}

// RefuseClipboardWrites makes navigator.clipboard.writeText reject, as it does when
// the browser denies it, so the page has to copy another way. The test server is on
// 127.0.0.1, a secure origin, so the page would otherwise never need to.
//
// It applies to the open page and to every page the browser loads from now on: bkd's
// script swaps pages in without loading one, so the open page keeps what it is given.
func (b *Browser) RefuseClipboardWrites() error {
	const refuse = `navigator.clipboard.writeText = () => Promise.reject(new DOMException('refused', 'NotAllowedError'));`
	_, err := chromedp.Call(b.ctx, page.AddScriptToEvaluateOnNewDocument,
		page.AddScriptToEvaluateOnNewDocumentParams{Source: refuse})
	if err != nil {
		return err
	}
	_, err = Exec[any](b, refuse)
	return err
}

// ClipboardText is the text on the clipboard.
func (b *Browser) ClipboardText() (string, error) {
	return chromedp.Run(b.ctx, chromedp.Evaluate[string]("navigator.clipboard.readText()", chromedp.EvalAwaitPromise))
}

// PreferScheme emulates the prefers-color-scheme media feature, "light" or
// "dark".
func (b *Browser) PreferScheme(scheme string) error {
	return send(b, emulation.SetEmulatedMedia, emulation.SetEmulatedMediaParams{
		Media:    "screen",
		Features: []*emulation.MediaFeature{{Name: "prefers-color-scheme", Value: scheme}},
	})
}

// Screenshot writes the viewport as a PNG.
func (b *Browser) Screenshot(path string) error {
	encoded, err := chromedp.Run(b.ctx, chromedp.CaptureScreenshot())
	if err != nil {
		return err
	}
	return os.WriteFile(path, encoded, 0o644)
}

// SetScripts switches page scripts on or off for the pages the browser opens
// from now on, to see what the server-rendered pages do on their own.
func (b *Browser) SetScripts(enabled bool) error {
	b.scriptsOff = !enabled
	return send(b, emulation.SetScriptExecutionDisabled, emulation.SetScriptExecutionDisabledParams{Value: !enabled})
}

// SetSessionCookie signs the browser in without going through the login form.
func (b *Browser) SetSessionCookie(baseURL, token string) error {
	httpOnly := true
	return send(b, network.SetCookie, network.SetCookieParams{
		Name: SessionCookie, Value: token, URL: baseURL, Path: "/", HTTPOnly: &httpOnly,
	})
}

// ClearCookies signs the browser out of everything.
func (b *Browser) ClearCookies() error {
	return send(b, network.ClearBrowserCookies, cdp.Empty{})
}

// Goto navigates to the address and waits for the page to load.
func (b *Browser) Goto(address string) error {
	return chromedp.Do(b.ctx, chromedp.Navigate(address))
}

// Reload reloads the page and waits for it to load.
func (b *Browser) Reload() error {
	if err := chromedp.Do(b.ctx, chromedp.Reload()); err != nil {
		return err
	}
	return b.WaitForLoad()
}

// Back goes one step back in the history and waits for the page to load.
func (b *Browser) Back() error {
	if err := chromedp.Do(b.ctx, chromedp.NavigateBack()); err != nil {
		return err
	}
	return b.WaitForLoad()
}

// Forward goes one step forward in the history and waits for the page to load.
func (b *Browser) Forward() error {
	if err := chromedp.Do(b.ctx, chromedp.NavigateForward()); err != nil {
		return err
	}
	return b.WaitForLoad()
}

// WaitForLoad waits for document.readyState to be "complete".
//
// CDP does not wait for a page load after a click, so every read or action
// that follows a navigation waits first. Without it, a key press can run
// before the page's script has loaded, and a lookup can run against the old
// document.
func (b *Browser) WaitForLoad() error {
	return Eventually("the page to finish loading", func() (bool, error) {
		state, err := Eval[string](b, "document.readyState")
		return state == "complete", err
	})
}

// URL is the page's current URL.
func (b *Browser) URL() (*url.URL, error) {
	location, err := chromedp.Run(b.ctx, chromedp.Location())
	if err != nil {
		return nil, err
	}
	return url.Parse(location)
}

// Title is the page's title.
func (b *Browser) Title() (string, error) {
	return Eval[string](b, "document.title")
}

// KeyEvent types keys at whatever has the focus.
func (b *Browser) KeyEvent(keys string) error {
	if err := b.WaitForLoad(); err != nil {
		return err
	}
	return chromedp.Do(b.ctx, chromedp.KeyEvent(keys))
}

// Eval evaluates a JavaScript expression in the page and decodes the result.
//
// Reads go through JavaScript rather than DOM-domain queries, because
// chromedp's queries wait forever by default and the suite's lookups are
// immediate. It also runs with page scripts switched off, and is not subject
// to the page's Content Security Policy.
func Eval[T any](b *Browser, expression string) (T, error) {
	return chromedp.Run(b.ctx, chromedp.Evaluate[T](expression))
}

// Exec runs the body of a JavaScript function in the page, with the arguments
// as `arguments`, and decodes what it returns.
func Exec[T any](b *Browser, body string, arguments ...any) (T, error) {
	expression := fmt.Sprintf("JSON.stringify((function() {%s}).apply(null, %s)) ?? 'null'", body, jsLiteral(arguments))
	var result T
	encoded, err := Eval[string](b, expression)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal([]byte(encoded), &result); err != nil {
		return result, fmt.Errorf("reading the script's result %s: %w", encoded, err)
	}
	return result, nil
}

// jsLiteral renders a Go value as a JavaScript literal.
func jsLiteral(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		// Only strings, numbers and slices of them are passed; this cannot happen.
		panic(fmt.Sprintf("encoding %v as a JavaScript literal: %v", value, err))
	}
	return string(encoded)
}
