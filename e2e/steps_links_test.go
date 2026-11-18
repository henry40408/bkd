package e2e

import (
	"github.com/cucumber/godog"

	"github.com/henry40408/bkd/e2e/internal/harness"
)

// slowRequestsScript waits before the request is sent, so that Stop can still
// end it, and the progress line stays on screen long enough to be read.
const slowRequestsScript = `const part = arguments[0]; const slowMs = 2000;
	const fetchNow = window.fetch;
	window.fetch = (url, ...rest) => String(url).includes(part)
		? new Promise((resolve) => setTimeout(resolve, slowMs)).then(() => fetchNow(url, ...rest))
		: fetchNow(url, ...rest);`

func registerLinkSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the server answers requests for `+quoted+` slowly$`, withString(func(part string) error {
		_, err := harness.Exec[any](w.browser, slowRequestsScript, part)
		return err
	}))
	sc.Step(`^the link check shows `+quoted+`$`, withString(func(text string) error {
		return w.textContains(".check-progress", text)
	}))
	sc.Step(`^I stop the link check$`, func() error { return w.click(".check-progress button") })
}
