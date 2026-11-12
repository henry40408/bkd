//! What every page route shares: errors that become pages or redirects, the extractor
//! that wraps the API's, the sidebar, and the checks for form posts.

use std::time::Duration;

use axum::extract::{FromRequestParts, Request};
use axum::http::request::Parts;
use axum::http::{HeaderMap, HeaderValue, StatusCode, header};
use axum::middleware::Next;
use axum::response::{Html, IntoResponse, Redirect, Response};
use maud::{Markup, html};

use super::layout;
use super::view::with_query;
use crate::auth::session::SessionUser;
use crate::bookmarks::{self, LinkCount, TagCount};
use crate::web::SharedState;
use crate::web::cookie;
use crate::web::error::ApiError;

// Forms post to this origin only. A page has no inline script or style, as the API's
// front end never had.
const PAGE_CONTENT_SECURITY_POLICY: &str = "default-src 'none'; script-src 'self'; \
     style-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; \
     form-action 'self'; frame-ancestors 'none'";

/// Why a page could not be shown. Each case is a redirect or an error page.
#[derive(Debug)]
pub enum PageError {
    /// Nobody is signed in.
    LoginRequired,
    /// A page was asked for without a session; `next` is where to go after signing in.
    /// `session_ended` is true only when a session cookie came with the request.
    SignInAgain {
        next: Option<String>,
        session_ended: bool,
    },
    /// The password must be typed again before this admin change.
    ReauthRequired,
    Failed(StatusCode, String),
    TooManyRequests(Duration),
    Internal(anyhow::Error),
}

impl PageError {
    pub fn not_found() -> Self {
        Self::Failed(
            StatusCode::NOT_FOUND,
            "That page does not exist.".to_owned(),
        )
    }

    pub fn bad_request(message: &str) -> Self {
        Self::Failed(StatusCode::BAD_REQUEST, message.to_owned())
    }
}

impl From<ApiError> for PageError {
    fn from(error: ApiError) -> Self {
        match error {
            ApiError::Unauthorized => Self::LoginRequired,
            ApiError::ReauthRequired => Self::ReauthRequired,
            ApiError::TooManyRequests(wait) => Self::TooManyRequests(wait),
            ApiError::Internal(error) => Self::Internal(error),
            other => {
                let (status, message) = other.status_and_message();
                Self::Failed(status, message.to_owned())
            }
        }
    }
}

impl From<anyhow::Error> for PageError {
    fn from(error: anyhow::Error) -> Self {
        Self::Internal(error)
    }
}

impl From<sqlx::Error> for PageError {
    fn from(error: sqlx::Error) -> Self {
        Self::Internal(error.into())
    }
}

/// The answer for an address no page has. The JSON API keeps the plain 404 it had before.
pub async fn not_found_page(uri: axum::http::Uri) -> Response {
    if uri.path() == "/api" || uri.path().starts_with("/api/") {
        return StatusCode::NOT_FOUND.into_response();
    }
    PageError::not_found().into_response()
}

fn error_page(status: StatusCode, message: &str) -> Response {
    let content = html! {
        div.card {
            h1 { (status.as_u16()) }
            p.err role="alert" { (message) }
            p { a href="/" { "Back to bkd" } }
        }
    };
    let title = format!("{} · bkd", status.as_u16());
    (status, Html(layout::bare(&title, content).into_string())).into_response()
}

impl IntoResponse for PageError {
    fn into_response(self) -> Response {
        match self {
            Self::LoginRequired => Redirect::to("/login").into_response(),
            Self::SignInAgain {
                next,
                session_ended,
            } => Redirect::to(&sign_in_again_href(next.as_deref(), session_ended)).into_response(),
            Self::ReauthRequired => Redirect::to("/confirm").into_response(),
            Self::Failed(status, message) => error_page(status, &message),
            Self::TooManyRequests(wait) => {
                let seconds = wait.as_secs() + u64::from(wait.subsec_nanos() > 0);
                let mut response = error_page(
                    StatusCode::TOO_MANY_REQUESTS,
                    "Too many attempts. Try again later.",
                );
                response
                    .headers_mut()
                    .insert(header::RETRY_AFTER, HeaderValue::from(seconds));
                response
            }
            Self::Internal(error) => {
                tracing::error!("internal error: {error:#}");
                error_page(StatusCode::INTERNAL_SERVER_ERROR, "Something went wrong.")
            }
        }
    }
}

