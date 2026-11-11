//! Forward authentication: a reverse proxy (Authelia, Authentik, oauth2-proxy) signs the
//! user in and tells bkd who it is in a request header.
//!
//! A header is only a claim. It is believed only when the TCP peer is one of the proxies
//! the operator listed; from anyone else it is ignored, so a client that reaches bkd
//! directly cannot sign in as somebody by typing a header. The proxy must in turn
//! overwrite the header on every request it forwards.

use std::net::IpAddr;

use anyhow::{Context, Result, bail};
use axum::http::{HeaderMap, HeaderName};

use super::username;

/// What the operator configured.
pub struct ForwardAuthConfig {
    /// Addresses or CIDR ranges of the proxies, such as `127.0.0.1` or `172.18.0.0/16`.
    pub trusted_proxies: Vec<String>,
    pub user_header: String,
    pub groups_header: String,
    /// Members of this group are admins. Without it, bkd never promotes or demotes anyone.
    pub admin_group: Option<String>,
}

pub struct ForwardAuth {
    trusted: Vec<Network>,
    user_header: HeaderName,
    groups_header: HeaderName,
    admin_group: Option<String>,
}

/// Who the proxy says the request is from.
#[derive(Debug, PartialEq, Eq)]
pub struct ForwardedUser {
    pub username: String,
    /// `Some` when an admin group is configured: whether the user is in it.
    pub is_admin: Option<bool>,
}

#[derive(Debug, PartialEq, Eq)]
pub enum Forwarded {
    /// Not from a trusted proxy, or no user header: use the session cookie.
    Absent,
    /// A trusted proxy sent a name that cannot be a bkd username. Nobody is signed in.
    Invalid,
    User(ForwardedUser),
}

impl ForwardAuth {
    pub fn new(config: ForwardAuthConfig) -> Result<Self> {
        if config.trusted_proxies.is_empty() {
            bail!("forward auth needs at least one trusted proxy address");
        }
        let trusted = config
            .trusted_proxies
            .iter()
            .map(|text| Network::parse(text))
            .collect::<Result<_>>()?;
        Ok(Self {
            trusted,
            user_header: header_name(&config.user_header)?,
            groups_header: header_name(&config.groups_header)?,
            admin_group: config.admin_group.filter(|group| !group.is_empty()),
        })
    }

    pub fn identify(&self, peer: Option<IpAddr>, headers: &HeaderMap) -> Forwarded {
        let Some(peer) = peer.filter(|peer| self.is_trusted(*peer)) else {
            return Forwarded::Absent;
        };
        let mut values = headers.get_all(&self.user_header).iter();
        let Some(value) = values.next() else {
            return Forwarded::Absent;
        };
        // Two values mean the proxy appended to a header the client sent, so the first
        // one is the client's. A value that is not plain text is no name either. In both
        // cases nobody is signed in, and the cookie must not fill the gap.
        let Ok(raw) = value.to_str() else {
            return Forwarded::Invalid;
        };
        if values.next().is_some() {
            tracing::warn!(%peer, "more than one forwarded user header");
            return Forwarded::Invalid;
        }
        if raw.is_empty() {
            return Forwarded::Absent;
        }
        let Ok(username) = username::normalize(raw) else {
            tracing::warn!(%peer, "forwarded user name is not a valid bkd username");
            return Forwarded::Invalid;
        };
        Forwarded::User(ForwardedUser {
            username,
            is_admin: self
                .admin_group
                .as_deref()
                .map(|group| self.in_group(headers, group)),
        })
    }

    fn is_trusted(&self, peer: IpAddr) -> bool {
        let peer = peer.to_canonical();
        self.trusted.iter().any(|network| network.contains(peer))
    }

    /// Authelia sends `Remote-Groups` as a comma-separated list.
    fn in_group(&self, headers: &HeaderMap, group: &str) -> bool {
        headers
            .get(&self.groups_header)
            .and_then(|value| value.to_str().ok())
            .is_some_and(|groups| groups.split(',').any(|name| name.trim() == group))
    }
}

fn header_name(text: &str) -> Result<HeaderName> {
    HeaderName::from_bytes(text.as_bytes())
        .with_context(|| format!("{text:?} is not a header name"))
}

/// One address or a range, as in `10.0.0.0/8`. Hand-rolled: it is a dozen lines, and the
/// project avoids a dependency for that.
#[derive(Debug, PartialEq, Eq)]
struct Network {
    address: IpAddr,
    prefix: u8,
}

impl Network {
    fn parse(text: &str) -> Result<Self> {
        let (address_text, prefix_text) = match text.split_once('/') {
            Some((address, prefix)) => (address, Some(prefix)),
            None => (text, None),
        };
        let address: IpAddr = address_text
            .trim()
            .parse()
            .with_context(|| format!("{text:?} is not an IP address or CIDR range"))?;
        let address = address.to_canonical();
        let longest = if address.is_ipv4() { 32 } else { 128 };
        let prefix = match prefix_text {
            None => longest,
            Some(prefix) => prefix
                .trim()
                .parse::<u8>()
                .ok()
                .filter(|prefix| *prefix <= longest)
                .with_context(|| format!("{text:?} has a bad prefix length"))?,
        };
        Ok(Self { address, prefix })
    }

