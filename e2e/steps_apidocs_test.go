package e2e

import (
	"fmt"

	"github.com/cucumber/godog"
)

const (
	docsPage = "#swagger-ui"
	// The top button, not the lock beside each operation.
	docsAuthorizeButton = docsPage + " .scheme-container .authorize"
	docsTokenField      = ".auth-container input[type=text]"
	docsSubmitToken     = ".auth-btn-wrapper .authorize"
	docsCloseDialog     = ".modal-btn.btn-done"
)

func registerAPIDocsSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the API documentation lists `+quoted+`$`, withString(func(text string) error { return w.textContains(docsPage, text) }))
	sc.Step(`^I authorize the API documentation with the new token$`, w.authorizeDocs)
	sc.Step(`^I try out `+quoted+` with the body `+quoted+`$`, withTwoStrings(w.tryOutOperation))
	sc.Step(`^the API documentation answers `+quoted+` for `+quoted+`$`, withTwoStrings(w.docsAnswer))
}

// operationBlock finds an operation by its operationId: Swagger UI puts the tag
// in front of it in the element id.
func operationBlock(operationID string) string {
	return fmt.Sprintf("[id^='operations-'][id$='-%s']", operationID)
}

func (w *world) authorizeDocs() error {
	if w.apiToken == "" {
		return fmt.Errorf("no step created an API token")
	}
	if err := w.click(docsAuthorizeButton); err != nil {
		return err
	}
	if err := w.fill(docsTokenField, "Token "+w.apiToken); err != nil {
		return err
	}
	if err := w.click(docsSubmitToken); err != nil {
		return err
	}
	return w.click(docsCloseDialog)
}

func (w *world) tryOutOperation(operationID, body string) error {
	block := operationBlock(operationID)
	if err := w.click(block + " .opblock-summary"); err != nil {
		return err
	}
	if err := w.click(block + " .try-out__btn"); err != nil {
		return err
	}
	if err := w.fill(block+" textarea.body-param__text", body); err != nil {
		return err
	}
	return w.click(block + " .execute")
}

// docsAnswer reads the status of the live answer, not the table's header, which
// says "Code".
func (w *world) docsAnswer(status, operationID string) error {
	return w.textContains(operationBlock(operationID)+" .live-responses-table tr.response .response-col_status", status)
}
