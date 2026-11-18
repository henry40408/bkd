package linkcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func TestStatusBoundariesBetweenTheOutcomes(t *testing.T) {
	requested := mustParseURL(t, "https://a.test/page")
	cases := map[int]Status{
		199: StatusUnknown, 200: StatusOK, 299: StatusOK, 300: StatusUnknown,
		399: StatusUnknown, 400: StatusBroken, 599: StatusBroken, 600: StatusUnknown,
	}
	for code, want := range cases {
		got := verdictFor(requested, answer{status: code, finalURL: requested}).Status
		if got != want {
			t.Errorf("%d: got %s, want %s", code, got, want)
		}
	}
}

func TestOnlyASuccessOrARefusalToBeAskedAgainIsFinal(t *testing.T) {
	cases := map[int]bool{
		199: false, 200: true, 299: true, 300: false, 404: false, 429: true, 503: true, 500: false,
	}
	for code, want := range cases {
		if got := (answer{status: code}).isFinal(); got != want {
			t.Errorf("%d: isFinal = %v, want %v", code, got, want)
		}
	}
	if !(answer{status: 403, challenged: true}).isFinal() {
		t.Error("a bot check is final")
	}
}

func TestAnEmptyQueryCountsAsAQueryOnlyWhenTheQuestionMarkIsThere(t *testing.T) {
	compare := func(address string) string { return normalizeForCompare(mustParseURL(t, address)) }
	assertEqual(t, compare("https://a.test/"), "https://a.test/")
	assertEqual(t, compare("https://a.test/?"), "https://a.test/?")
	assertEqual(t, compare("https://a.test/?x=1"), "https://a.test/?x=1")
}

func TestLegacyIPv4PartsAreBoundedByTheirPosition(t *testing.T) {
	valid := map[string]string{
		"4294967295":  "255.255.255.255",
		"1.16777215":  "1.255.255.255",
		"1.2.65535":   "1.2.255.255",
		"1.2.3.255":   "1.2.3.255",
		"255.0.0.1":   "255.0.0.1",
		"255.255.0.1": "255.255.0.1",
	}
	for host, want := range valid {
		got, ok := parseLegacyIPv4(host)
		if !ok || got.String() != want {
			t.Errorf("%s: got %v %v, want %s", host, got, ok, want)
		}
	}
	for _, host := range []string{"4294967296", "1.16777216", "1.2.65536", "1.2.3.256", "256.0.0.1", "1.256.0.1"} {
		if _, ok := parseLegacyIPv4(host); ok {
			t.Errorf("%s must not parse", host)
		}
	}
}

func TestIPv6RangesStopExactlyWhereTheyAreDocumented(t *testing.T) {
	for text, want := range map[string]bool{
		"2001:1ff::1":  false,
		"2001:200::1":  true,
		"2001:db8::1":  false,
		"3fff:fff::1":  false,
		"3fff:1000::1": true,
	} {
		if got := isPublic(netip.MustParseAddr(text)); got != want {
			t.Errorf("%s: public = %v, want %v", text, got, want)
		}
	}
}

func TestTheFirstTargetOfAHostIsNotDelayed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()

	started := time.Now()
	collect(t, loopbackChecker(0).StartPaced(context.Background(), targetsOn(server.URL, 1), 3*time.Second))

	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("a lone target waited %v for a pause that is only between targets", elapsed)
	}
}

func TestSleepWithoutAWaitStillNoticesCancellation(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for range 200 {
		if sleep(cancelled, 0) {
			t.Fatal("slept on a cancelled context")
		}
	}
	if !sleep(context.Background(), 0) {
		t.Fatal("a zero wait on a live context must go on")
	}
}
