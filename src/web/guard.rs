//! Request checks shared by all routes: who is calling, and whether the request may
//! come from the page that sent it.

use std::net::{IpAddr, SocketAddr};

use axum::extract::{ConnectInfo, FromRequestParts, Request, State};
use axum::http::request::Parts;
use axum::http::{HeaderMap, HeaderName, HeaderValue, Method, header};
use axum::middleware::Next;
use axum::response::{IntoResponse, Response};

use super::SharedState;
use super::cookie;
use super::error::ApiError;
use crate::auth::forward_auth::{Forwarded, ForwardedUser};
use crate::auth::session::{self, SessionUser};
use crate::auth::unix_now;
use crate::users::{self, ForwardedAccount};

/// The signed-in user. A handler that takes this argument rejects anonymous requests.
pub struct CurrentUser(pub SessionUser);

impl FromRequestParts<SharedState> for CurrentUser {
    type Rejection = ApiError;

    async fn from_request_parts(parts: &mut Parts, state: &SharedState) -> Result<Self, ApiError> {
        user_from_request(state, peer_ip(parts), &parts.headers)
            .await?
            .map(Self)
            .ok_or(ApiError::Unauthorized)
    }
}

/// A signed-in admin. Anyone else is refused: 401 when anonymous, 403 when not an admin.
pub struct AdminUser(pub SessionUser);

impl FromRequestParts<SharedState> for AdminUser {
    type Rejection = ApiError;

    async fn from_request_parts(parts: &mut Parts, state: &SharedState) -> Result<Self, ApiError> {
        let CurrentUser(user) = CurrentUser::from_request_parts(parts, state).await?;
        if !user.is_admin {
            return Err(ApiError::Forbidden("admin access required"));
        }
        Ok(Self(user))
    }
}

/// An admin who typed their password in the last few minutes. Changes to accounts take
/// this, so a stolen admin session cannot promote, disable or reset anyone without the
/// password. Refused with `ReauthRequired` when the confirmation is old.
pub struct ConfirmedAdmin(pub SessionUser);

impl FromRequestParts<SharedState> for ConfirmedAdmin {
    type Rejection = ApiError;

    async fn from_request_parts(parts: &mut Parts, state: &SharedState) -> Result<Self, ApiError> {
        let AdminUser(user) = AdminUser::from_request_parts(parts, state).await?;
        // The proxy already proved who this is, and there is no password to ask for.
        if user.via_proxy {
            return Ok(Self(user));
        }
        let confirmed = match cookie::read_token(&parts.headers, state.secure_cookies) {
            Some(token) => session::is_confirmed(&state.pool, token, unix_now()).await?,
            None => false,
        };
        if !confirmed {
            return Err(ApiError::ReauthRequired);
        }
        Ok(Self(user))
    }
}

/// The address of the TCP peer: the proxy when there is one, not the end user.
pub fn peer_ip(parts: &Parts) -> Option<IpAddr> {
    let ConnectInfo(peer) = parts.extensions.get::<ConnectInfo<SocketAddr>>()?;
    Some(peer.ip())
}

/// A trusted proxy's word wins over a cookie: when the proxy signs the user out, bkd must
/// not keep them in. `None` when nobody is signed in.
pub async fn user_from_request(
    state: &SharedState,
    peer: Option<IpAddr>,
    headers: &HeaderMap,
) -> Result<Option<SessionUser>, ApiError> {
    let forwarded = match &state.forward_auth {
        Some(forward_auth) => forward_auth.identify(peer, headers),
        None => Forwarded::Absent,
    };
    match forwarded {
        Forwarded::Absent => user_from_cookie(state, headers).await,
        Forwarded::Invalid => Ok(None),
        Forwarded::User(forwarded) => user_from_proxy(state, &forwarded).await,
    }
}

async fn user_from_proxy(
    state: &SharedState,
    forwarded: &ForwardedUser,
) -> Result<Option<SessionUser>, ApiError> {
    let account = ForwardedAccount {
        username: &forwarded.username,
        is_admin: forwarded.is_admin,
        now: unix_now(),
    };
    let adopted = users::adopt_forwarded(&state.pool, &account).await?;
    // The first account may have just been made here, which closes setup.
    if adopted.is_some()
        && state.setup_token().is_some()
        && crate::auth::first_admin::users_exist(&state.pool).await?
    {
        state.clear_setup_token();
    }
    Ok(adopted.map(|(user_id, is_admin)| SessionUser {
        user_id,
        username: forwarded.username.clone(),
        is_admin,
        via_proxy: true,
    }))
}