    fn contains(&self, ip: IpAddr) -> bool {
        match (self.address, ip) {
            (IpAddr::V4(network), IpAddr::V4(ip)) => matches_prefix(
                u128::from(u32::from(network)),
                u128::from(u32::from(ip)),
                32,
                self.prefix,
            ),
            (IpAddr::V6(network), IpAddr::V6(ip)) => {
                matches_prefix(u128::from(network), u128::from(ip), 128, self.prefix)
            }
            _ => false,
        }
    }
}

/// Compares the first `prefix` bits of two `width`-bit numbers.
fn matches_prefix(network: u128, ip: u128, width: u32, prefix: u8) -> bool {
    let keep = u32::from(prefix);
    if keep == 0 {
        return true;
    }
    let shift = width - keep;
    network >> shift == ip >> shift
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::http::HeaderValue;

    fn forward_auth(admin_group: Option<&str>) -> ForwardAuth {
        ForwardAuth::new(ForwardAuthConfig {
            trusted_proxies: vec![
                "127.0.0.1".into(),
                "172.18.0.0/16".into(),
                "fd00::/8".into(),
            ],
            user_header: "Remote-User".into(),
            groups_header: "Remote-Groups".into(),
            admin_group: admin_group.map(str::to_owned),
        })
        .unwrap()
    }

    fn headers(user: &str, groups: &str) -> HeaderMap {
        let mut headers = HeaderMap::new();
        headers.insert("remote-user", HeaderValue::from_str(user).unwrap());
        headers.insert("remote-groups", HeaderValue::from_str(groups).unwrap());
        headers
    }

    fn ip(text: &str) -> Option<IpAddr> {
        Some(text.parse().unwrap())
    }

    #[test]
    fn a_trusted_proxy_names_the_user() {
        let found = forward_auth(None).identify(ip("172.18.0.5"), &headers("Henry", ""));
        let expected = ForwardedUser {
            username: "henry".into(),
            is_admin: None,
        };
        assert_eq!(found, Forwarded::User(expected));
    }

    #[test]
    fn anyone_else_is_ignored() {
        let auth = forward_auth(None);
        for peer in ["172.19.0.1", "8.8.8.8", "::1", "fe80::1"] {
            assert_eq!(
                auth.identify(ip(peer), &headers("henry", "")),
                Forwarded::Absent,
                "{peer}"
            );
        }
        assert_eq!(
            auth.identify(None, &headers("henry", "")),
            Forwarded::Absent
        );
    }

    #[test]
    fn an_ipv4_peer_mapped_into_ipv6_still_matches() {
        let found = forward_auth(None).identify(ip("::ffff:127.0.0.1"), &headers("henry", ""));
        assert!(matches!(found, Forwarded::User(_)));
    }

    #[test]
    fn ipv6_ranges_match() {
        let found = forward_auth(None).identify(ip("fd12::1"), &headers("henry", ""));
        assert!(matches!(found, Forwarded::User(_)));
    }

    #[test]
    fn a_missing_or_empty_header_falls_back_to_the_cookie() {
        let auth = forward_auth(None);
        assert_eq!(
            auth.identify(ip("127.0.0.1"), &HeaderMap::new()),
            Forwarded::Absent
        );
        assert_eq!(
            auth.identify(ip("127.0.0.1"), &headers("", "")),
            Forwarded::Absent
        );
    }

    #[test]
    fn a_repeated_or_non_text_header_signs_nobody_in() {
        let auth = forward_auth(None);
        let mut twice = headers("mei", "");
        twice.append("remote-user", HeaderValue::from_static("root"));
        assert_eq!(auth.identify(ip("127.0.0.1"), &twice), Forwarded::Invalid);

        let mut accented = HeaderMap::new();
        accented.insert(
            "remote-user",
            HeaderValue::from_bytes("jos\u{e9}".as_bytes()).unwrap(),
        );
        assert_eq!(
            auth.identify(ip("127.0.0.1"), &accented),
            Forwarded::Invalid
        );
    }

    #[test]
    fn a_name_bkd_cannot_hold_signs_nobody_in() {
        let auth = forward_auth(None);
        for name in ["ab", "has space", "-lead", "a@b.test"] {
            assert_eq!(
                auth.identify(ip("127.0.0.1"), &headers(name, "")),
                Forwarded::Invalid,
                "{name}"
            );
        }
    }

    #[test]
    fn the_admin_group_decides_the_role() {
        let auth = forward_auth(Some("admins"));
        let admin = auth.identify(ip("127.0.0.1"), &headers("henry", "dev, admins"));
        let member = auth.identify(ip("127.0.0.1"), &headers("henry", "dev,administrators"));
        let admin_flag = |found| match found {
            Forwarded::User(user) => user.is_admin,
            other => panic!("{other:?}"),
        };
        assert_eq!(admin_flag(admin), Some(true));
        assert_eq!(admin_flag(member), Some(false));
    }

    #[test]
    fn a_bad_setting_is_refused_at_startup() {
        for proxies in [
            vec![],
            vec!["not-an-ip".to_owned()],
            vec!["10.0.0.0/33".to_owned()],
        ] {
            let config = ForwardAuthConfig {
                trusted_proxies: proxies,
                user_header: "Remote-User".into(),
                groups_header: "Remote-Groups".into(),
                admin_group: None,
            };
            assert!(ForwardAuth::new(config).is_err());
        }
    }

    #[test]
    fn a_zero_length_prefix_matches_everything() {
        let network = Network::parse("0.0.0.0/0").unwrap();
        assert!(network.contains("203.0.113.9".parse().unwrap()));
    }
}
