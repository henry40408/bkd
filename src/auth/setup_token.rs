//! The one-time token that lets the person who can read the server log create the
//! first admin. It exists only in memory: it is printed at startup and never stored.
//!
//! The token is 50 random bits written as two groups of five Crockford base32
//! characters (`XXXXX-XXXXX`). Comparison ignores case, dashes and spaces, and runs
//! on HMACs so the check takes the same time wherever the guess first differs.

use hmac::{Hmac, KeyInit, Mac};
use sha2::Sha256;

use super::random_bytes;

type HmacSha256 = Hmac<Sha256>;

const ALPHABET: &[u8; 32] = b"0123456789ABCDEFGHJKMNPQRSTVWXYZ";
const GROUP_LENGTH: usize = 5;
const TOKEN_LENGTH: usize = 2 * GROUP_LENGTH;

pub struct SetupToken {
    mac_key: [u8; 32],
    expected_tag: Vec<u8>,
}

impl SetupToken {
    /// Returns the token to keep in memory and the text to show the operator.
    pub fn generate() -> (Self, String) {
        let symbols = encode(u64::from_be_bytes(random_bytes::<8>()));
        let display = format!("{}-{}", &symbols[..GROUP_LENGTH], &symbols[GROUP_LENGTH..]);

        let mac_key = random_bytes::<32>();
        let expected_tag = tag(&mac_key, &symbols);
        (
            Self {
                mac_key,
                expected_tag,
            },
            display,
        )
    }

    pub fn matches(&self, guess: &str) -> bool {
        let mut mac =
            HmacSha256::new_from_slice(&self.mac_key).expect("HMAC accepts any key length");
        mac.update(normalize(guess).as_bytes());
        mac.verify_slice(&self.expected_tag).is_ok()
    }
}

/// Turns the top 50 bits of `random` into ten Crockford symbols.
fn encode(random: u64) -> String {
    let bits = random >> (64 - 5 * TOKEN_LENGTH);
    (0..TOKEN_LENGTH)
        .rev()
        .map(|index| ALPHABET[((bits >> (5 * index)) & 0b11111) as usize] as char)
        .collect()
}

fn tag(key: &[u8], normalized: &str) -> Vec<u8> {
    let mut mac = HmacSha256::new_from_slice(key).expect("HMAC accepts any key length");
    mac.update(normalized.as_bytes());
    mac.finalize().into_bytes().to_vec()
}

/// Uppercases, drops dashes and whitespace, and applies Crockford's look-alike rules
/// (`O` is `0`; `I` and `L` are `1`) so a misread character still matches.
fn normalize(guess: &str) -> String {
    guess
        .chars()
        .filter(|character| *character != '-' && !character.is_whitespace())
        .flat_map(char::to_uppercase)
        .map(|character| match character {
            'O' => '0',
            'I' | 'L' => '1',
            other => other,
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn display_form_is_two_groups_of_crockford_characters() {
        let (_, display) = SetupToken::generate();
        let (first, second) = display.split_once('-').unwrap();
        assert_eq!((first.len(), second.len()), (GROUP_LENGTH, GROUP_LENGTH));
        assert!(
            display
                .bytes()
                .all(|byte| byte == b'-' || ALPHABET.contains(&byte))
        );
    }

    #[test]
    fn encoding_uses_the_top_fifty_bits() {
        assert_eq!(encode(0), "0000000000");
        assert_eq!(encode(u64::MAX), "ZZZZZZZZZZ");
        // Only the top bit set: the first symbol is 0b10000 = 16 = 'G'.
        assert_eq!(encode(1 << 63), "G000000000");
        // The lowest kept bit (bit 14) is the last bit of the last symbol.
        assert_eq!(encode(1 << 14), "0000000001");
        // The 14 discarded low bits never reach the output.
        assert_eq!(encode((1 << 14) - 1), "0000000000");
    }

    #[test]
    fn the_displayed_token_matches() {
        let (token, display) = SetupToken::generate();
        assert!(token.matches(&display));
    }

    #[test]
    fn case_dashes_and_spaces_are_ignored() {
        let (token, display) = SetupToken::generate();
        let sloppy = format!("  {} ", display.to_lowercase().replace('-', " "));
        assert!(token.matches(&sloppy));
        assert!(token.matches(&display.replace('-', "")));
    }

    #[test]
    fn look_alike_characters_are_forgiven() {
        let (token, display) = SetupToken::generate();
        let misread = display.replace('0', "o").replace('1', "l");
        assert!(token.matches(&misread));
    }

    #[test]
    fn a_wrong_guess_is_rejected() {
        let (token, display) = SetupToken::generate();
        let mut wrong = display.into_bytes();
        let last = wrong.len() - 1;
        wrong[last] = if wrong[last] == b'A' { b'B' } else { b'A' };
        assert!(!token.matches(&String::from_utf8(wrong).unwrap()));
        assert!(!token.matches(""));
        assert!(!token.matches("-----"));
    }

    #[test]
    fn a_token_does_not_match_another_instance() {
        let (_, other_display) = SetupToken::generate();
        let (token, _) = SetupToken::generate();
        assert!(!token.matches(&other_display));
    }

    #[test]
    fn extra_characters_are_rejected() {
        let (token, display) = SetupToken::generate();
        assert!(!token.matches(&format!("{display}0")));
    }
}
