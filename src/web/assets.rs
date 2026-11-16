//! The front end: the static files compiled into the binary, so the program stays a
//! single file with nothing to deploy next to it.

use axum::http::{HeaderValue, header};
use axum::response::{IntoResponse, Response};

const APP_CSS: &str = include_str!("../../frontend/app.css");
const APP_JS: &str = include_str!("../../frontend/app.js");
const FAVICON_SVG: &str = include_str!("../../frontend/favicon.svg");
const FAVICON_ICO: &[u8] = include_bytes!("../../frontend/favicon.ico");
const APPLE_TOUCH_ICON: &[u8] = include_bytes!("../../frontend/apple-touch-icon.png");

// Only files from this origin may run or load. The page has no inline script or style,
// so a stray `<script>` injected through bookmark text would still not execute.
const CONTENT_SECURITY_POLICY: &str = "default-src 'none'; script-src 'self'; style-src 'self'; \
     connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'";

pub async fn stylesheet() -> Response {
    asset("text/css; charset=utf-8", APP_CSS.as_bytes())
}

pub async fn script() -> Response {
    asset("text/javascript; charset=utf-8", APP_JS.as_bytes())
}

pub async fn favicon_svg() -> Response {
    asset("image/svg+xml", FAVICON_SVG.as_bytes())
}

// Browsers ask for this path on their own, even when the page links the SVG.
pub async fn favicon_ico() -> Response {
    asset("image/x-icon", FAVICON_ICO)
}

pub async fn apple_touch_icon() -> Response {
    asset("image/png", APPLE_TOUCH_ICON)
}

fn asset(content_type: &'static str, body: &'static [u8]) -> Response {
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
