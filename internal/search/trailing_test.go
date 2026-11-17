package search

import "testing"

func TestAQueryEndingInABackslashInsideAQuoteDoesNotPanic(t *testing.T) {
	for _, query := range []string{`"abc\`, `\`, `"\`, `title:"x\`} {
		NewFilter(query, "")
	}
}
