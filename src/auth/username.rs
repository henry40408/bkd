//! Username rules. They mirror the `CHECK` on `users.username`, so the application
//! reports a readable error before the database would reject the row.

use std::fmt;

const MIN_LENGTH: usize = 3;
const MAX_LENGTH: usize = 32;

#[derive(Debug, PartialEq, Eq)]
pub enum UsernameError {
    Length,
    Character,
    Edge,
}

impl fmt::Display for UsernameError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Length => write!(
                f,
                "username must be {MIN_LENGTH} to {MAX_LENGTH} characters"
            ),
            Self::Character => write!(f, "username may only contain a-z, 0-9, '.', '_' and '-'"),
            Self::Edge => write!(f, "username must start and end with a letter or digit"),
        }
    }
}

impl std::error::Error for UsernameError {}

/// Lowercases the input and checks it against the rules. Surrounding whitespace is
/// not trimmed: it is not an allowed character, so it is an error.
pub fn normalize(raw: &str) -> Result<String, UsernameError> {
    let username = raw.to_ascii_lowercase();
    if !(MIN_LENGTH..=MAX_LENGTH).contains(&username.len()) {
        return Err(UsernameError::Length);
    }
    if !username.bytes().all(is_allowed) {
        return Err(UsernameError::Character);
    }
    if !username.starts_with(is_alphanumeric) || !username.ends_with(is_alphanumeric) {
        return Err(UsernameError::Edge);
    }
    Ok(username)
}

fn is_allowed(byte: u8) -> bool {
    byte.is_ascii_lowercase() || byte.is_ascii_digit() || matches!(byte, b'.' | b'_' | b'-')
}

fn is_alphanumeric(character: char) -> bool {
    character.is_ascii_lowercase() || character.is_ascii_digit()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn uppercase_is_lowercased() {
        assert_eq!(normalize("Henry.W").unwrap(), "henry.w");
    }

    #[test]
    fn length_is_limited() {
        assert_eq!(normalize("ab"), Err(UsernameError::Length));
        assert!(normalize(&"a".repeat(32)).is_ok());
        assert_eq!(normalize(&"a".repeat(33)), Err(UsernameError::Length));
    }

    #[test]
    fn only_ascii_symbols_are_allowed() {
        for bad in ["a b c", "ab/c", "mé1", "日本語", " abc", "abc\n"] {
            assert!(normalize(bad).is_err(), "{bad:?}");
        }
        assert_eq!(normalize("a b c"), Err(UsernameError::Character));
    }

    #[test]
    fn edges_must_be_alphanumeric() {
        for bad in ["-abc", "abc.", "_abc", "abc_"] {
            assert_eq!(normalize(bad), Err(UsernameError::Edge), "{bad:?}");
        }
    }
}
