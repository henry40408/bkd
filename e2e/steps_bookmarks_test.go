package e2e

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/henry40408/bkd/e2e/internal/harness"
)

const (
	searchBar = "[role=search]"
	addForm   = "#add form"
	editForm  = ".edit-form"
	// keepLink is Keep in a delete question, inline or on its own page.
	keepLink = "form:has(.danger-button) a.quiet-link"
	// loadMoreLink is the link that makes the list longer.
	loadMoreLink = "a#load-more"
)

func registerBookmarkSteps(sc *godog.ScenarioContext, w *world) {
	registerBookmarkSetupSteps(sc, w)
	registerBookmarkActionSteps(sc, w)
	registerSearchSteps(sc, w)
	registerNavigationSteps(sc, w)
	registerBookmarkAssertionSteps(sc, w)
}

func registerBookmarkSetupSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I have the bookmark `+quoted+` titled `+quoted+`$`, withTwoStrings(func(url, title string) error {
		return w.createBookmark(harness.NewBookmark{URL: url, Title: title})
	}))
	sc.Step(`^I have the bookmark `+quoted+` titled `+quoted+` tagged `+quoted+`$`, withThreeStrings(func(url, title, tag string) error {
		return w.createBookmark(harness.NewBookmark{URL: url, Title: title, Tags: []string{tag}})
	}))
	sc.Step(`^I have the bookmark `+quoted+` titled `+quoted+` with the note `+quoted+`$`, withThreeStrings(func(url, title, note string) error {
		return w.createBookmark(harness.NewBookmark{URL: url, Title: title, Note: note})
	}))
	sc.Step(`^I have (\d+) (?:more )?bookmarks?$`, w.haveBulkBookmarks)
}

func registerBookmarkActionSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I add the bookmark `+quoted+` titled `+quoted+` tagged `+quoted+`$`, withThreeStrings(w.addBookmark))
	sc.Step(`^I press Enter twice to add `+quoted+` titled `+quoted+`$`, withTwoStrings(w.submitAddFormTwice))
	sc.Step(`^I start editing `+quoted+`$`, withString(w.startEditing))
	sc.Step(`^I edit `+quoted+` to the title `+quoted+`$`, withTwoStrings(w.editBookmark))
	sc.Step(`^I save the edit with the URL `+quoted+`$`, withString(w.saveEditWithURL))
	sc.Step(`^I press Enter in the field `+quoted+`$`, withString(w.pressEnter))
	sc.Step(`^I fill the add form with the URL `+quoted+` and the title `+quoted+`$`, withTwoStrings(w.fillAddForm))
	sc.Step(`^I type the URL `+quoted+` in the edit form$`, withString(func(url string) error {
		return w.fill(editForm+" input[name=url]", url)
	}))
	sc.Step(`^the (add|edit) form (can|cannot) be saved$`, w.formCanBeSaved)
	sc.Step(`^the edit form holds `+quoted+` as the title$`, withString(func(title string) error {
		return w.titleFieldHolds(editForm, title)
	}))
	sc.Step(`^the edit form is closed$`, func() error {
		return harness.Eventually("the edit form is closed", func() (bool, error) {
			forms, err := w.browser.All(harness.CSS(editForm))
			return len(forms) == 0, err
		})
	})
	sc.Step(`^I cancel editing$`, func() error { return w.click(editForm + " a.quiet-link") })
	sc.Step(`^I start deleting `+quoted+`$`, withString(func(title string) error {
		return w.clickRowAction(title, "Delete")
	}))
	sc.Step(`^I keep it$`, func() error { return w.click(keepLink) })
	sc.Step(`^I confirm the deletion$`, func() error { return w.click("main button.danger-button") })
	sc.Step(`^I am asked `+quoted+`$`, withString(func(question string) error {
		return w.textContains(".inline-title", question)
	}))
	sc.Step(`^I am asked `+quoted+` on its own page$`, withString(func(question string) error {
		return w.textContains("main h1", question)
	}))
	sc.Step(`^I choose the tag `+quoted+`$`, withString(w.chooseSidebarFilter))
	sc.Step(`^I choose the link filter `+quoted+`$`, withString(w.chooseSidebarFilter))
	sc.Step(`^I choose Random$`, func() error { return w.clickLink(searchTools, "Random") })
	sc.Step(`^I choose Another$`, func() error { return w.clickLink("section.pick", "Another") })
	sc.Step(`^I close the pick$`, func() error { return w.clickLink("section.pick", "Close") })
	sc.Step(`^I choose Load more$`, func() error { return w.click(loadMoreLink) })
}

func registerSearchSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I search for `+quoted+`$`, withString(w.searchFor))
	sc.Step(`^the server answers the search for `+quoted+` slowly$`, withString(w.slowSearch))
	sc.Step(`^the search for `+quoted+` has been sent$`, withString(func(term string) error {
		return w.searchRecorded("searchSent", term)
	}))
	sc.Step(`^no slow answer has arrived yet$`, w.noSlowAnswerYet)
	sc.Step(`^the slow answer to the search for `+quoted+` has arrived$`, withString(w.slowAnswerArrived))
}

func registerNavigationSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the sidebar lists the link filter `+quoted+`$`, withString(func(label string) error {
		return w.textContains("#sidebar", label)
	}))
	sc.Step(`^the pick shows `+quoted+`$`, withString(func(text string) error {
		return w.textContains("section.pick", text)
	}))
	// The pick can only be absent after a positive check, so this one waits for it.
	sc.Step(`^the pick does not show `+quoted+`$`, withString(func(text string) error {
		return w.textLacks("section.pick", text)
	}))
	sc.Step(`^there is no pick$`, w.noPick)
	sc.Step(`^the add form holds `+quoted+` as the title$`, withString(func(title string) error { return w.titleFieldHolds(addForm, title) }))
	sc.Step(`^I am told `+quoted+`$`, withString(w.expectText))
	sc.Step(`^Load more is offered$`, func() error { return w.loadMoreOffered(true) })
	sc.Step(`^Load more is not offered$`, func() error { return w.loadMoreOffered(false) })
	sc.Step(`^the page title is still `+quoted+`$`, withString(w.pageTitleIs))
}

func registerBookmarkAssertionSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the list shows `+quoted+`$`, withString(func(text string) error { return w.textContains("#main", text) }))
	sc.Step(`^the list does not show `+quoted+`$`, withString(func(text string) error { return w.textLacks("#main", text) }))
	sc.Step(`^the age of `+quoted+` says `+quoted+`$`, withTwoStrings(w.ageSays))
	sc.Step(`^hovering the age of `+quoted+` shows the date as the browser formats it$`, withString(w.ageHoverIsBrowserFormatted))
	sc.Step(`^the hover text of the age of `+quoted+` names UTC$`, withString(w.ageHoverNamesUTC))
	sc.Step(`^the link `+quoted+` points to `+quoted+`$`, withTwoStrings(w.linkPointsTo))
	sc.Step(`^the sidebar lists the tag `+quoted+`$`, withString(func(tag string) error { return w.textContains("#sidebar", tag) }))
	sc.Step(`^the sidebar does not list the tag `+quoted+`$`, withString(func(tag string) error { return w.textLacks("#sidebar", tag) }))
	sc.Step(`^the account has exactly (\d+) bookmarks?$`, w.accountHasBookmarks)
	sc.Step(`^`+quoted+` is highlighted$`, withString(func(text string) error { return w.textContains("main mark", text) }))
	sc.Step(`^`+quoted+` is not highlighted$`, withString(func(text string) error { return w.textLacks("main mark", text) }))
	sc.Step(`^I see the empty message `+quoted+`$`, withString(func(text string) error { return w.textContains("main .empty", text) }))
	sc.Step(`^the list has (\d+) bookmarks$`, w.listHas)
}

func (w *world) createBookmark(bookmark harness.NewBookmark) error {
	session, err := w.mySession()
	if err != nil {
		return err
	}
	return w.api.CreateBookmark(session, bookmark)
}

func (w *world) haveBulkBookmarks(count int) error {
	return w.haveTaggedBulkBookmarks(count, "")
}

// haveTaggedBulkBookmarks makes bulk bookmarks, tagged when tag is not empty.
func (w *world) haveTaggedBulkBookmarks(count int, tag string) error {
	for range count {
		w.bulkCount++
		bookmark := harness.NewBookmark{
			URL:   fmt.Sprintf("https://bulk.test/%d", w.bulkCount),
			Title: fmt.Sprintf("Bulk %d", w.bulkCount),
		}
		if tag != "" {
			bookmark.Tags = []string{tag}
		}
		if err := w.createBookmark(bookmark); err != nil {
			return err
		}
	}
	return nil
}

// openAddForm opens the add form, which sits in a <details> that starts closed.
func (w *world) openAddForm() error {
	form, err := w.browser.First(harness.CSS(addForm))
	if err != nil {
		return err
	}
	shown, err := form.Displayed()
	if err != nil || shown {
		return err
	}
	return w.click("#add summary")
}

func (w *world) fillAddForm(url, title string) error {
	if err := w.openAddForm(); err != nil {
		return err
	}
	if err := w.fill(addForm+" input[name=url]", url); err != nil {
		return err
	}
	return w.fill(addForm+" input[name=title]", title)
}

func (w *world) addBookmark(url, title, tags string) error {
	if err := w.fillAddForm(url, title); err != nil {
		return err
	}
	if err := w.fill(addForm+" input[name=tags]", tags); err != nil {
		return err
	}
	return w.click(addForm + " button[type=submit]")
}

func (w *world) submitAddFormTwice(url, title string) error {
	if err := w.fillAddForm(url, title); err != nil {
		return err
	}
	return w.pressEnterTwice(addForm + " input[name=title]")
}

func rowOf(title string) string {
	return fmt.Sprintf("//li[contains(@class, 'item')][.//a[@class='title' and text()=%s]]", xpathString(title))
}

// clickRowAction clicks a row's Edit or Delete link, which show only while the
// pointer is over the row.
func (w *world) clickRowAction(title, action string) error {
	row, err := w.browser.Await(harness.XPath(rowOf(title)))
	if err != nil {
		return err
	}
	if err := row.Hover(); err != nil {
		return err
	}
	return w.click(fmt.Sprintf("a[aria-label='%s %s']", action, title))
}

func (w *world) startEditing(title string) error {
	if err := w.clickRowAction(title, "Edit"); err != nil {
		return err
	}
	_, err := w.element(editForm)
	return err
}

func (w *world) editBookmark(oldTitle, newTitle string) error {
	if err := w.startEditing(oldTitle); err != nil {
		return err
	}
	if err := w.fill(editForm+" input[name=title]", newTitle); err != nil {
		return err
	}
	return w.click(editForm + " button[type=submit]")
}

func (w *world) saveEditWithURL(url string) error {
	if err := w.fill(editForm+" input[name=url]", url); err != nil {
		return err
	}
	return w.click(editForm + " button[type=submit]")
}

// formCanBeSaved retries because the script updates the button after each keystroke.
func (w *world) formCanBeSaved(which, can string) error {
	selector := addForm
	if which == "edit" {
		selector = editForm
	}
	return w.saveButtonIs(selector, which, can)
}

// saveButtonIs waits for the form's submit button to be off ("cannot") or on ("can").
func (w *world) saveButtonIs(formSelector, name, can string) error {
	wantDisabled := can == "cannot"
	return harness.Eventually(fmt.Sprintf("the %s form %s be saved", name, can), func() (bool, error) {
		button, err := w.element(formSelector + " button[type=submit]")
		if err != nil {
			return false, err
		}
		_, disabled, err := button.Attr("disabled")
		return disabled == wantDisabled, err
	})
}

func (w *world) searchFor(query string) error {
	// The page may still be swapping in the previous answer, which makes the bar go stale.
	return harness.Eventually("the search bar takes the search", func() (bool, error) {
		if err := w.fill(searchBar+" input[name=q]", query); err != nil {
			return false, err
		}
		return true, w.click(searchBar + " button[type=submit]")
	})
}

// slowSearchScript holds back the answer to one search for 1.5 s in the
// browser, and records when the server answered it (searchSent) and when the
// page gets the answer (searchAnswered), so a scenario can make an old answer
// land after a newer one. A search still on its way when a newer one starts is
// aborted, and its answer would never be held back.
const slowSearchScript = `const term = arguments[0]; const slowMs = 1500;
	window.searchSent = []; window.searchAnswered = [];
	const fetchNow = window.fetch;
	window.fetch = async (url, ...rest) => {
		const slow = new URL(url, location.href).searchParams.get('q') === term;
		const response = await fetchNow(url, ...rest);
		if (!slow) return response;
		window.searchSent.push(term);
		await new Promise((resolve) => setTimeout(resolve, slowMs));
		window.searchAnswered.push(term);
		return response;
	};`

func (w *world) slowSearch(term string) error {
	_, err := harness.Exec[any](w.browser, slowSearchScript, term)
	return err
}

func (w *world) noSlowAnswerYet() error {
	answered, err := harness.Exec[int](w.browser, "return window.searchAnswered.length")
	if err != nil {
		return err
	}
	if answered != 0 {
		return fmt.Errorf("a slow answer has already arrived, so the order is not what the scenario needs")
	}
	return nil
}

func (w *world) slowAnswerArrived(term string) error {
	if err := w.searchRecorded("searchAnswered", term); err != nil {
		return err
	}
	// Let the page process the answer before the scenario looks at the list.
	time.Sleep(300 * time.Millisecond)
	return nil
}

func (w *world) searchRecorded(list, term string) error {
	script := fmt.Sprintf("return window.%s.includes(arguments[0])", list)
	return harness.Eventually(fmt.Sprintf("%s has %s", list, term), func() (bool, error) {
		return harness.Exec[bool](w.browser, script, term)
	})
}

// chooseSidebarFilter clicks a sidebar filter link; its label is followed by a count.
func (w *world) chooseSidebarFilter(label string) error {
	link := fmt.Sprintf("//nav[@id='sidebar']//li/a[starts-with(normalize-space(.), %s)]", xpathString(label))
	return w.clickLocator(harness.XPath(link), harness.Placement{})
}

func (w *world) noPick() error {
	return harness.Eventually("the pick is gone", func() (bool, error) {
		picks, err := w.browser.All(harness.CSS("section.pick"))
		return len(picks) == 0, err
	})
}

func (w *world) titleFieldHolds(form, expected string) error {
	field, err := w.element(form + " input[name=title]")
	if err != nil {
		return err
	}
	actual, err := field.Value()
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("the title field holds %q", actual)
	}
	return nil
}

func (w *world) loadMoreOffered(offered bool) error {
	return harness.Eventually(fmt.Sprintf("Load more is offered: %t", offered), func() (bool, error) {
		links, err := w.browser.All(harness.CSS(loadMoreLink))
		return (len(links) == 0) != offered, err
	})
}

func (w *world) pageTitleIs(expected string) error {
	actual, err := w.browser.Title()
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("the page title is %q", actual)
	}
	return nil
}

// ageOf is the <time> element in the row of the bookmark with this title.
func ageOf(title string) harness.Locator {
	return harness.XPath(rowOf(title) + "//time")
}

func (w *world) ageSays(title, expected string) error {
	age, err := w.browser.Await(ageOf(title))
	if err != nil {
		return err
	}
	actual, err := age.Text()
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("the age says %q", actual)
	}
	return nil
}

// ageHoverIsBrowserFormatted: the script shows the hover text in the reader's
// locale and time zone, which only the browser itself can tell; the server's
// text is in UTC.
func (w *world) ageHoverIsBrowserFormatted(title string) error {
	age, err := w.browser.Await(ageOf(title))
	if err != nil {
		return err
	}
	hoverText, _, err := age.Attr("title")
	if err != nil {
		return err
	}
	if hoverText == "" {
		return fmt.Errorf("the age has no hover text")
	}
	expected, err := harness.Call[string](age, "function() { return new Date(this.dateTime).toLocaleString(); }")
	if err != nil {
		return err
	}
	// A bookmark made a moment ago: a wrong conversion would still agree with
	// itself above, so the instant is checked against the clock too.
	secondsOff, err := harness.Call[float64](age,
		"function() { return Math.abs(Date.now() - Date.parse(this.dateTime)) / 1000; }")
	if err != nil {
		return err
	}
	if math.IsNaN(secondsOff) || secondsOff >= 120 {
		return fmt.Errorf("datetime is %s seconds from now", strconv.FormatFloat(secondsOff, 'f', -1, 64))
	}
	if hoverText != expected {
		return fmt.Errorf("hover text %q, the browser formats %q", hoverText, expected)
	}
	return nil
}

func (w *world) ageHoverNamesUTC(title string) error {
	age, err := w.browser.Await(ageOf(title))
	if err != nil {
		return err
	}
	hoverText, _, err := age.Attr("title")
	if err != nil {
		return err
	}
	if !strings.Contains(hoverText, "UTC") {
		return fmt.Errorf("the hover text is %q", hoverText)
	}
	return nil
}

func (w *world) linkPointsTo(title, href string) error {
	link, err := w.browser.Await(harness.XPath(fmt.Sprintf("//li//a[@class='title' and text()=%s]", xpathString(title))))
	if err != nil {
		return err
	}
	actual, found, err := link.Attr("href")
	if err != nil {
		return err
	}
	if !found || actual != href {
		return fmt.Errorf("the link goes to %q", actual)
	}
	return nil
}

func (w *world) accountHasBookmarks(expected int) error {
	// A repeated request, if the page wrongly sent one, is still in flight here.
	time.Sleep(500 * time.Millisecond)
	session, err := w.mySession()
	if err != nil {
		return err
	}
	bookmarks, err := w.api.Bookmarks(session)
	if err != nil {
		return err
	}
	if len(bookmarks) != expected {
		return fmt.Errorf("the account has %d bookmarks", len(bookmarks))
	}
	return nil
}

func (w *world) listHas(expected int) error {
	return harness.Eventually(fmt.Sprintf("the list has %d bookmarks", expected), func() (bool, error) {
		items, err := w.browser.All(harness.CSS("ul.list li.item"))
		return len(items) == expected, err
	})
}
