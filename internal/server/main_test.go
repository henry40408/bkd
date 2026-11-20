package server

import (
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"
)

const (
	// hangLimit is far above the package's normal run time (about ten seconds).
	hangLimit = 2 * time.Minute
	// heapLimit is far above what the tests need (a few hundred megabytes at most).
	heapLimit = 1 << 30
)

// TestMain ends the test binary when it hangs or balloons. A loop that stops
// advancing (which mutation testing produces on purpose, for example in
// the importer) can grow a buffer without end, at several hundred megabytes a
// second. Gremlins kills `go test`
// on its own timeout but not the test binary it started, so the orphan would
// eat all memory until the runner kills the whole job.
func TestMain(m *testing.M) {
	time.AfterFunc(hangLimit, func() { abort(fmt.Sprintf("still running after %s", hangLimit)) })
	go watchHeap()
	os.Exit(m.Run())
}

func watchHeap() {
	var stats runtime.MemStats
	for range time.Tick(50 * time.Millisecond) {
		runtime.ReadMemStats(&stats)
		if stats.HeapAlloc > heapLimit {
			abort("heap above the limit")
		}
	}
}

func abort(reason string) {
	fmt.Fprintf(os.Stderr, "server tests aborted, %s: a loop does not advance\n", reason)
	os.Exit(2)
}