/// `None` when the request carries no cookie or the session is not valid.
async fn user_from_cookie(
    state: &SharedState,
    headers: &HeaderMap,
) -> Result<Option<SessionUser>, ApiError> {
    let Some(token) = cookie::read_token(headers, state.secure_cookies) else {
        return Ok(None);
    };
    Ok(session::lookup(&state.pool, token, unix_now()).await?)
}

/// Refuses a state-changing request whose `Origin` names another site. Together with
/// `SameSite=Lax` and the JSON-only bodies, this closes cross-site request forgery
/// even for a browser that ignores `SameSite`.
pub async fn same_origin_only(request: Request, next: Next) -> Response {
    if !is_safe_method(request.method()) && !origin_matches_host(request.headers()) {
        return ApiError::Forbidden("cross-site request refused").into_response();
    }
    next.run(request).await
}

fn is_safe_method(method: &Method) -> bool {
    matches!(*method, Method::GET | Method::HEAD | Method::OPTIONS)
}

/// A request without `Origin` (a command-line client) passes: it cannot be a browser
/// acting on a logged-in user's behalf, because browsers send `Origin` on these.
fn origin_matches_host(headers: &HeaderMap) -> bool {
    let Some(origin) = headers.get(header::ORIGIN) else {
        return true;
    };
    let Some(host) = headers.get(header::HOST) else {
        return false;
    };
    let origin = origin.to_str().unwrap_or_default();
    let authority = origin.split_once("://").map(|(_, authority)| authority);
    authority == host.to_str().ok()
}

/// Keeps API responses out of shared caches and stops the browser from guessing types.
pub async fn api_headers(request: Request, next: Next) -> Response {
    let mut response = next.run(request).await;
    let headers = response.headers_mut();
    headers.insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
    headers.insert(
        header::X_CONTENT_TYPE_OPTIONS,
        HeaderValue::from_static("nosniff"),
    );
    response
}

/// Headers every response carries, API and static files alike. HSTS is sent only when
/// the operator says the site is served over HTTPS: on plain HTTP browsers ignore it.
pub async fn security_headers(
    State(state): State<SharedState>,
    request: Request,
    next: Next,
) -> Response {
    let mut response = next.run(request).await;
    let headers = response.headers_mut();
    let fixed = [
        (header::X_FRAME_OPTIONS, "DENY"),
        (header::REFERRER_POLICY, "no-referrer"),
        (
            HeaderName::from_static("cross-origin-opener-policy"),
            "same-origin",
        ),
        (
            HeaderName::from_static("cross-origin-resource-policy"),
            "same-origin",
        ),
        (
            HeaderName::from_static("permissions-policy"),
            "camera=(), microphone=(), geolocation=(), payment=(), usb=()",
        ),
    ];
    // A page route sets its own `Referrer-Policy` and keeps it.
    for (name, value) in fixed {
        headers
            .entry(name)
            .or_insert(HeaderValue::from_static(value));
    }
    if state.secure_cookies {
        headers.insert(
            header::STRICT_TRANSPORT_SECURITY,
            HeaderValue::from_static("max-age=31536000"),
        );
    }
    response
}

#[cfg(test)]
mod tests {
    use super::*;

    fn headers(origin: Option<&str>, host: Option<&str>) -> HeaderMap {
        let mut headers = HeaderMap::new();
        if let Some(origin) = origin {
            headers.insert(header::ORIGIN, HeaderValue::from_str(origin).unwrap());
        }
        if let Some(host) = host {
            headers.insert(header::HOST, HeaderValue::from_str(host).unwrap());
        }
        headers
    }

    #[test]
    fn no_origin_is_allowed() {
        assert!(origin_matches_host(&headers(None, Some("bkd.test"))));
    }

    #[test]
    fn a_matching_origin_is_allowed() {
        assert!(origin_matches_host(&headers(
            Some("https://bkd.test"),
            Some("bkd.test")
        )));
        assert!(origin_matches_host(&headers(
            Some("http://localhost:8080"),
            Some("localhost:8080")
        )));
    }

    #[test]
    fn another_site_is_refused() {
        assert!(!origin_matches_host(&headers(
            Some("https://evil.test"),
            Some("bkd.test")
        )));
        assert!(!origin_matches_host(&headers(
            Some("http://bkd.test:81"),
            Some("bkd.test")
        )));
        assert!(!origin_matches_host(&headers(
            Some("null"),
            Some("bkd.test")
        )));
        assert!(!origin_matches_host(&headers(
            Some("https://bkd.test"),
            None
        )));
    }
}
