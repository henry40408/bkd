//! Per-IP limit on attempts, kept in memory. It slows down guessing of passwords and
//! of the setup token; it resets when the process restarts.
//!
//! Callers charge an attempt *before* checking the secret (`try_acquire`) and give the
//! charge back only when the attempt succeeds (`refund`). Charging first matters:
//! checking a password takes tens of milliseconds, and an attacker who sends many
//! requests at once would otherwise get them all past a check-then-record limiter.

use std::collections::HashMap;
use std::net::IpAddr;
use std::sync::Mutex;
use std::time::{Duration, Instant};

// Bounds memory. When the table is full of live entries, new addresses are refused.
const MAX_TRACKED_ADDRESSES: usize = 10_000;

pub struct RateLimiter {
    max_attempts: u32,
    window: Duration,
    entries: Mutex<HashMap<IpAddr, Entry>>,
}

struct Entry {
    attempts: u32,
    window_started: Instant,
}

impl RateLimiter {
    /// Allows `max_attempts` charged attempts per address in each `window`.
    pub fn new(max_attempts: u32, window: Duration) -> Self {
        Self {
            max_attempts,
            window,
            entries: Mutex::new(HashMap::new()),
        }
    }

    /// Charges one attempt. `Err` carries how long the caller must wait.
    pub fn try_acquire(&self, address: IpAddr, now: Instant) -> Result<(), Duration> {
        let mut entries = self.entries.lock().expect("rate limiter lock poisoned");
        let key = bucket(address);
        if !entries.contains_key(&key) && entries.len() >= MAX_TRACKED_ADDRESSES {
            entries.retain(|_, entry| now < entry.window_started + self.window);
            if entries.len() >= MAX_TRACKED_ADDRESSES {
                return Err(self.window);
            }
        }
        let entry = entries.entry(key).or_insert(Entry {
            attempts: 0,
            window_started: now,
        });
        let window_ends = entry.window_started + self.window;
        if now >= window_ends {
            *entry = Entry {
                attempts: 0,
                window_started: now,
            };
        } else if entry.attempts >= self.max_attempts {
            return Err(window_ends - now);
        }
        entry.attempts += 1;
        Ok(())
    }

    /// Gives back the charge of one attempt that succeeded, so a legitimate user is
    /// not slowed down. Failed attempts keep their charge until the window ends: a
    /// success must not wipe them, or an attacker with an account of their own could
    /// reset the count between guesses.
    pub fn refund(&self, address: IpAddr) {
        let mut entries = self.entries.lock().expect("rate limiter lock poisoned");
        if let Some(entry) = entries.get_mut(&bucket(address)) {
            entry.attempts = entry.attempts.saturating_sub(1);
        }
    }
}

