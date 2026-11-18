package importer

import "testing"

func TestTheSecondsLimitItselfIsAlreadyTooLargeForSeconds(t *testing.T) {
	assertEqual(t, toSeconds(secondsLimit-1), secondsLimit-1)
	assertEqual(t, toSeconds(secondsLimit), secondsLimit/1000)
}

func TestADateOfOneSecondIsKept(t *testing.T) {
	assertEqual(t, createdAt(ptr(1), now), int64(1))
}
