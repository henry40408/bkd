package auth

import (
	"net/netip"
	"sync"
	"time"
)

// Bounds memory. When the table is full of live entries, new addresses are refused.
const maxTrackedAddresses = 10_000

// Limiter is a per-address limit on attempts, kept in memory. It slows down
// guessing of passwords and of the setup token; it resets when the process
// restarts.
//
// Callers charge an attempt before checking the secret (TryAcquire) and give
// the charge back only when the attempt succeeds (Refund). Charging first
// matters: checking a password takes tens of milliseconds, and an attacker who
// sends many requests at once would otherwise get them all past a
// check-then-record limiter.
type Limiter struct {
	maxAttempts int
	window      time.Duration

	mu      sync.Mutex
	entries map[netip.Addr]*limitEntry
}

type limitEntry struct {
	attempts      int
	windowStarted time.Time
}

// NewLimiter allows maxAttempts charged attempts per address in each window.
func NewLimiter(maxAttempts int, window time.Duration) *Limiter {
	return &Limiter{
		maxAttempts: maxAttempts,
		window:      window,
		entries:     make(map[netip.Addr]*limitEntry),
	}
}

// TryAcquire charges one attempt. When it refuses, wait says how long the
// caller must wait.
func (limiter *Limiter) TryAcquire(address netip.Addr, now time.Time) (ok bool, wait time.Duration) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	key := bucketOf(address)
	if _, tracked := limiter.entries[key]; !tracked && len(limiter.entries) >= maxTrackedAddresses {
		limiter.dropExpired(now)
		if len(limiter.entries) >= maxTrackedAddresses {
			return false, limiter.window
		}
	}
	entry, tracked := limiter.entries[key]
	if !tracked {
		entry = &limitEntry{windowStarted: now}
		limiter.entries[key] = entry
	}
	windowEnds := entry.windowStarted.Add(limiter.window)
	if !now.Before(windowEnds) {
		*entry = limitEntry{windowStarted: now}
	} else if entry.attempts >= limiter.maxAttempts {
		return false, windowEnds.Sub(now)
	}
	entry.attempts++
	return true, 0
}

func (limiter *Limiter) dropExpired(now time.Time) {
	for key, entry := range limiter.entries {
		if !now.Before(entry.windowStarted.Add(limiter.window)) {
			delete(limiter.entries, key)
		}
	}
}

// Refund gives back the charge of one attempt that succeeded, so a legitimate
// user is not slowed down. Failed attempts keep their charge until the window
// ends: a success must not wipe them, or an attacker with an account of their
// own could reset the count between guesses.
func (limiter *Limiter) Refund(address netip.Addr) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	entry, tracked := limiter.entries[bucketOf(address)]
	if tracked && entry.attempts > 0 {
		entry.attempts--
	}
}

// bucketOf maps an address to the unit that shares one budget.
func bucketOf(address netip.Addr) netip.Addr {
	// A dual-stack socket reports IPv4 clients as ::ffff:a.b.c.d. Without this
	// they would all fall into the single IPv6 bucket below.
	address = address.Unmap().WithZone("")
	if address.Is4() {
		return address
	}
	// One person usually controls a whole /64, so counting single addresses
	// would let them dodge the limit by rotating within it.
	return netip.PrefixFrom(address, 64).Masked().Addr()
}
