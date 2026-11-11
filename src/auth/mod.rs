//! Everything that decides who a request belongs to: passwords, sessions, forward
//! authentication, the first-admin setup token and the per-IP rate limit.

pub mod first_admin;
pub mod forward_auth;
pub mod password;
pub mod rate_limit;
pub mod session;
pub mod setup_token;
pub mod username;

use std::time::{SystemTime, UNIX_EPOCH};

use sha2::{Digest, Sha256};

/// Current time as Unix seconds, the unit every timestamp column uses.
pub fn unix_now() -> i64 {
    let since_epoch = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("system clock is before 1970");
    i64::try_from(since_epoch.as_secs()).expect("Unix seconds fit in i64")
}

/// Fills an array from the operating system's random source.
///
/// Panics when the source fails: every caller needs the bytes for a secret, and
/// continuing with predictable bytes would be worse than stopping.
fn random_bytes<const N: usize>() -> [u8; N] {
    let mut bytes = [0u8; N];
    getrandom::fill(&mut bytes).expect("operating system random source failed");
    bytes
}

fn to_hex(bytes: &[u8]) -> String {
    bytes.iter().map(|byte| format!("{byte:02x}")).collect()
}

fn sha256_hex(input: &str) -> String {
    to_hex(&Sha256::digest(input.as_bytes()))
}