/// Wraps one of the API's extractors (`CurrentUser`, `AdminUser`, `ConfirmedAdmin`) so
/// that its refusal becomes a redirect or an error page, not JSON.
pub struct Page<T>(pub T);

impl<T> FromRequestParts<SharedState> for Page<T>
where
    T: FromRequestParts<SharedState, Rejection = ApiError>,
{
    type Rejection = PageError;

    async fn from_request_parts(parts: &mut Parts, state: &SharedState) -> Result<Self, PageError> {
        match T::from_request_parts(parts, state).await {
            Ok(value) => Ok(Self(value)),
            // A first visit sends no cookie; only a cookie that no longer works means a
            // session ended.
            Err(ApiError::Unauthorized) => Err(PageError::SignInAgain {
                next: return_path(parts),
                session_ended: cookie::read_token(&parts.headers, state.secure_cookies).is_some(),
            }),
            Err(error) => Err(PageError::from(error)),
        }
    }
}

/// Where to come back to after signing in. Only a page that is read can be asked for
/// again; a refused form post has no address to return to.
fn return_path(parts: &Parts) -> Option<String> {
    if parts.method != axum::http::Method::GET {
        return None;
    }
    let path = parts.uri.path_and_query()?.as_str();
    same_site_path(path).map(str::to_owned)
}

fn sign_in_again_href(next: Option<&str>, session_ended: bool) -> String {
    let mut pairs = Vec::new();
    if let Some(next) = next {
        pairs.push(("next", next));
    }
    if session_ended {
        pairs.push(("notice", "session-ended"));
    }
    with_query("/login", &pairs)
}

/// `value` when it is a path on this site, so that a link or redirect built from it can
/// not lead to another one. A scheme, a `//` host, a backslash (which browsers read as
/// `/`) and control characters are all refused.
pub fn same_site_path(value: &str) -> Option<&str> {
    let safe = value.starts_with('/')
        && !value.starts_with("//")
        && !value.contains('\\')
        && !value.chars().any(char::is_control);
    safe.then_some(value)
}

/// `value` as a same-site path, else the list.
pub fn path_or_home(value: Option<&str>) -> String {
    value.and_then(same_site_path).unwrap_or("/").to_owned()
}

/// A page that knows how to draw itself.
pub trait View {
    fn markup(&self) -> Markup;
}

pub fn render<T: View>(page: &T) -> Result<Html<String>, PageError> {
    Ok(Html(page.markup().into_string()))
}

/// A page answered with a status, such as 400 for a form that was refused.
pub fn render_with_status<T: View>(status: StatusCode, page: &T) -> Result<Response, PageError> {
    Ok((status, render(page)?).into_response())
}

/// For a form that was refused: the status and message to show on the form again. Faults
/// the form cannot fix (sign-in, server errors) go to the error page instead.
pub fn form_refusal(error: ApiError) -> Result<(StatusCode, String), PageError> {
    match error {
        ApiError::Unauthorized
        | ApiError::ReauthRequired
        | ApiError::TooManyRequests(_)
        | ApiError::Internal(_) => Err(PageError::from(error)),
        other => {
            let (status, message) = other.status_and_message();
            Ok((status, message.to_owned()))
        }
    }
}

/// A redirect to `to` that carries the session cookie the API handler set in `api_response`.
pub fn redirect_keeping_cookie(api_response: &Response, to: &str) -> Response {
    let mut response = Redirect::to(to).into_response();
    for cookie in api_response.headers().get_all(header::SET_COOKIE) {
        response
            .headers_mut()
            .append(header::SET_COOKIE, cookie.clone());
    }
    response
}

