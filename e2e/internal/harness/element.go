package harness

import (
	"fmt"

	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// Element is a handle on a DOM node: the remote object id the browser gave
// it. Once the document it belongs to is gone, every call on it fails; that
// failure means the node is stale.
type Element struct {
	browser *Browser
	id      runtime.RemoteObjectID
	what    string
}

// Locator says how to find elements: by CSS selector or by XPath.
type Locator struct {
	kind  string
	query string
}

// CSS locates by CSS selector.
func CSS(selector string) Locator { return Locator{kind: "css", query: selector} }

// XPath locates by XPath expression. Inside an element, start it with `.`.
func XPath(expression string) Locator { return Locator{kind: "xpath", query: expression} }

func (l Locator) String() string { return l.kind + " " + l.query }

// String names the element in messages.
func (e *Element) String() string { return e.what }

// The JavaScript every lookup shares. checkVisibility is what a person would
// call "on screen": display, visibility and opacity all count.
const lookupPrelude = `
	const shown = (node) => node.nodeType === 1
		&& node.checkVisibility({ opacityProperty: true, visibilityProperty: true });
	const nodesOf = (scope, kind, query) => {
		if (kind === 'css') { return [...scope.querySelectorAll(query)]; }
		const document = scope.ownerDocument || scope;
		const snapshot = document.evaluate(query, scope, null, XPathResult.ORDERED_NODE_SNAPSHOT_TYPE, null);
		const nodes = [];
		for (let index = 0; index < snapshot.snapshotLength; index++) { nodes.push(snapshot.snapshotItem(index)); }
		return nodes;
	};
`

// First finds the first element matching the locator, and fails when there is
// none.
func (b *Browser) First(locator Locator) (*Element, error) {
	return first(b, nil, locator, false)
}

// All finds every element matching the locator.
func (b *Browser) All(locator Locator) ([]*Element, error) {
	return all(b, nil, locator)
}

// Await waits for an element matching the locator that is on screen.
func (b *Browser) Await(locator Locator) (*Element, error) {
	return await(b, nil, locator)
}

// Await waits for a descendant matching the locator that is on screen.
func (e *Element) Await(locator Locator) (*Element, error) {
	return await(e.browser, e, locator)
}

// All finds every descendant matching the locator.
func (e *Element) All(locator Locator) ([]*Element, error) {
	return all(e.browser, e, locator)
}

func await(b *Browser, scope *Element, locator Locator) (*Element, error) {
	var found *Element
	err := Eventually("an element on screen for "+locator.String(), func() (bool, error) {
		element, err := first(b, scope, locator, true)
		found = element
		return err == nil, err
	})
	return found, err
}

func first(b *Browser, scope *Element, locator Locator, onScreen bool) (*Element, error) {
	object, err := objectFrom(b, scope, lookupPrelude+`
		const nodes = nodesOf(this, kind, query);
		const onScreen = extra;
		return (onScreen ? nodes.filter(shown) : nodes)[0] ?? null;`,
		locator.kind, locator.query, onScreen)
	if err != nil {
		return nil, err
	}
	found := wrap(b, object, locator.String())
	if found == nil {
		return nil, fmt.Errorf("no element matches %s", locator)
	}
	return found, nil
}

func all(b *Browser, scope *Element, locator Locator) ([]*Element, error) {
	count, err := valueFrom[int](b, scope, lookupPrelude+`return nodesOf(this, kind, query).length;`,
		locator.kind, locator.query, 0)
	if err != nil {
		return nil, err
	}
	elements := make([]*Element, 0, count)
	for index := range count {
		object, err := objectFrom(b, scope, lookupPrelude+`return nodesOf(this, kind, query)[extra] ?? null;`,
			locator.kind, locator.query, index)
		if err != nil {
			return nil, err
		}
		found := wrap(b, object, fmt.Sprintf("%s[%d]", locator, index))
		if found == nil {
			return nil, fmt.Errorf("%s matched %d elements, then fewer", locator, count)
		}
		elements = append(elements, found)
	}
	return elements, nil
}

// wrap turns a remote object into an Element, nil for JavaScript null.
func wrap(b *Browser, object *runtime.RemoteObject, what string) *Element {
	if object.ObjectID == "" {
		return nil
	}
	return &Element{browser: b, id: object.ObjectID, what: what}
}

// objectFrom runs a lookup body with `this` set to the scope element, or to
// the document when scope is nil, and returns the result as a remote object.
// The body sees its arguments as kind, query and extra.
func objectFrom(b *Browser, scope *Element, body string, args ...any) (*runtime.RemoteObject, error) {
	function := functionOf(body)
	if scope != nil {
		return chromedp.Run(b.ctx, chromedp.CallFunctionOn[*runtime.RemoteObject](function, onObject(scope.id), args...))
	}
	return chromedp.Run(b.ctx, chromedp.Evaluate[*runtime.RemoteObject](onDocument(function, args)))
}

// valueFrom is objectFrom for a result that is a plain value.
func valueFrom[T any](b *Browser, scope *Element, body string, args ...any) (T, error) {
	function := functionOf(body)
	if scope != nil {
		return chromedp.Run(b.ctx, chromedp.CallFunctionOn[T](function, onObject(scope.id), args...))
	}
	return chromedp.Run(b.ctx, chromedp.Evaluate[T](onDocument(function, args)))
}

func functionOf(body string) string {
	return "function(kind, query, extra) {" + body + "}"
}

func onObject(id runtime.RemoteObjectID) chromedp.CallOption {
	return func(params *runtime.CallFunctionOnParams) {
		params.ObjectID = id
	}
}

func onDocument(function string, args []any) string {
	return fmt.Sprintf("(%s).apply(document, %s)", function, jsLiteral(args))
}

// Call calls a function with `this` set to the element, and decodes the
// result.
func Call[T any](e *Element, function string, args ...any) (T, error) {
	return chromedp.Run(e.browser.ctx, chromedp.CallFunctionOn[T](function, onObject(e.id), args...))
}

// Displayed reports whether the browser renders the element.
func (e *Element) Displayed() (bool, error) {
	return Call[bool](e, "function() { return this.checkVisibility({ opacityProperty: true, visibilityProperty: true }); }")
}

// Text is the element's rendered text, empty while the element is hidden.
func (e *Element) Text() (string, error) {
	return Call[string](e, `function() {
		if (!this.checkVisibility({ opacityProperty: true, visibilityProperty: true })) { return ''; }
		return this.innerText.trim();
	}`)
}

// Attr reads an attribute, and reports whether it is present.
func (e *Element) Attr(name string) (string, bool, error) {
	value, err := Call[*string](e, "function(name) { return this.getAttribute(name); }", name)
	if err != nil || value == nil {
		return "", false, err
	}
	return *value, true, nil
}

// Value is the current value of a form control.
func (e *Element) Value() (string, error) {
	return Call[string](e, "function() { return this.value; }")
}

// Checked reports whether a checkbox is ticked.
func (e *Element) Checked() (bool, error) {
	return Call[bool](e, "function() { return this.checked; }")
}

// Focus gives the element the keyboard focus.
func (e *Element) Focus() error {
	_, err := Call[chromedp.Void](e, "function() { this.focus(); }")
	return err
}

// Fill replaces the contents of a text field, as pasting would.
func (e *Element) Fill(text string) error {
	if err := e.Focus(); err != nil {
		return err
	}
	if text == "" {
		_, err := Call[chromedp.Void](e, `function() {
			this.value = '';
			this.dispatchEvent(new Event('input', { bubbles: true }));
		}`)
		return err
	}
	if _, err := Call[chromedp.Void](e, "function() { this.select(); }"); err != nil {
		return err
	}
	if _, err := chromedp.Call(e.browser.ctx, input.InsertText, input.InsertTextParams{Text: text}); err != nil {
		return err
	}
	return e.expectValue(text)
}

// expectValue fails when the field does not hold text. The text goes to
// whatever has the focus, and a page that rebuilds itself between the focus and
// the typing can take the focus away.
func (e *Element) expectValue(text string) error {
	actual, err := e.Value()
	if err != nil {
		return err
	}
	if actual != text {
		return fmt.Errorf("the field holds %q after filling in %q", actual, text)
	}
	return nil
}

// ChooseFile gives a file input the file at path, as the file dialog would.
func (e *Element) ChooseFile(path string) error {
	_, err := chromedp.Call(e.browser.ctx, dom.SetFileInputFiles, dom.SetFileInputFilesParams{
		Files: []string{path}, ObjectID: e.id,
	})
	return err
}

// Hover moves the pointer over the element, as a person reaching for its
// hidden actions would.
func (e *Element) Hover() error {
	point, err := e.point(centred)
	if err != nil {
		return err
	}
	return chromedp.Do(e.browser.ctx, chromedp.MouseEvent(input.DispatchMouseEventTypeMouseMoved, point.X, point.Y))
}

// clickPoint is where a native click lands.
type clickPoint struct {
	Visible bool    `json:"visible"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
}

// Placement says where on the element a click lands, and with which modifier
// keys held. The zero value is the centre, with none.
type Placement struct {
	// OffsetX and OffsetY move the point away from the element's centre.
	OffsetX float64
	OffsetY float64
	// Modifiers is a bit field, for example chromedp.ModifierShift.
	Modifiers chromedp.Modifier
	// AsShown clicks the element where it is, for a step that is about where
	// the page is scrolled. Otherwise the element is scrolled to the centre.
	AsShown bool
}

// Where an element is scrolled to before the pointer goes to it, as a
// scrollIntoView block.
const (
	centred = "center"
	asShown = ""
)

// Click is a native click: the element is scrolled into view, unless the
// placement keeps it where it is, then a real mouse press and release go to the
// point the placement names. Whatever covers that point receives them.
func (e *Element) Click(placement Placement) error {
	if err := e.browser.WaitForLoad(); err != nil {
		return err
	}
	block := centred
	if placement.AsShown {
		block = asShown
	}
	point, err := e.point(block)
	if err != nil {
		return err
	}
	x, y := point.X+placement.OffsetX, point.Y+placement.OffsetY
	return chromedp.Do(e.browser.ctx,
		chromedp.MouseEvent(input.DispatchMouseEventTypeMouseMoved, x, y),
		chromedp.MouseClickXY(x, y, chromedp.ButtonModifiers(placement.Modifiers)),
	)
}

// point is the centre of the element, after scrolling it to block unless that
// is asShown.
func (e *Element) point(block string) (clickPoint, error) {
	point, err := Call[clickPoint](e, `function(block) {
		if (!this.checkVisibility({ opacityProperty: true, visibilityProperty: true })) {
			return { visible: false, x: 0, y: 0 };
		}
		if (block) this.scrollIntoView({ block, inline: 'nearest', behavior: 'instant' });
		const r = this.getBoundingClientRect();
		return { visible: true, x: r.left + r.width / 2, y: r.top + r.height / 2 };
	}`, block)
	if err != nil {
		return clickPoint{}, err
	}
	if !point.Visible {
		return clickPoint{}, fmt.Errorf("element not interactable: %s is not displayed", e)
	}
	return point, nil
}
