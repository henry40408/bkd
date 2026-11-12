//! The front end: three static files compiled into the binary, so the program stays a
//! single file with nothing to deploy next to it.

use axum::http::{HeaderValue, header};
use axum::response::{IntoResponse, Response};

const APP_CSS: &str = include_str!("../../frontend/app.css");
const APP_JS: &str = include_str!("../../frontend/app.js");

// Only files from this origin may run or load. The page has no inline script or style,
// so a stray `<script>` injected through bookmark text would still not execute.
const CONTENT_SECURITY_POLICY: &str = "default-src 'none'; script-src 'self'; style-src 'self'; \
     connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'";

pub async fn stylesheet() -> Response {
    asset("text/css; charset=utf-8", APP_CSS)
}

pub async fn script() -> Response {
    asset("text/javascript; charset=utf-8", APP_JS)
}

fn asset(content_type: &'static str, body: &'static str) -> Response {
    (
        [
            (header::CONTENT_TYPE, HeaderValue::from_static(content_type)),
            // The files change with each release and carry no version in their names,
            // so the browser must ask again before using its copy.
            (header::CACHE_CONTROL, HeaderValue::from_static("no-cache")),
            (
                header::X_CONTENT_TYPE_OPTIONS,
                HeaderValue::from_static("nosniff"),
            ),
            (
                header::CONTENT_SECURITY_POLICY,
                HeaderValue::from_static(CONTENT_SECURITY_POLICY),
            ),
            (
                header::REFERRER_POLICY,
                HeaderValue::from_static("no-referrer"),
            ),
        ],
        body,
    )
        .into_response()
}