/// A redirect that carries a fixed notice key, which the target page turns into text.
pub fn redirect_with_notice(path: &str, notice: Notice) -> Response {
    // `path` may carry a query already, such as the list the person came from.
    Redirect::to(&with_query(path, &[("notice", notice.key())])).into_response()
}

/// What a page may announce after a change. The text is fixed here, never taken from
/// the address, so a crafted link cannot put words on the page.
#[derive(Clone, Copy)]
pub enum Notice {
    SessionEnded,
    OthersEnded,
    UserChanged,
    Confirmed,
    BookmarkSaved,
    BookmarkDeleted,
}

impl Notice {
    fn key(self) -> &'static str {
        match self {
            Self::SessionEnded => "session-ended",
            Self::OthersEnded => "others-ended",
            Self::UserChanged => "user-changed",
            Self::Confirmed => "confirmed",
            Self::BookmarkSaved => "bookmark-saved",
            Self::BookmarkDeleted => "bookmark-deleted",
        }
    }

    pub fn from_key(key: Option<&str>) -> Option<&'static str> {
        Some(match key? {
            "password-changed" => "Password changed. Your other browsers were signed out.",
            "session-ended" => "That browser was signed out.",
            "others-ended" => "All other browsers were signed out.",
            "user-changed" => "Saved.",
            "confirmed" => "Password confirmed. You can change accounts for the next few minutes.",
            "bookmark-saved" => "Bookmark saved.",
            "bookmark-deleted" => "Bookmark deleted.",
            "bulk-done" => "Done.",
            "nothing-selected" => "Tick at least one bookmark first.",
            _ => return None,
        })
    }
}

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Section {
    Bookmarks,
    Transfer,
    Account,
    Users,
}

pub struct FilterLink {
    pub label: String,
    pub count: Option<i64>,
    pub href: String,
    pub current: bool,
}

/// The page frame around every signed-in screen: the sidebar and the account links.
pub struct Shell {
    pub username: String,
    pub is_admin: bool,
    /// Signed in by the reverse proxy: no password and no Sign out here.
    pub forwarded: bool,
    pub section: Section,
    pub all_current: bool,
    pub statuses: Vec<FilterLink>,
    pub tags: Vec<FilterLink>,
    pub notice: Option<&'static str>,
}

// Worst first: what needs a look comes before what is fine.
const LINK_FILTERS: [(&str, &str); 5] = [
    ("broken", "Broken"),
    ("redirected", "Moved"),
    ("unknown", "Not confirmed"),
    ("ok", "OK"),
    ("unchecked", "Not checked"),
];

pub struct Active<'a> {
    pub section: Section,
    pub tag: &'a str,
    pub status: &'a str,
    pub notice: Option<&'a str>,
}

impl Shell {
    pub async fn load(
        state: &SharedState,
        user: &SessionUser,
        active: Active<'_>,
    ) -> Result<Self, PageError> {
        let tags = bookmarks::tag_counts(&state.pool, user.user_id).await?;
        let links = bookmarks::link_counts(&state.pool, user.user_id).await?;
        let on_list = active.section == Section::Bookmarks;
        Ok(Self {
            username: user.username.clone(),
            is_admin: user.is_admin,
            forwarded: user.via_proxy,
            section: active.section,
            all_current: on_list && active.tag.is_empty() && active.status.is_empty(),
            statuses: status_links(&links, on_list.then_some(active.status)),
            tags: tag_links(&tags, on_list.then_some((active.tag, active.status))),
            notice: Notice::from_key(active.notice),
        })
    }
}

fn status_links(counts: &[LinkCount], current: Option<&str>) -> Vec<FilterLink> {
    LINK_FILTERS
        .iter()
        .filter_map(|(status, label)| {
            let found = counts.iter().find(|count| count.status == *status)?;
            Some(FilterLink {
                label: (*label).to_owned(),
                count: Some(found.count),
                href: with_query("/", &[("status", status)]),
                current: current == Some(*status),
            })
        })
        .collect()
}

