//! Password hashing with Argon2id. Hashing takes tens of milliseconds of CPU, so the
//! async functions run it on the blocking thread pool instead of a runtime worker.

use std::fmt;
use std::sync::{Arc, OnceLock};
use std::thread::available_parallelism;

use anyhow::{Result, anyhow};
use argon2::Argon2;
use argon2::password_hash::{PasswordHasher, PasswordVerifier};
use tokio::sync::Semaphore;

const MIN_LENGTH: usize = 8;
// Argon2 cost grows with input size, so an unbounded password is a cheap way to burn CPU.
const MAX_BYTES: usize = 256;

#[derive(Debug, PartialEq, Eq)]
pub enum PasswordError {
    TooShort,
    TooLong,
}

impl fmt::Display for PasswordError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::TooShort => write!(f, "password must be at least {MIN_LENGTH} characters"),
            Self::TooLong => write!(f, "password must be at most {MAX_BYTES} bytes"),
        }
    }
}

impl std::error::Error for PasswordError {}

/// Checks a new password. Login does not call this: an old password that no longer
/// meets the rules must still work.
pub fn validate(password: &str) -> Result<(), PasswordError> {
    if password.len() > MAX_BYTES {
        return Err(PasswordError::TooLong);
    }
    if password.chars().count() < MIN_LENGTH {
        return Err(PasswordError::TooShort);
    }
    Ok(())
}

pub fn hash(password: &str) -> Result<String> {
    let hash = Argon2::default()
        .hash_password(password.as_bytes())
        .map_err(|error| anyhow!("cannot hash password: {error}"))?;
    Ok(hash.to_string())
}

/// Returns false for a wrong password and for a stored hash that cannot be parsed.
pub fn verify(password: &str, stored_hash: &str) -> bool {
    // Oversized input is refused before hashing, for the same reason as in `validate`.
    if password.len() > MAX_BYTES {
        return false;
    }
    Argon2::default()
        .verify_password(password.as_bytes(), stored_hash)
        .is_ok()
}

/// The hash that `verify_against_dummy` checks against. Built once.
fn dummy_hash() -> &'static str {
    static DUMMY_HASH: OnceLock<String> = OnceLock::new();
    DUMMY_HASH.get_or_init(|| hash("dummy password").expect("hashing a constant works"))
}

/// Builds the dummy hash now, so the first login attempt is not slower than the rest.
/// Call it once at startup.
pub fn warm_up() {
    dummy_hash();
}

/// Spends the same time as a real check. Call it when the username does not exist
/// or the account is disabled, so response time does not reveal which usernames exist.
pub fn verify_against_dummy(password: &str) {
    let _ = verify(password, dummy_hash());
}

/// Each Argon2 run allocates about 19 MiB. Without a cap, a burst of login requests
/// from many addresses could use gigabytes at once, so only as many runs as there are
/// CPU cores go at the same time and the rest wait their turn.
async fn run_limited<T: Send + 'static>(work: impl FnOnce() -> T + Send + 'static) -> Result<T> {
    static PERMITS: OnceLock<Arc<Semaphore>> = OnceLock::new();
    let permits = PERMITS.get_or_init(|| {
        Arc::new(Semaphore::new(
            available_parallelism().map_or(2, usize::from),
        ))
    });
    let permit = permits.clone().acquire_owned().await?;
    // The permit moves into the blocking task: if the request is cancelled, the
    // Argon2 run still goes on, so the permit must stay held until it ends.
    Ok(tokio::task::spawn_blocking(move || {
        let _permit = permit;
        work()
    })
    .await?)
}

pub async fn hash_async(password: String) -> Result<String> {
    run_limited(move || hash(&password)).await?
}

pub async fn verify_against_dummy_async(password: String) -> Result<()> {
    run_limited(move || verify_against_dummy(&password)).await
}

pub async fn verify_async(password: String, stored_hash: String) -> Result<bool> {
    run_limited(move || verify(&password, &stored_hash)).await
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn correct_password_verifies() {
        let stored = hash("correct horse").unwrap();
        assert!(verify("correct horse", &stored));
    }

    #[test]
    fn wrong_password_is_rejected() {
        let stored = hash("correct horse").unwrap();
        assert!(!verify("wrong horse", &stored));
    }

    #[test]
    fn same_password_gets_a_different_salt() {
        assert_ne!(
            hash("correct horse").unwrap(),
            hash("correct horse").unwrap()
        );
    }

    #[test]
    fn garbage_hash_never_verifies() {
        assert!(!verify("anything", "not a hash"));
        assert!(!verify("anything", ""));
    }

    #[test]
    fn oversized_password_is_refused() {
        let stored = hash("correct horse").unwrap();
        assert!(!verify(&"a".repeat(MAX_BYTES + 1), &stored));
        assert_eq!(
            validate(&"a".repeat(MAX_BYTES + 1)),
            Err(PasswordError::TooLong)
        );
    }

    #[test]
    fn short_password_is_refused() {
        assert_eq!(validate("1234567"), Err(PasswordError::TooShort));
        assert!(validate("12345678").is_ok());
        // Counted in characters, not bytes.
        assert!(validate("密碼密碼密碼密碼").is_ok());
    }

    #[test]
    fn dummy_check_does_not_panic() {
        verify_against_dummy("whatever");
    }
}
