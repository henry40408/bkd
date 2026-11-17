package auth

import (
	"encoding/binary"
	"net/netip"
	"testing"
	"time"
)

const testWindow = 60 * time.Second

func newTestLimiter() *Limiter { return NewLimiter(3, testWindow) }

func address(text string) netip.Addr { return netip.MustParseAddr(text) }

func mustAcquire(t *testing.T, limiter *Limiter, ip netip.Addr, now time.Time) {
	t.Helper()
	if ok, _ := limiter.TryAcquire(ip, now); !ok {
		t.Fatalf("%v was refused", ip)
	}
}

func mustRefuse(t *testing.T, limiter *Limiter, ip netip.Addr, now time.Time) {
	t.Helper()
	if ok, _ := limiter.TryAcquire(ip, now); ok {
		t.Fatalf("%v was allowed", ip)
	}
}

func useUp(t *testing.T, limiter *Limiter, ip netip.Addr, now time.Time) {
	t.Helper()
	for range 3 {
		mustAcquire(t, limiter, ip, now)
	}
}

func TestAttemptsUpToTheLimitAreAllowed(t *testing.T) {
	limiter, now, ip := newTestLimiter(), time.Now(), address("192.0.2.1")
	useUp(t, limiter, ip, now)
	mustRefuse(t, limiter, ip, now)
}

func TestTheWaitShrinksUntilTheWindowEnds(t *testing.T) {
	limiter, start, ip := newTestLimiter(), time.Now(), address("192.0.2.1")
	useUp(t, limiter, ip, start)
	ok, wait := limiter.TryAcquire(ip, start.Add(20*time.Second))
	if ok || wait != 40*time.Second {
		t.Fatalf("ok = %v, wait = %v", ok, wait)
	}
	mustAcquire(t, limiter, ip, start.Add(testWindow))
}

func TestANewWindowStartsCountingFromZero(t *testing.T) {
	limiter, start, ip := newTestLimiter(), time.Now(), address("192.0.2.1")
	useUp(t, limiter, ip, start)
	next := start.Add(testWindow)
	useUp(t, limiter, ip, next)
	mustRefuse(t, limiter, ip, next)
}

func TestAddressesAreCountedSeparately(t *testing.T) {
	limiter, now := newTestLimiter(), time.Now()
	useUp(t, limiter, address("192.0.2.1"), now)
	mustAcquire(t, limiter, address("192.0.2.2"), now)
}

func TestARefundGivesBackOneAttemptOnly(t *testing.T) {
	limiter, now, ip := newTestLimiter(), time.Now(), address("192.0.2.1")
	useUp(t, limiter, ip, now)
	limiter.Refund(ip)
	mustAcquire(t, limiter, ip, now)
	mustRefuse(t, limiter, ip, now)
}

func TestSuccessesCannotEraseEarlierFailures(t *testing.T) {
	limiter, now, ip := newTestLimiter(), time.Now(), address("192.0.2.1")
	// Two failed guesses, then one success that is refunded.
	mustAcquire(t, limiter, ip, now)
	mustAcquire(t, limiter, ip, now)
	mustAcquire(t, limiter, ip, now)
	limiter.Refund(ip)
	mustAcquire(t, limiter, ip, now)
	limiter.Refund(ip)
	mustAcquire(t, limiter, ip, now)
	mustRefuse(t, limiter, ip, now)
}

func TestRefundingAnUnknownAddressDoesNothing(t *testing.T) {
	limiter := newTestLimiter()
	limiter.Refund(address("192.0.2.1"))
	if len(limiter.entries) != 0 {
		t.Fatalf("%d entries", len(limiter.entries))
	}
}

func TestAnIPv6NetworkSharesOneBudget(t *testing.T) {
	limiter, now := newTestLimiter(), time.Now()
	for _, host := range []string{"2001:db8:0:1::1", "2001:db8:0:1::2", "2001:db8:0:1::3"} {
		mustAcquire(t, limiter, address(host), now)
	}
	mustRefuse(t, limiter, address("2001:db8:0:1:ffff::9"), now)
	mustAcquire(t, limiter, address("2001:db8:0:2::1"), now)
}

func TestIPv4ClientsOfADualStackSocketKeepSeparateBudgets(t *testing.T) {
	limiter, now := newTestLimiter(), time.Now()
	useUp(t, limiter, address("::ffff:192.0.2.1"), now)
	mustAcquire(t, limiter, address("::ffff:192.0.2.2"), now)
	// The same client is one bucket however its address is written.
	mustRefuse(t, limiter, address("192.0.2.1"), now)
}

func fillTable(t *testing.T, limiter *Limiter, now time.Time) {
	t.Helper()
	for index := range maxTrackedAddresses {
		var octets [4]byte
		binary.BigEndian.PutUint32(octets[:], uint32(index))
		mustAcquire(t, limiter, netip.AddrFrom4(octets), now)
	}
}

func TestExpiredEntriesAreDroppedWhenTheTableIsFull(t *testing.T) {
	limiter, start := newTestLimiter(), time.Now()
	fillTable(t, limiter, start)
	mustAcquire(t, limiter, address("203.0.113.1"), start.Add(testWindow))
	if len(limiter.entries) != 1 {
		t.Fatalf("%d entries", len(limiter.entries))
	}
}

func TestAFullTableOfLiveEntriesRefusesNewAddresses(t *testing.T) {
	limiter, start := newTestLimiter(), time.Now()
	fillTable(t, limiter, start)
	ok, wait := limiter.TryAcquire(address("203.0.113.1"), start)
	if ok || wait != testWindow {
		t.Fatalf("ok = %v, wait = %v", ok, wait)
	}
	if len(limiter.entries) != maxTrackedAddresses {
		t.Fatalf("%d entries", len(limiter.entries))
	}
	// Addresses already tracked still work.
	mustAcquire(t, limiter, address("0.0.0.0"), start)
}