fn tag_links(counts: &[TagCount], current: Option<(&str, &str)>) -> Vec<FilterLink> {
    counts
        .iter()
        .map(|entry| FilterLink {
            label: entry.tag.clone(),
            count: Some(entry.count),
            href: with_query("/", &[("tag", &entry.tag)]),
            // A status filter replaces the tag filter in the sidebar, as it always did.
            current: current.is_some_and(|(tag, status)| status.is_empty() && tag == entry.tag),
        })
        .collect()
}

/// Refuses a form post unless its `Origin` names this site. The API lets a request
/// without `Origin` through, because only a script can send one; a browser always sends
/// `Origin` with a form post, so here a missing one is a refusal.
pub async fn form_origin_required(request: Request, next: Next) -> Response {
    let safe = matches!(
        *request.method(),
        axum::http::Method::GET | axum::http::Method::HEAD
    );
    if !safe && !origin_names_this_host(request.headers()) {
        return error_page(
            StatusCode::FORBIDDEN,
            "This request came from another site.",
        );
    }
    next.run(request).await
}

fn origin_names_this_host(headers: &HeaderMap) -> bool {
    let origin = headers.get(header::ORIGIN).and_then(|v| v.to_str().ok());
    let host = headers.get(header::HOST).and_then(|v| v.to_str().ok());
    match (origin, host) {
        (Some(origin), Some(host)) => origin
            .split_once("://")
            .is_some_and(|(_, authority)| authority == host),
        _ => false,
    }
}

/// Headers for every page. `same-origin` (not `no-referrer`) because a browser sends
/// `Origin: null` with a form post under `no-referrer`, which the check above refuses.
pub async fn page_headers(request: Request, next: Next) -> Response {
    let mut response = next.run(request).await;
    let headers = response.headers_mut();
    headers.insert(
        header::CONTENT_SECURITY_POLICY,
        HeaderValue::from_static(PAGE_CONTENT_SECURITY_POLICY),
    );
    headers.insert(
        header::REFERRER_POLICY,
        HeaderValue::from_static("same-origin"),
    );
    headers.insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
    headers.insert(
        header::X_CONTENT_TYPE_OPTIONS,
        HeaderValue::from_static("nosniff"),
    );
    response
}

#[cfg(test)]
mod tests {
    use super::*;

    fn headers(origin: Option<&str>, host: &str) -> HeaderMap {
        let mut headers = HeaderMap::new();
        headers.insert(header::HOST, HeaderValue::from_str(host).unwrap());
        if let Some(origin) = origin {
            headers.insert(header::ORIGIN, HeaderValue::from_str(origin).unwrap());
        }
        headers
    }

    #[test]
    fn a_form_post_needs_a_matching_origin() {
        assert!(origin_names_this_host(&headers(
            Some("http://bkd.test"),
            "bkd.test"
        )));
        assert!(!origin_names_this_host(&headers(None, "bkd.test")));
        assert!(!origin_names_this_host(&headers(Some("null"), "bkd.test")));
        assert!(!origin_names_this_host(&headers(
            Some("http://evil.test"),
            "bkd.test"
        )));
    }

    #[test]
    fn only_a_path_on_this_site_is_a_same_site_path() {
        for good in ["/", "/account", "/?q=a&page=2", "/a/b?x=%2F%2F"] {
            assert_eq!(same_site_path(good), Some(good));
        }
        for bad in [
            "",
            "evil.test",
            "https://evil.test",
            "//evil.test",
            "/\\evil.test",
            "\\\\evil.test",
            "javascript:alert(1)",
            "/a\r\nSet-Cookie: x=1",
            "/a\tb",
        ] {
            assert_eq!(same_site_path(bad), None, "{bad:?}");
        }
        assert_eq!(path_or_home(None), "/");
        assert_eq!(path_or_home(Some("//evil.test")), "/");
    }

    #[test]
    fn notice_text_comes_only_from_known_keys() {
        assert!(Notice::from_key(Some("confirmed")).is_some());
        assert!(Notice::from_key(Some("<script>")).is_none());
        assert!(Notice::from_key(None).is_none());
    }
}
