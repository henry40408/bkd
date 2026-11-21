package e2e

import (
	"fmt"
	"os"

	"github.com/cucumber/godog"

	"github.com/henry40408/bkd/e2e/internal/harness"
)

const importForm = "form[action='/transfer/import']"

// browserFile is what Firefox writes: a folder, two web links (one with a note
// and a tag) and a smart bookmark that is not a web link.
const browserFile = `<!DOCTYPE NETSCAPE-Bookmark-file-1>
<META HTTP-EQUIV="Content-Type" CONTENT="text/html; charset=UTF-8">
<TITLE>Bookmarks</TITLE>
<H1>Bookmarks Menu</H1>
<DL><p>
    <DT><H3>Recipes</H3>
    <DL><p>
        <DT><A HREF="https://soup.test/" ADD_DATE="1600000002" TAGS="winter">Tomato soup</A>
        <DD>use less salt
        <DT><A HREF="https://bread.test/" ADD_DATE="1600000003">Fresh bread</A>
        <DT><A HREF="place:parent=1&amp;type=7">Smart bookmark</A>
    </DL><p>
</DL><p>
`

func registerTransferSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I open Import and export$`, w.openImportAndExport)
	sc.Step(`^I import a bookmarks file exported from a browser$`, w.importBrowserFile)
	sc.Step(`^I import without choosing a file$`, func() error { return w.click(importForm + " button[type=submit]") })
	sc.Step(`^the import result says `+quoted+`$`, withString(func(expected string) error {
		return w.textContains(".import-result", expected)
	}))
	sc.Step(`^importing my own export adds nothing new$`, w.ownExportAddsNothing)
	sc.Step(`^the download links point to the HTML and JSON exports$`, w.downloadLinksPoint)
}

func (w *world) openImportAndExport() error {
	if err := w.click("#sidebar a[href='/transfer']"); err != nil {
		return err
	}
	_, err := w.element(importForm)
	return err
}

func (w *world) importBrowserFile() error {
	file, err := os.CreateTemp("", "bkd-import-*.html")
	if err != nil {
		return err
	}
	// The browser reads the file when the form is submitted, so it must outlive the click.
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err := file.WriteString(browserFile); err != nil {
		_ = file.Close() // the write error is the one to report
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	input, err := w.element(importForm + " input[type=file]")
	if err != nil {
		return err
	}
	if err := input.ChooseFile(file.Name()); err != nil {
		return err
	}
	if err := w.click(importForm + " button[type=submit]"); err != nil {
		return err
	}
	_, err = w.element(".import-result")
	return err
}

func (w *world) ownExportAddsNothing() error {
	session, err := w.mySession()
	if err != nil {
		return err
	}
	before, err := w.api.Bookmarks(session)
	if err != nil {
		return err
	}
	exported, err := w.api.ExportHTML(session)
	if err != nil {
		return err
	}
	report, err := w.api.ImportHTML(session, exported)
	if err != nil {
		return err
	}
	if report.Added != 0 {
		return fmt.Errorf("the import added bookmarks: %+v", report)
	}
	if report.Invalid != 0 {
		return fmt.Errorf("the import refused links: %+v", report)
	}
	if report.Duplicates != len(before) {
		return fmt.Errorf("unexpected report: %+v", report)
	}
	after, err := w.api.Bookmarks(session)
	if err != nil {
		return err
	}
	if len(after) != len(before) {
		return fmt.Errorf("the account went from %d to %d bookmarks", len(before), len(after))
	}
	return nil
}

func (w *world) downloadLinksPoint() error {
	downloads := [][2]string{
		{"Download HTML", "/api/export?format=html"},
		{"Download JSON", "/api/export?format=json"},
	}
	for _, download := range downloads {
		if err := w.downloadLinkPoints(download[0], download[1]); err != nil {
			return err
		}
	}
	return nil
}

func (w *world) downloadLinkPoints(label, href string) error {
	link, err := w.browser.Await(harness.XPath(fmt.Sprintf("//a[normalize-space(.)=%s]", xpathString(label))))
	if err != nil {
		return err
	}
	actual, _, err := link.Attr("href")
	if err != nil {
		return err
	}
	if actual != href {
		return fmt.Errorf("%s points to %q", label, actual)
	}
	_, isDownload, err := link.Attr("download")
	if err != nil {
		return err
	}
	if !isDownload {
		return fmt.Errorf("%s is not a download", label)
	}
	return nil
}