/// Maps an address to the unit that shares one budget.
fn bucket(address: IpAddr) -> IpAddr {
    // A dual-stack socket reports IPv4 clients as `::ffff:a.b.c.d`. Without this
    // they would all fall into the single IPv6 bucket below.
    match address.to_canonical() {
        ip @ IpAddr::V4(_) => ip,
        // One person usually controls a whole /64, so counting single addresses
        // would let them dodge the limit by rotating within it.
        IpAddr::V6(v6) => {
            let mut octets = v6.octets();
            octets[8..].fill(0);
            IpAddr::V6(octets.into())
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const WINDOW: Duration = Duration::from_secs(60);

    fn limiter() -> RateLimiter {
        RateLimiter::new(3, WINDOW)
    }

    fn address(text: &str) -> IpAddr {
        text.parse().unwrap()
    }

    fn use_up(limiter: &RateLimiter, ip: IpAddr, now: Instant) {
        for _ in 0..3 {
            limiter.try_acquire(ip, now).unwrap();
        }
    }

    #[test]
    fn attempts_up_to_the_limit_are_allowed() {
        let limiter = limiter();
        let now = Instant::now();
        let ip = address("192.0.2.1");
        use_up(&limiter, ip, now);
        assert!(limiter.try_acquire(ip, now).is_err());
    }

    #[test]
    fn the_wait_shrinks_until_the_window_ends() {
        let limiter = limiter();
        let start = Instant::now();
        let ip = address("192.0.2.1");
        use_up(&limiter, ip, start);
        let later = start + Duration::from_secs(20);
        assert_eq!(limiter.try_acquire(ip, later), Err(Duration::from_secs(40)));
        assert!(limiter.try_acquire(ip, start + WINDOW).is_ok());
    }

    #[test]
    fn a_new_window_starts_counting_from_zero() {
        let limiter = limiter();
        let start = Instant::now();
        let ip = address("192.0.2.1");
        use_up(&limiter, ip, start);
        let next_window = start + WINDOW;
        use_up(&limiter, ip, next_window);
        assert!(limiter.try_acquire(ip, next_window).is_err());
    }

    #[test]
    fn addresses_are_counted_separately() {
        let limiter = limiter();
        let now = Instant::now();
        use_up(&limiter, address("192.0.2.1"), now);
        assert!(limiter.try_acquire(address("192.0.2.2"), now).is_ok());
    }

    #[test]
    fn a_refund_gives_back_one_attempt_only() {
        let limiter = limiter();
        let now = Instant::now();
        let ip = address("192.0.2.1");
        use_up(&limiter, ip, now);
        limiter.refund(ip);
        assert!(limiter.try_acquire(ip, now).is_ok());
        assert!(limiter.try_acquire(ip, now).is_err());
    }

    #[test]
    fn successes_cannot_erase_earlier_failures() {
        let limiter = limiter();
        let now = Instant::now();
        let ip = address("192.0.2.1");
        // Two failed guesses, then one success that is refunded.
        limiter.try_acquire(ip, now).unwrap();
        limiter.try_acquire(ip, now).unwrap();
        limiter.try_acquire(ip, now).unwrap();
        limiter.refund(ip);
        limiter.try_acquire(ip, now).unwrap();
        limiter.refund(ip);
        assert!(limiter.try_acquire(ip, now).is_ok());
        assert!(limiter.try_acquire(ip, now).is_err());
    }

    #[test]
    fn refunding_an_unknown_address_does_nothing() {
        let limiter = limiter();
        limiter.refund(address("192.0.2.1"));
        assert!(limiter.entries.lock().unwrap().is_empty());
    }

    #[test]
    fn an_ipv6_network_shares_one_budget() {
        let limiter = limiter();
        let now = Instant::now();
        for host in 1..=3 {
            limiter
                .try_acquire(address(&format!("2001:db8:0:1::{host}")), now)
                .unwrap();
        }
        assert!(
            limiter
                .try_acquire(address("2001:db8:0:1:ffff::9"), now)
                .is_err()
        );
        assert!(limiter.try_acquire(address("2001:db8:0:2::1"), now).is_ok());
    }

    #[test]
    fn ipv4_clients_of_a_dual_stack_socket_keep_separate_budgets() {
        let limiter = limiter();
        let now = Instant::now();
        use_up(&limiter, address("::ffff:192.0.2.1"), now);
        assert!(
            limiter
                .try_acquire(address("::ffff:192.0.2.2"), now)
                .is_ok()
        );
        // The same client is one bucket however its address is written.
        assert!(limiter.try_acquire(address("192.0.2.1"), now).is_err());
    }

    #[test]
    fn expired_entries_are_dropped_when_the_table_is_full() {
        let limiter = limiter();
        let start = Instant::now();
        fill_table(&limiter, start);
        limiter
            .try_acquire(address("203.0.113.1"), start + WINDOW)
            .unwrap();
        assert_eq!(limiter.entries.lock().unwrap().len(), 1);
    }

    #[test]
    fn a_full_table_of_live_entries_refuses_new_addresses() {
        let limiter = limiter();
        let start = Instant::now();
        fill_table(&limiter, start);
        assert_eq!(
            limiter.try_acquire(address("203.0.113.1"), start),
            Err(WINDOW)
        );
        assert_eq!(limiter.entries.lock().unwrap().len(), MAX_TRACKED_ADDRESSES);
        // Addresses already tracked still work.
        assert!(limiter.try_acquire(address("0.0.0.0"), start).is_ok());
    }

    fn fill_table(limiter: &RateLimiter, now: Instant) {
        for index in 0..MAX_TRACKED_ADDRESSES {
            let ip = IpAddr::from(u32::try_from(index).unwrap().to_be_bytes());
            limiter.try_acquire(ip, now).unwrap();
        }
    }
}
