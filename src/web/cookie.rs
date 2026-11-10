//! The session cookie. The value is a hex token, so it needs no escaping.

use axum::http::{HeaderMap, HeaderValue, header};

use crate::auth::session::SESSION_LIFETIME_SECONDS;

const NAME: &str = "bkd_session";
// The `__Host-` prefix makes a browser refuse a cookie set by a sibling subdomain or
// with a `Domain` attribute, which would otherwise let another site's owner plant a
// session on this one. Browsers require `Secure` for it, so it applies only then.
const SECURE_NAME: &str = "__Host-bkd_session";

fn name(secure: bool) -> &'static str {
    if secure { SECURE_NAME } else { NAME }
}

/// Finds the session token among the request's cookies.
pub fn read_token(headers: &HeaderMap, secure: bool) -> Option<&str> {
    let wanted = name(secure);
    headers
        .get_all(header::COOKIE)
        .iter()
        .filter_map(|value| value.to_str().ok())
        .flat_map(|value| value.split(';'))
        .filter_map(|pair| pair.trim().split_once('='))
        .find(|(name, _)| *name == wanted)
        .map(|(_, value)| value)
}

/// `HttpOnly` hides the token from scripts. `SameSite=Strict` keeps the browser from
/// sending it on any request that starts on another site.
pub fn set(token: &str, secure: bool) -> HeaderValue {
    build(token, SESSION_LIFETIME_SECONDS, secure)
}

pub fn clear(secure: bool) -> HeaderValue {
    build("", 0, secure)
}

fn build(value: &str, max_age: i64, secure: bool) -> HeaderValue {
    let secure_flag = secure;
    let secure = if secure { "; Secure" } else { "" };
    let name = name(secure_flag);
    let cookie =
        format!("{name}={value}; Path=/; HttpOnly; SameSite=Strict; Max-Age={max_age}{secure}");
    HeaderValue::from_str(&cookie).expect("cookie contains only visible ASCII")
}

#[cfg(test)]
mod tests {
    use super::*;

    fn headers(cookie: &str) -> HeaderMap {
        let mut headers = HeaderMap::new();
        headers.insert(header::COOKIE, HeaderValue::from_str(cookie).unwrap());
        headers
    }

    #[test]
    fn the_token_is_found_among_other_cookies() {
        assert_eq!(
            read_token(&headers("a=1; bkd_session=abc; b=2"), false),
            Some("abc")
        );
        assert_eq!(read_token(&headers("bkd_session=abc"), false), Some("abc"));
    }

    #[test]
    fn a_missing_or_similar_name_gives_none() {
        assert_eq!(read_token(&HeaderMap::new(), false), None);
        assert_eq!(
            read_token(&headers("x_bkd_session=abc; bkd_session2=def"), false),
            None
        );
    }

    #[test]
    fn the_cookie_is_http_only_and_strict() {
        let value = set("tok", false);
        let text = value.to_str().unwrap();
        assert!(text.starts_with("bkd_session=tok;"));
        assert!(text.contains("HttpOnly") && text.contains("SameSite=Strict"));
        assert!(!text.contains("Secure"));
        assert!(set("tok", true).to_str().unwrap().ends_with("; Secure"));
    }

    #[test]
    fn a_secure_cookie_uses_the_host_prefix_and_ignores_the_plain_name() {
        let text = set("tok", true).to_str().unwrap().to_owned();
        assert!(text.starts_with("__Host-bkd_session=tok;"));
        assert!(!text.contains("Domain"));
        let both = headers("bkd_session=planted; __Host-bkd_session=real");
        assert_eq!(read_token(&both, true), Some("real"));
        assert_eq!(read_token(&headers("bkd_session=planted"), true), None);
    }

    #[test]
    fn clearing_expires_the_cookie_at_once() {
        let value = clear(false);
        let text = value.to_str().unwrap();
        assert!(text.starts_with("bkd_session=;") && text.contains("Max-Age=0"));
    }
}
