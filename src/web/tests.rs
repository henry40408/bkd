//! End-to-end tests: real routes, real SQLite, no network. Requests go straight into
//! the router with a made-up client address.

use std::net::SocketAddr;

use axum::Router;
use axum::body::Body;
use axum::extract::ConnectInfo;
use axum::http::{HeaderMap, Method, Request, StatusCode, header};
use serde_json::{Value, json};
use tower::ServiceExt;

use super::{AppState, Settings, SharedState, router};
use crate::auth::forward_auth::{ForwardAuth, ForwardAuthConfig};
use crate::auth::password;
use crate::auth::setup_token::SetupToken;
use crate::db::test_support::tempdir::TempDir;
use crate::db::test_support::test_pool;
use crate::linkcheck::Checker;

const PASSWORD: &str = "correct horse battery";

struct TestApp {
    state: SharedState,
    router: Router,
    setup_display: Option<String>,
    _dir: TempDir,
}

struct Reply {
    status: StatusCode,
    headers: HeaderMap,
    body: Value,
    text: String,
}

/// A request to send. Defaults to a JSON body from 192.0.2.1 with no cookie.
struct Send {
    method: Method,
    uri: String,
    body: Option<Value>,
    cookie: Option<String>,
    ip: &'static str,
    origin: Option<&'static str>,
    raw: Option<(&'static str, Vec<u8>)>,
    headers: Vec<(&'static str, &'static str)>,
}

impl Send {
    fn new(method: Method, uri: &str) -> Self {
        Self {
            method,
            uri: uri.to_owned(),
            body: None,
            cookie: None,
            ip: "192.0.2.1",
            origin: None,
            raw: None,
            headers: Vec::new(),
        }
    }
    fn get(uri: &str) -> Self {
        Self::new(Method::GET, uri)
    }
    fn post(uri: &str, body: Value) -> Self {
        Self::new(Method::POST, uri).body(body)
    }
    fn put(uri: &str, body: Value) -> Self {
        Self::new(Method::PUT, uri).body(body)
    }
    fn delete(uri: &str) -> Self {
        Self::new(Method::DELETE, uri)
    }
    fn body(mut self, body: Value) -> Self {
        self.body = Some(body);
        self
    }
    /// A body that is not JSON, sent with the given content type.
    fn raw(mut self, content_type: &'static str, bytes: impl Into<Vec<u8>>) -> Self {
        self.raw = Some((content_type, bytes.into()));
        self
    }
    fn cookie(mut self, token: &str) -> Self {
        self.cookie = Some(format!("bkd_session={token}"));
        self
    }
    fn ip(mut self, ip: &'static str) -> Self {
        self.ip = ip;
        self
    }
    fn header(mut self, name: &'static str, value: &'static str) -> Self {
        self.headers.push((name, value));
        self
    }
    fn origin(mut self, origin: &'static str) -> Self {
        self.origin = Some(origin);
        self
    }
}

impl TestApp {
    async fn new(setup_token: Option<SetupToken>) -> Self {
        // Link-check tests talk to a server on this machine.
        Self::with_checker(setup_token, Checker::allowing_loopback().unwrap()).await
    }

    async fn with_checker(setup_token: Option<SetupToken>, link_checker: Checker) -> Self {
        let (dir, pool) = test_pool().await;
        let setup_display = None;
        let state = AppState::new(Settings {
            pool,
            setup_token,
            secure_cookies: false,
            forward_auth: None,
            link_checker,
        });
        Self {
            router: router(state.clone()),
            state,
            setup_display,
            _dir: dir,
        }
    }

    /// An empty database waiting for its first admin.
    async fn awaiting_setup() -> Self {
        let (token, display) = SetupToken::generate();
        let mut app = Self::new(Some(token)).await;
        app.setup_display = Some(display);
        app
    }

    /// A database that already has users, so setup is closed.
    async fn running() -> Self {
        Self::new(None).await
    }

    async fn send(&self, request: Send) -> Reply {
        let mut builder = Request::builder()
            .method(request.method)
            .uri(request.uri)
            .header(header::HOST, "bkd.test");
        if let Some(cookie) = request.cookie {
            builder = builder.header(header::COOKIE, cookie);
        }
        for (name, value) in request.headers {
            builder = builder.header(name, value);
        }
        if let Some(origin) = request.origin {
            builder = builder.header(header::ORIGIN, origin);
        }
        let body = match (request.body, request.raw) {
            (Some(value), _) => {
                builder = builder.header(header::CONTENT_TYPE, "application/json");
                Body::from(value.to_string())
            }
            (None, Some((content_type, bytes))) => {
                builder = builder.header(header::CONTENT_TYPE, content_type);
                Body::from(bytes)
            }
            (None, None) => Body::empty(),
        };
        let mut http_request = builder.body(body).unwrap();
        let client: SocketAddr = format!("{}:50000", request.ip).parse().unwrap();
        http_request.extensions_mut().insert(ConnectInfo(client));

        let response = self.router.clone().oneshot(http_request).await.unwrap();
        let (parts, body) = response.into_parts();
        let bytes = axum::body::to_bytes(body, usize::MAX).await.unwrap();
        Reply {
            status: parts.status,
            headers: parts.headers,
            body: serde_json::from_slice(&bytes).unwrap_or(Value::Null),
            text: String::from_utf8_lossy(&bytes).into_owned(),
        }
    }

    /// Adds a user straight into the database, bypassing the HTTP flow.
    async fn add_user(&self, username: &str, is_admin: bool) -> i64 {
        let hash = password::hash(PASSWORD).unwrap();
        sqlx::query_scalar(
            "INSERT INTO users (username, password_hash, is_admin, created_at)
             VALUES (?1, ?2, ?3, 0) RETURNING id",
        )
        .bind(username)
        .bind(hash)
        .bind(is_admin)
        .fetch_one(&self.state.pool)
        .await
        .unwrap()
    }

    /// Adds a user and signs them in; returns the session token.
    async fn sign_in_as(&self, username: &str) -> String {
        self.add_user(username, false).await;
        let reply = self.login(username, PASSWORD).await;
        assert_eq!(reply.status, StatusCode::OK);
        session_token(&reply)
    }

    async fn login(&self, username: &str, password: &str) -> Reply {
        let body = json!({"username": username, "password": password});
        self.send(Send::post("/api/login", body)).await
    }
}

fn session_token(reply: &Reply) -> String {
    let cookie = reply
        .headers
        .get(header::SET_COOKIE)
        .unwrap()
        .to_str()
        .unwrap();
    let (pair, _) = cookie.split_once(';').unwrap();
    pair.strip_prefix("bkd_session=").unwrap().to_owned()
}

fn bookmark(url: &str, title: &str, tags: &[&str]) -> Value {
    json!({"url": url, "title": title, "note": "", "tags": tags})
}

async fn create_bookmark(app: &TestApp, token: &str, title: &str, tags: &[&str]) -> Value {
    let url = format!("https://example.com/{}", title.replace(' ', "-"));
    let reply = app
        .send(Send::post("/api/bookmarks", bookmark(&url, title, tags)).cookie(token))
        .await;
    assert_eq!(reply.status, StatusCode::CREATED);
    reply.body
}

fn titles(reply: &Reply) -> Vec<&str> {
    reply.body["bookmarks"]
        .as_array()
        .unwrap()
        .iter()
        .map(|bookmark| bookmark["title"].as_str().unwrap())
        .collect()
}

// ---------------------------------------------------------------- setup

#[tokio::test]
async fn state_reports_that_setup_is_needed() {
    let app = TestApp::awaiting_setup().await;
    let reply = app.send(Send::get("/api/state")).await;
    assert_eq!(reply.status, StatusCode::OK);
    assert_eq!(reply.body, json!({"setup_required": true, "user": null}));
}

#[tokio::test]
async fn setup_with_the_right_token_creates_a_signed_in_admin() {
    let app = TestApp::awaiting_setup().await;
    let body = json!({"token": app.setup_display, "username": "Henry", "password": PASSWORD});
    let reply = app.send(Send::post("/api/setup", body)).await;
    assert_eq!(reply.status, StatusCode::CREATED);

    let state = app
        .send(Send::get("/api/state").cookie(&session_token(&reply)))
        .await;
    assert_eq!(state.body["setup_required"], false);
    assert_eq!(state.body["user"]["username"], "henry");
    assert_eq!(state.body["user"]["is_admin"], true);
}

#[tokio::test]
async fn setup_accepts_a_sloppily_typed_token() {
    let app = TestApp::awaiting_setup().await;
    let sloppy = app
        .setup_display
        .as_deref()
        .unwrap()
        .to_lowercase()
        .replace('-', " ");
    let body = json!({"token": sloppy, "username": "henry", "password": PASSWORD});
    assert_eq!(
        app.send(Send::post("/api/setup", body)).await.status,
        StatusCode::CREATED
    );
}

#[tokio::test]
async fn setup_with_a_wrong_token_is_refused() {
    let app = TestApp::awaiting_setup().await;
    let body = json!({"token": "00000-00000", "username": "henry", "password": PASSWORD});
    let reply = app.send(Send::post("/api/setup", body)).await;
    assert_eq!(reply.status, StatusCode::FORBIDDEN);
    assert!(reply.headers.get(header::SET_COOKIE).is_none());
    let state = app.send(Send::get("/api/state")).await;
    assert_eq!(state.body["setup_required"], true);
}

#[tokio::test]
async fn setup_can_only_succeed_once() {
    let app = TestApp::awaiting_setup().await;
    let body = json!({"token": app.setup_display, "username": "henry", "password": PASSWORD});
    assert_eq!(
        app.send(Send::post("/api/setup", body.clone()))
            .await
            .status,
        StatusCode::CREATED
    );

    let second = json!({"token": app.setup_display, "username": "mei", "password": PASSWORD});
    let reply = app.send(Send::post("/api/setup", second)).await;
    assert_eq!(reply.status, StatusCode::CONFLICT);
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM users")
        .fetch_one(&app.state.pool)
        .await
        .unwrap();
    assert_eq!(count, 1);
}

#[tokio::test]
async fn setup_is_closed_when_the_server_started_with_users() {
    let app = TestApp::running().await;
    let body = json!({"token": "00000-00000", "username": "henry", "password": PASSWORD});
    let reply = app.send(Send::post("/api/setup", body)).await;
    assert_eq!(reply.status, StatusCode::CONFLICT);
}

#[tokio::test]
async fn setup_rejects_a_bad_username_or_password_without_using_up_the_token() {
    let app = TestApp::awaiting_setup().await;
    let token = app.setup_display.clone();
    // More attempts than the rate limit allows: only a refund keeps them all at 400.
    for attempt in 0..8 {
        let (username, password) = if attempt % 2 == 0 {
            ("x", PASSWORD)
        } else {
            ("henry", "short")
        };
        let body = json!({"token": token, "username": username, "password": password});
        let reply = app.send(Send::post("/api/setup", body)).await;
        assert_eq!(reply.status, StatusCode::BAD_REQUEST);
    }
    let body = json!({"token": token, "username": "henry", "password": PASSWORD});
    assert_eq!(
        app.send(Send::post("/api/setup", body)).await.status,
        StatusCode::CREATED
    );
}

#[tokio::test]
async fn guessing_the_setup_token_is_rate_limited_per_ip() {
    let app = TestApp::awaiting_setup().await;
    let wrong = json!({"token": "00000-00000", "username": "henry", "password": PASSWORD});
    for _ in 0..5 {
        let reply = app.send(Send::post("/api/setup", wrong.clone())).await;
        assert_eq!(reply.status, StatusCode::FORBIDDEN);
    }
    let blocked = app.send(Send::post("/api/setup", wrong)).await;
    assert_eq!(blocked.status, StatusCode::TOO_MANY_REQUESTS);
    assert!(blocked.headers.contains_key(header::RETRY_AFTER));

    // Even the right token waits, and another address is not affected.
    let right = json!({"token": app.setup_display, "username": "henry", "password": PASSWORD});
    let still_blocked = app.send(Send::post("/api/setup", right.clone())).await;
    assert_eq!(still_blocked.status, StatusCode::TOO_MANY_REQUESTS);
    let elsewhere = app
        .send(Send::post("/api/setup", right).ip("198.51.100.7"))
        .await;
    assert_eq!(elsewhere.status, StatusCode::CREATED);
}

// ---------------------------------------------------------------- login

#[tokio::test]
async fn login_sets_a_http_only_strict_cookie() {
    let app = TestApp::running().await;
    app.add_user("henry", false).await;
    let reply = app.login("Henry", PASSWORD).await;
    assert_eq!(reply.status, StatusCode::OK);
    let cookie = reply
        .headers
        .get(header::SET_COOKIE)
        .unwrap()
        .to_str()
        .unwrap();
    assert!(cookie.contains("HttpOnly") && cookie.contains("SameSite=Strict"));
    assert!(!cookie.contains("Secure"));
}

#[tokio::test]
async fn every_login_failure_looks_the_same() {
    let app = TestApp::running().await;
    app.add_user("henry", false).await;
    app.add_user("mei", false).await;
    sqlx::query("UPDATE users SET disabled_at = 1 WHERE username = 'mei'")
        .execute(&app.state.pool)
        .await
        .unwrap();

    let wrong_password = app.login("henry", "not the password").await;
    let unknown_user = app.login("nobody", PASSWORD).await;
    let disabled_user = app.login("mei", PASSWORD).await;
    let malformed_name = app.login("not a name!", PASSWORD).await;
    for reply in [
        &wrong_password,
        &unknown_user,
        &disabled_user,
        &malformed_name,
    ] {
        assert_eq!(reply.status, StatusCode::UNAUTHORIZED);
        assert_eq!(reply.body, wrong_password.body);
        assert!(reply.headers.get(header::SET_COOKIE).is_none());
    }
}

#[tokio::test]
async fn repeated_failures_lock_out_the_ip_even_for_the_right_password() {
    let app = TestApp::running().await;
    app.add_user("henry", false).await;
    for _ in 0..10 {
        assert_eq!(
            app.login("henry", "wrong").await.status,
            StatusCode::UNAUTHORIZED
        );
    }
    let blocked = app.login("henry", PASSWORD).await;
    assert_eq!(blocked.status, StatusCode::TOO_MANY_REQUESTS);
    assert!(blocked.headers.contains_key(header::RETRY_AFTER));
}

#[tokio::test]
async fn a_successful_login_does_not_erase_earlier_failures() {
    let app = TestApp::running().await;
    app.add_user("henry", false).await;
    for _ in 0..9 {
        app.login("henry", "wrong").await;
    }
    assert_eq!(app.login("henry", PASSWORD).await.status, StatusCode::OK);
    assert_eq!(app.login("henry", PASSWORD).await.status, StatusCode::OK);
    // 9 failures are still on record: one more spends the last attempt, then it blocks.
    assert_eq!(
        app.login("henry", "wrong").await.status,
        StatusCode::UNAUTHORIZED
    );
    assert_eq!(
        app.login("henry", PASSWORD).await.status,
        StatusCode::TOO_MANY_REQUESTS
    );
}

#[tokio::test]
async fn logout_ends_the_session_and_clears_the_cookie() {
    let app = TestApp::running().await;
    let token = app.sign_in_as("henry").await;
    let reply = app
        .send(Send::post("/api/logout", json!({})).cookie(&token))
        .await;
    assert_eq!(reply.status, StatusCode::NO_CONTENT);
    let cookie = reply
        .headers
        .get(header::SET_COOKIE)
        .unwrap()
        .to_str()
        .unwrap();
    assert!(cookie.contains("Max-Age=0"));

    let after = app.send(Send::get("/api/bookmarks").cookie(&token)).await;
    assert_eq!(after.status, StatusCode::UNAUTHORIZED);
}

#[tokio::test]
async fn disabling_a_user_cuts_off_the_existing_session() {
    let app = TestApp::running().await;
    let token = app.sign_in_as("henry").await;
    sqlx::query("UPDATE users SET disabled_at = 1")
        .execute(&app.state.pool)
        .await
        .unwrap();
    let reply = app.send(Send::get("/api/bookmarks").cookie(&token)).await;
    assert_eq!(reply.status, StatusCode::UNAUTHORIZED);
}

async fn change_password(app: &TestApp, token: &str, current: &str, new: &str) -> Reply {
    let body = json!({"current": current, "new": new});
    app.send(Send::put("/api/account/password", body).cookie(token))
        .await
}

#[tokio::test]
async fn changing_the_password_ends_every_other_session_and_keeps_this_browser_signed_in() {
    let app = TestApp::running().await;
    let token = app.sign_in_as("henry").await;
    let other = session_token(&app.login("henry", PASSWORD).await);

    let reply = change_password(&app, &token, PASSWORD, "a brand new password").await;
    assert_eq!(reply.status, StatusCode::NO_CONTENT);
    let fresh = session_token(&reply);

    for old in [&token, &other] {
        let after = app.send(Send::get("/api/bookmarks").cookie(old)).await;
        assert_eq!(after.status, StatusCode::UNAUTHORIZED);
    }
    let after = app.send(Send::get("/api/bookmarks").cookie(&fresh)).await;
    assert_eq!(after.status, StatusCode::OK);
    assert_eq!(
        app.login("henry", PASSWORD).await.status,
        StatusCode::UNAUTHORIZED
    );
    let login = app.login("henry", "a brand new password").await;
    assert_eq!(login.status, StatusCode::OK);
}

#[tokio::test]
async fn changing_the_password_needs_the_current_one_and_a_valid_new_one() {
    let app = TestApp::running().await;
    let token = app.sign_in_as("henry").await;

    let wrong = change_password(&app, &token, "not the password", "a brand new password").await;
    assert_eq!(wrong.status, StatusCode::FORBIDDEN);
    for bad in ["short", "henry is my name", PASSWORD] {
        let reply = change_password(&app, &token, PASSWORD, bad).await;
        assert_eq!(reply.status, StatusCode::BAD_REQUEST, "{bad:?}");
    }
    let anonymous = app
        .send(Send::put(
            "/api/account/password",
            json!({"current": PASSWORD, "new": "a brand new password"}),
        ))
        .await;
    assert_eq!(anonymous.status, StatusCode::UNAUTHORIZED);

    // Nothing changed: the old password and the old session still work.
    assert_eq!(app.login("henry", PASSWORD).await.status, StatusCode::OK);
    let after = app.send(Send::get("/api/bookmarks").cookie(&token)).await;
    assert_eq!(after.status, StatusCode::OK);
}

#[tokio::test]
async fn guessing_the_current_password_is_rate_limited() {
    let app = TestApp::running().await;
    let token = app.sign_in_as("henry").await;
    for _ in 0..super::LOGIN_ATTEMPTS_PER_WINDOW {
        let reply = change_password(&app, &token, "not the password", "a brand new password").await;
        assert_eq!(reply.status, StatusCode::FORBIDDEN);
    }
    let reply = change_password(&app, &token, PASSWORD, "a brand new password").await;
    assert_eq!(reply.status, StatusCode::TOO_MANY_REQUESTS);
}

#[tokio::test]
async fn every_response_carries_the_security_headers() {
    let app = TestApp::running().await;
    for uri in ["/", "/app.js", "/api/state", "/api/bookmarks"] {
        let reply = app.send(Send::get(uri)).await;
        assert_eq!(reply.headers[header::X_FRAME_OPTIONS], "DENY", "{uri}");
        assert_eq!(reply.headers["cross-origin-opener-policy"], "same-origin");
        assert_eq!(reply.headers["cross-origin-resource-policy"], "same-origin");
        assert!(reply.headers.contains_key("permissions-policy"));
        assert!(
            !reply
                .headers
                .contains_key(header::STRICT_TRANSPORT_SECURITY)
        );
    }
}

#[tokio::test]
async fn hsts_is_sent_only_when_cookies_are_secure() {
    let (_dir, pool) = test_pool().await;
    let state = AppState::new(Settings {
        pool,
        setup_token: None,
        secure_cookies: true,
        forward_auth: None,
        link_checker: Checker::allowing_loopback().unwrap(),
    });
    let request = Request::builder()
        .uri("/")
        .header(header::HOST, "bkd.test")
        .body(Body::empty())
        .unwrap();
    let response = router(state).oneshot(request).await.unwrap();
    assert!(
        response
            .headers()
            .contains_key(header::STRICT_TRANSPORT_SECURITY)
    );
}

// ------------------------------------------------------------ bookmarks

#[tokio::test]
async fn bookmark_routes_need_a_session() {
    let app = TestApp::running().await;
    let create = Send::post("/api/bookmarks", bookmark("https://a.b", "t", &[]));
    for request in [
        Send::get("/api/bookmarks"),
        create,
        Send::put("/api/bookmarks/1", bookmark("https://a.b", "t", &[])),
        Send::delete("/api/bookmarks/1"),
        Send::get("/api/tags"),
        Send::post(
            "/api/bookmarks/bulk",
            json!({"ids": [1], "action": "delete"}),
        ),
        Send::post("/api/bookmarks/check", json!({"ids": [1]})),
    ] {
        assert_eq!(app.send(request).await.status, StatusCode::UNAUTHORIZED);
    }
    let forged = app
        .send(Send::get("/api/bookmarks").cookie("0123456789abcdef"))
        .await;
    assert_eq!(forged.status, StatusCode::UNAUTHORIZED);
}

#[tokio::test]
async fn a_bookmark_can_be_created_listed_changed_and_deleted() {
    let app = TestApp::running().await;
    let token = app.sign_in_as("henry").await;

    let created = create_bookmark(&app, &token, "Rust book", &["Rust", "book"]).await;
    assert_eq!(created["tags"], json!(["book", "rust"]));
    let id = created["id"].as_i64().unwrap();

    let listed = app.send(Send::get("/api/bookmarks").cookie(&token)).await;
    assert_eq!(titles(&listed), ["Rust book"]);

    let uri = format!("/api/bookmarks/{id}");
    let changed = app
        .send(
            Send::put(
                &uri,
                bookmark("https://example.com/new", "New title", &["x"]),
            )
            .cookie(&token),
        )
        .await;
    assert_eq!(changed.status, StatusCode::OK);
    assert_eq!(changed.body["title"], "New title");
    assert_eq!(changed.body["tags"], json!(["x"]));

    let deleted = app.send(Send::delete(&uri).cookie(&token)).await;
    assert_eq!(deleted.status, StatusCode::NO_CONTENT);
    let again = app.send(Send::delete(&uri).cookie(&token)).await;
    assert_eq!(again.status, StatusCode::NOT_FOUND);
    let empty = app.send(Send::get("/api/bookmarks").cookie(&token)).await;
    assert!(titles(&empty).is_empty());
}

#[tokio::test]
async fn invalid_bookmarks_are_rejected_with_a_reason() {
    let app = TestApp::running().await;
    let token = app.sign_in_as("henry").await;
    for body in [
        bookmark("javascript:alert(1)", "t", &[]),
        bookmark("https://a.b", "   ", &[]),
        json!({"url": "https://a.b"}),
    ] {
        let reply = app
            .send(Send::post("/api/bookmarks", body).cookie(&token))
            .await;
        assert!(reply.status.is_client_error(), "{:?}", reply.status);
    }
    let reply = app
        .send(Send::post("/api/bookmarks", bookmark("ftp://a.b", "t", &[])).cookie(&token))
        .await;
    assert_eq!(reply.status, StatusCode::BAD_REQUEST);
    assert!(reply.body["error"].as_str().unwrap().contains("http"));
}

#[tokio::test]
async fn users_cannot_see_or_touch_each_others_bookmarks() {
    let app = TestApp::running().await;
    let henry = app.sign_in_as("henry").await;
    let mei = app.sign_in_as("mei").await;
    let private = create_bookmark(&app, &henry, "henry secret", &["private"]).await;
    let uri = format!("/api/bookmarks/{}", private["id"]);

    let listed = app.send(Send::get("/api/bookmarks").cookie(&mei)).await;
    assert!(titles(&listed).is_empty());
    let searched = app
        .send(Send::get("/api/bookmarks?q=secret").cookie(&mei))
        .await;
    assert!(titles(&searched).is_empty());
    let tags = app.send(Send::get("/api/tags").cookie(&mei)).await;
    assert_eq!(tags.body["tags"], json!([]));

    let hijack = app
        .send(Send::put(&uri, bookmark("https://evil.test", "hijacked", &[])).cookie(&mei))
        .await;
    assert_eq!(hijack.status, StatusCode::NOT_FOUND);
    let delete = app.send(Send::delete(&uri).cookie(&mei)).await;
    assert_eq!(delete.status, StatusCode::NOT_FOUND);

    let intact = app.send(Send::get("/api/bookmarks").cookie(&henry)).await;
    assert_eq!(titles(&intact), ["henry secret"]);
}

#[tokio::test]
async fn search_and_tag_filters_work_through_the_api() {
    let app = TestApp::running().await;
    let token = app.sign_in_as("henry").await;
    create_bookmark(&app, &token, "學習 Rust", &["教學"]).await;
    create_bookmark(&app, &token, "Rust async", &["rust"]).await;
    create_bookmark(&app, &token, "Cooking", &["rust"]).await;

    let by_text = app
        .send(Send::get("/api/bookmarks?q=rust%20async").cookie(&token))
        .await;
    assert_eq!(titles(&by_text), ["Rust async"]);
    let chinese = app
        .send(Send::get("/api/bookmarks?q=%E5%AD%B8%E7%BF%92").cookie(&token))
        .await;
    assert_eq!(titles(&chinese), ["學習 Rust"]);
    let by_tag = app
        .send(Send::get("/api/bookmarks?tag=Rust").cookie(&token))
        .await;
    assert_eq!(titles(&by_tag), ["Cooking", "Rust async"]);
    let both = app
        .send(Send::get("/api/bookmarks?tag=rust&q=cook").cookie(&token))
        .await;
    assert_eq!(titles(&both), ["Cooking"]);

    let tags = app.send(Send::get("/api/tags").cookie(&token)).await;
    assert_eq!(
        tags.body["tags"],
        json!([{"tag": "rust", "count": 2}, {"tag": "教學", "count": 1}])
    );
}

#[tokio::test]
async fn random_picks_from_the_filtered_bookmarks_of_the_caller_only() {
    let app = TestApp::running().await;
    let token = app.sign_in_as("henry").await;
    let other = app.sign_in_as("mei").await;
    create_bookmark(&app, &token, "Rust async", &["rust"]).await;
    create_bookmark(&app, &token, "Cooking", &[]).await;
    create_bookmark(&app, &other, "Mei only", &["rust"]).await;

    let by_tag = app
        .send(Send::get("/api/bookmarks/random?tag=rust").cookie(&token))
        .await;
    assert_eq!(by_tag.status, StatusCode::OK);
    assert_eq!(by_tag.body["title"], "Rust async");
    let by_text = app
        .send(Send::get("/api/bookmarks/random?q=cook").cookie(&token))
        .await;
    assert_eq!(by_text.body["title"], "Cooking");

    let none = app
        .send(Send::get("/api/bookmarks/random?q=zzz").cookie(&token))
        .await;
    assert_eq!(none.status, StatusCode::NOT_FOUND);
    // Only the other user has this one: a missing user filter would return it.
    let theirs = app
        .send(Send::get("/api/bookmarks/random?q=Mei").cookie(&token))
        .await;
    assert_eq!(theirs.status, StatusCode::NOT_FOUND);
    let signed_out = app.send(Send::get("/api/bookmarks/random")).await;
    assert_eq!(signed_out.status, StatusCode::UNAUTHORIZED);
}

#[tokio::test]
async fn random_does_not_always_pick_the_same_bookmark() {
    let app = TestApp::running().await;
    let token = app.sign_in_as("henry").await;
    for n in 0..10 {
        create_bookmark(&app, &token, &format!("item {n}"), &[]).await;
    }
    let mut seen = std::collections::HashSet::new();
    for _ in 0..40 {
        let reply = app
            .send(Send::get("/api/bookmarks/random").cookie(&token))
            .await;
        seen.insert(reply.body["id"].as_i64().unwrap());
    }
    // 40 draws from 10 would all agree with probability 10^-39.
    assert!(seen.len() > 1);
}

async fn bulk(app: &TestApp, token: &str, body: Value) -> Reply {
    app.send(Send::post("/api/bookmarks/bulk", body).cookie(token))
        .await
}

fn tags_of(list: &Reply, title: &str) -> Vec<String> {
    list.body["bookmarks"]
        .as_array()
        .unwrap()
        .iter()
        .find(|b| b["title"] == title)
        .map(|b| serde_json::from_value(b["tags"].clone()).unwrap())
        .unwrap()
}

#[tokio::test]
async fn bulk_add_and_remove_tags_report_how_many_bookmarks_changed() {
    let app = TestApp::running().await;
    let token = app.sign_in_as("henry").await;
    let a = create_bookmark(&app, &token, "a", &["rust"]).await;
    let b = create_bookmark(&app, &token, "b", &[]).await;
    let c = create_bookmark(&app, &token, "c", &["keep"]).await;
    let ids = json!([a["id"], b["id"], a["id"]]);

    let added = bulk(
        &app,
        &token,
        json!({"ids": ids, "action": "add_tags", "tags": [" Rust ", "New"]}),
    )
    .await;
    assert_eq!(added.status, StatusCode::OK);
    // "a" already had rust but gains "new", so both are changed.
    assert_eq!(added.body, json!({"changed": 2}));
    let list = app.send(Send::get("/api/bookmarks").cookie(&token)).await;
    assert_eq!(tags_of(&list, "a"), ["new", "rust"]);
    assert_eq!(tags_of(&list, "b"), ["new", "rust"]);
    assert_eq!(tags_of(&list, "c"), ["keep"]);

    let again = bulk(
        &app,
        &token,
        json!({"ids": [a["id"]], "action": "add_tags", "tags": ["rust"]}),
    )
    .await;
    assert_eq!(again.body, json!({"changed": 0}));

    let removed = bulk(
        &app,
        &token,
        json!({"ids": [a["id"], b["id"], c["id"]], "action": "remove_tags", "tags": ["rust"]}),
    )
    .await;
    assert_eq!(removed.body, json!({"changed": 2}));
    let list = app.send(Send::get("/api/bookmarks").cookie(&token)).await;
    assert_eq!(tags_of(&list, "a"), ["new"]);
    assert_eq!(tags_of(&list, "c"), ["keep"]);
}

#[tokio::test]
async fn bulk_delete_removes_the_bookmarks_and_their_tags() {
    let app = TestApp::running().await;
    let token = app.sign_in_as("henry").await;
    let a = create_bookmark(&app, &token, "a", &["x"]).await;
    let b = create_bookmark(&app, &token, "b", &["y"]).await;
    create_bookmark(&app, &token, "c", &["z"]).await;

    let reply = bulk(
        &app,
        &token,
        json!({"ids": [a["id"], b["id"]], "action": "delete"}),
    )
    .await;
    assert_eq!(reply.status, StatusCode::OK);
    assert_eq!(reply.body, json!({"changed": 2}));
    let list = app.send(Send::get("/api/bookmarks").cookie(&token)).await;
    assert_eq!(titles(&list), ["c"]);
    let tags = app.send(Send::get("/api/tags").cookie(&token)).await;
    assert_eq!(tags.body["tags"], json!([{"tag": "z", "count": 1}]));
}

#[tokio::test]
async fn a_bulk_request_with_an_unknown_or_foreign_id_changes_nothing() {
    let app = TestApp::running().await;
    let henry = app.sign_in_as("henry").await;
    let mei = app.sign_in_as("mei").await;
    let mine = create_bookmark(&app, &henry, "mine", &[]).await;
    let theirs = create_bookmark(&app, &mei, "theirs", &[]).await;

    for action in [
        json!({"action": "delete"}),
        json!({"action": "add_tags", "tags": ["x"]}),
        json!({"action": "remove_tags", "tags": ["x"]}),
    ] {
        let mut body = action;
        body["ids"] = json!([mine["id"], theirs["id"]]);
        assert_eq!(
            bulk(&app, &henry, body.clone()).await.status,
            StatusCode::NOT_FOUND
        );
        body["ids"] = json!([mine["id"], 99999]);
        assert_eq!(bulk(&app, &henry, body).await.status, StatusCode::NOT_FOUND);
    }
    let henrys = app.send(Send::get("/api/bookmarks").cookie(&henry)).await;
    assert_eq!(titles(&henrys), ["mine"]);
    assert_eq!(tags_of(&henrys, "mine"), Vec::<String>::new());
    let meis = app.send(Send::get("/api/bookmarks").cookie(&mei)).await;
    assert_eq!(titles(&meis), ["theirs"]);
}

#[tokio::test]
async fn bulk_rejects_bad_requests_with_a_reason() {
    let app = TestApp::running().await;
    let token = app.sign_in_as("henry").await;
    let a = create_bookmark(&app, &token, "a", &[]).await;
    let too_many: Vec<i64> = (1..=201).collect();
    let long_tag = "x".repeat(51);

    for body in [
        json!({"ids": [], "action": "delete"}),
        json!({"ids": too_many, "action": "delete"}),
        json!({"ids": [a["id"]], "action": "add_tags", "tags": []}),
        json!({"ids": [a["id"]], "action": "add_tags", "tags": [" "]}),
        json!({"ids": [a["id"]], "action": "remove_tags", "tags": [long_tag]}),
    ] {
        let reply = bulk(&app, &token, body.clone()).await;
        assert_eq!(reply.status, StatusCode::BAD_REQUEST, "{body}");
        assert!(reply.body["error"].is_string());
    }
    let unknown = bulk(&app, &token, json!({"ids": [a["id"]], "action": "explode"})).await;
    assert!(unknown.status.is_client_error());
    let missing_tags = bulk(
        &app,
        &token,
        json!({"ids": [a["id"]], "action": "add_tags"}),
    )
    .await;
    assert!(missing_tags.status.is_client_error());
    let exactly_max: Vec<i64> = (1..=200).collect();
    let at_limit = bulk(
        &app,
        &token,
        json!({"ids": exactly_max, "action": "delete"}),
    )
    .await;
    assert_eq!(at_limit.status, StatusCode::NOT_FOUND);
}

#[tokio::test]
async fn bulk_add_that_would_exceed_the_tag_limit_changes_nothing() {
    let app = TestApp::running().await;
    let token = app.sign_in_as("henry").await;
    let full_tags: Vec<String> = (0..20).map(|n| format!("t{n}")).collect();
    let full_refs: Vec<&str> = full_tags.iter().map(String::as_str).collect();
    let full = create_bookmark(&app, &token, "full", &full_refs).await;
    let empty = create_bookmark(&app, &token, "empty", &[]).await;

    let reply = bulk(
        &app,
        &token,
        json!({"ids": [empty["id"], full["id"]], "action": "add_tags", "tags": ["one-more"]}),
    )
    .await;
    assert_eq!(reply.status, StatusCode::BAD_REQUEST);
    let list = app.send(Send::get("/api/bookmarks").cookie(&token)).await;
    assert_eq!(tags_of(&list, "empty"), Vec::<String>::new());
    assert_eq!(tags_of(&list, "full").len(), 20);
}

#[tokio::test]
async fn paging_returns_distinct_slices() {
    let app = TestApp::running().await;
    let token = app.sign_in_as("henry").await;
    for n in 0..5 {
        create_bookmark(&app, &token, &format!("item {n}"), &[]).await;
    }
    let first = app
        .send(Send::get("/api/bookmarks?limit=2").cookie(&token))
        .await;
    let second = app
        .send(Send::get("/api/bookmarks?limit=2&offset=2").cookie(&token))
        .await;
    assert_eq!(titles(&first).len(), 2);
    assert_eq!(titles(&second).len(), 2);
    assert!(
        titles(&first)
            .iter()
            .all(|title| !titles(&second).contains(title))
    );
}

// ------------------------------------------------------------- hardening

#[tokio::test]
async fn a_cross_site_write_is_refused_but_a_same_site_one_passes() {
    let app = TestApp::running().await;
    let token = app.sign_in_as("henry").await;
    let body = bookmark("https://a.b", "t", &[]);

    let forged = Send::post("/api/bookmarks", body.clone())
        .cookie(&token)
        .origin("https://evil.test");
    assert_eq!(app.send(forged).await.status, StatusCode::FORBIDDEN);

    let genuine = Send::post("/api/bookmarks", body)
        .cookie(&token)
        .origin("http://bkd.test");
    assert_eq!(app.send(genuine).await.status, StatusCode::CREATED);
}

#[tokio::test]
async fn cross_site_update_delete_login_and_setup_are_refused_with_no_store() {
    let app = TestApp::awaiting_setup().await;
    let setup = json!({"token": app.setup_display, "username": "henry", "password": PASSWORD});
    let forged_setup = Send::post("/api/setup", setup).origin("https://evil.test");
    let reply = app.send(forged_setup).await;
    assert_eq!(reply.status, StatusCode::FORBIDDEN);
    assert_eq!(reply.headers[header::CACHE_CONTROL], "no-store");

    let app = TestApp::running().await;
    let token = app.sign_in_as("henry").await;
    let id = create_bookmark(&app, &token, "keep me", &[]).await["id"]
        .as_i64()
        .unwrap();
    let uri = format!("/api/bookmarks/{id}");

    let login = json!({"username": "henry", "password": PASSWORD});
    let forged_login = Send::post("/api/login", login).origin("https://evil.test");
    assert_eq!(app.send(forged_login).await.status, StatusCode::FORBIDDEN);
    let forged_put = Send::put(&uri, bookmark("https://a.b", "changed", &[]))
        .cookie(&token)
        .origin("https://evil.test");
    assert_eq!(app.send(forged_put).await.status, StatusCode::FORBIDDEN);
    let forged_delete = Send::delete(&uri)
        .cookie(&token)
        .origin("https://evil.test");
    assert_eq!(app.send(forged_delete).await.status, StatusCode::FORBIDDEN);

    let listed = app.send(Send::get("/api/bookmarks").cookie(&token)).await;
    assert_eq!(titles(&listed), ["keep me"]);
}

#[tokio::test]
async fn a_tag_filter_that_cannot_exist_returns_nothing() {
    let app = TestApp::running().await;
    let token = app.sign_in_as("henry").await;
    create_bookmark(&app, &token, "tagged", &["rust"]).await;
    let uri = format!("/api/bookmarks?tag={}", "x".repeat(200));
    let reply = app.send(Send::get(&uri).cookie(&token)).await;
    assert_eq!(reply.status, StatusCode::OK);
    assert!(titles(&reply).is_empty());
}

#[tokio::test]
async fn a_form_post_cannot_create_bookmarks() {
    let app = TestApp::running().await;
    let token = app.sign_in_as("henry").await;
    let request = Request::builder()
        .method(Method::POST)
        .uri("/api/bookmarks")
        .header(header::HOST, "bkd.test")
        .header(header::COOKIE, format!("bkd_session={token}"))
        .header(header::CONTENT_TYPE, "application/x-www-form-urlencoded")
        .body(Body::from("url=https%3A%2F%2Fa.b&title=t"))
        .unwrap();
    let mut request = request;
    let client: SocketAddr = "192.0.2.1:50000".parse().unwrap();
    request.extensions_mut().insert(ConnectInfo(client));
    let response = app.router.clone().oneshot(request).await.unwrap();
    assert_eq!(response.status(), StatusCode::UNSUPPORTED_MEDIA_TYPE);
}

#[tokio::test]
async fn api_responses_are_not_cached() {
    let app = TestApp::running().await;
    let reply = app.send(Send::get("/api/state")).await;
    assert_eq!(reply.headers[header::CACHE_CONTROL], "no-store");
    assert_eq!(reply.headers[header::X_CONTENT_TYPE_OPTIONS], "nosniff");
}

#[tokio::test]
async fn oversized_bodies_are_refused() {
    let app = TestApp::running().await;
    let token = app.sign_in_as("henry").await;
    let huge = json!({"url": "https://a.b", "title": "t", "note": "x".repeat(100_000)});
    let reply = app
        .send(Send::post("/api/bookmarks", huge).cookie(&token))
        .await;
    assert_eq!(reply.status, StatusCode::PAYLOAD_TOO_LARGE);
}

// ------------------------------------------------------- user management

impl TestApp {
    async fn sign_in_as_admin(&self, username: &str) -> (i64, String) {
        let id = self.add_user(username, true).await;
        let reply = self.login(username, PASSWORD).await;
        assert_eq!(reply.status, StatusCode::OK);
        (id, session_token(&reply))
    }
}

fn user_id(reply: &Reply, username: &str) -> i64 {
    reply.body["users"]
        .as_array()
        .unwrap()
        .iter()
        .find(|user| user["username"] == username)
        .unwrap()["id"]
        .as_i64()
        .unwrap()
}

#[tokio::test]
async fn user_routes_refuse_anonymous_callers_and_plain_users() {
    let app = TestApp::running().await;
    let plain = app.sign_in_as("mei").await;
    let new_user = json!({"username": "bob", "password": PASSWORD});

    assert_eq!(
        app.send(Send::get("/api/users")).await.status,
        StatusCode::UNAUTHORIZED
    );
    let listed = app.send(Send::get("/api/users").cookie(&plain)).await;
    assert_eq!(listed.status, StatusCode::FORBIDDEN);
    let created = Send::post("/api/users", new_user).cookie(&plain);
    assert_eq!(app.send(created).await.status, StatusCode::FORBIDDEN);
    let role = Send::put("/api/users/1/role", json!({"is_admin": true})).cookie(&plain);
    assert_eq!(app.send(role).await.status, StatusCode::FORBIDDEN);
    let delete = Send::delete("/api/users/1").cookie(&plain);
    assert_eq!(app.send(delete).await.status, StatusCode::FORBIDDEN);
    assert_eq!(listed.headers[header::CACHE_CONTROL], "no-store");
}

#[tokio::test]
async fn an_admin_creates_a_user_who_can_sign_in() {
    let app = TestApp::running().await;
    let (_, admin) = app.sign_in_as_admin("henry").await;
    let body = json!({"username": "Mei", "password": PASSWORD});
    let created = app
        .send(Send::post("/api/users", body).cookie(&admin))
        .await;
    assert_eq!(created.status, StatusCode::CREATED);
    assert_eq!(created.body["username"], "mei");
    assert_eq!(created.body["is_admin"], false);
    assert!(created.body.get("password_hash").is_none());

    assert_eq!(app.login("mei", PASSWORD).await.status, StatusCode::OK);
    let listed = app.send(Send::get("/api/users").cookie(&admin)).await;
    assert_eq!(listed.body["users"].as_array().unwrap().len(), 2);
    assert!(!listed.body.to_string().contains("argon2"));
}

#[tokio::test]
async fn creating_a_user_validates_and_rejects_duplicates() {
    let app = TestApp::running().await;
    let (_, admin) = app.sign_in_as_admin("henry").await;
    let post = |body: Value| Send::post("/api/users", body).cookie(&admin);

    let bad_name = json!({"username": "x", "password": PASSWORD});
    assert_eq!(
        app.send(post(bad_name)).await.status,
        StatusCode::BAD_REQUEST
    );
    let short = json!({"username": "mei", "password": "short"});
    assert_eq!(app.send(post(short)).await.status, StatusCode::BAD_REQUEST);
    let duplicate = json!({"username": "HENRY", "password": PASSWORD});
    assert_eq!(app.send(post(duplicate)).await.status, StatusCode::CONFLICT);
}

#[tokio::test]
async fn a_demoted_admin_loses_access_on_the_next_request() {
    let app = TestApp::running().await;
    let (_, first) = app.sign_in_as_admin("henry").await;
    let (second_id, second) = app.sign_in_as_admin("mei").await;

    let uri = format!("/api/users/{second_id}/role");
    let demote = Send::put(&uri, json!({"is_admin": false})).cookie(&first);
    assert_eq!(app.send(demote).await.status, StatusCode::NO_CONTENT);

    let still_admin = app.send(Send::get("/api/users").cookie(&second)).await;
    assert_eq!(still_admin.status, StatusCode::FORBIDDEN);
    let state = app.send(Send::get("/api/state").cookie(&second)).await;
    assert_eq!(state.body["user"]["is_admin"], false);
}

#[tokio::test]
async fn the_last_admin_cannot_lock_everyone_out() {
    let app = TestApp::running().await;
    let (admin_id, admin) = app.sign_in_as_admin("henry").await;
    let base = format!("/api/users/{admin_id}");

    let demote = Send::put(&format!("{base}/role"), json!({"is_admin": false})).cookie(&admin);
    assert_eq!(app.send(demote).await.status, StatusCode::CONFLICT);
    let disable =
        Send::put(&format!("{base}/disabled"), json!({"is_disabled": true})).cookie(&admin);
    assert_eq!(app.send(disable).await.status, StatusCode::CONFLICT);
    let delete = Send::delete(&base).cookie(&admin);
    assert_eq!(app.send(delete).await.status, StatusCode::CONFLICT);

    // Still an admin, still signed in.
    let listed = app.send(Send::get("/api/users").cookie(&admin)).await;
    assert_eq!(listed.status, StatusCode::OK);
}

#[tokio::test]
async fn an_admin_may_remove_themselves_when_another_admin_remains() {
    let app = TestApp::running().await;
    let (first_id, first) = app.sign_in_as_admin("henry").await;
    let (_, second) = app.sign_in_as_admin("mei").await;

    let delete = Send::delete(&format!("/api/users/{first_id}")).cookie(&first);
    assert_eq!(app.send(delete).await.status, StatusCode::NO_CONTENT);
    assert_eq!(
        app.send(Send::get("/api/state").cookie(&first)).await.body["user"],
        Value::Null
    );
    // The remaining admin is now the last one.
    let listed = app.send(Send::get("/api/users").cookie(&second)).await;
    let second_id = user_id(&listed, "mei");
    let delete = Send::delete(&format!("/api/users/{second_id}")).cookie(&second);
    assert_eq!(app.send(delete).await.status, StatusCode::CONFLICT);
}

#[tokio::test]
async fn disabling_a_user_ends_their_sessions_and_blocks_login() {
    let app = TestApp::running().await;
    let (_, admin) = app.sign_in_as_admin("henry").await;
    let user = app.sign_in_as("mei").await;
    let listed = app.send(Send::get("/api/users").cookie(&admin)).await;
    let id = user_id(&listed, "mei");

    let uri = format!("/api/users/{id}/disabled");
    let disable = Send::put(&uri, json!({"is_disabled": true})).cookie(&admin);
    assert_eq!(app.send(disable).await.status, StatusCode::NO_CONTENT);
    let bookmarks = app.send(Send::get("/api/bookmarks").cookie(&user)).await;
    assert_eq!(bookmarks.status, StatusCode::UNAUTHORIZED);
    assert_eq!(
        app.login("mei", PASSWORD).await.status,
        StatusCode::UNAUTHORIZED
    );

    let enable = Send::put(&uri, json!({"is_disabled": false})).cookie(&admin);
    assert_eq!(app.send(enable).await.status, StatusCode::NO_CONTENT);
    // The old session stays dead; a fresh login works.
    let old = app.send(Send::get("/api/bookmarks").cookie(&user)).await;
    assert_eq!(old.status, StatusCode::UNAUTHORIZED);
    assert_eq!(app.login("mei", PASSWORD).await.status, StatusCode::OK);
}

#[tokio::test]
async fn resetting_a_password_ends_sessions_and_changes_the_login() {
    let app = TestApp::running().await;
    let (_, admin) = app.sign_in_as_admin("henry").await;
    let user = app.sign_in_as("mei").await;
    let listed = app.send(Send::get("/api/users").cookie(&admin)).await;
    let id = user_id(&listed, "mei");

    let uri = format!("/api/users/{id}/password");
    let weak = Send::put(&uri, json!({"password": "short"})).cookie(&admin);
    assert_eq!(app.send(weak).await.status, StatusCode::BAD_REQUEST);
    assert_eq!(
        app.send(Send::get("/api/bookmarks").cookie(&user))
            .await
            .status,
        StatusCode::OK
    );

    let reset = Send::put(&uri, json!({"password": "a brand new password"})).cookie(&admin);
    assert_eq!(app.send(reset).await.status, StatusCode::NO_CONTENT);
    assert_eq!(
        app.send(Send::get("/api/bookmarks").cookie(&user))
            .await
            .status,
        StatusCode::UNAUTHORIZED
    );
    assert_eq!(
        app.login("mei", PASSWORD).await.status,
        StatusCode::UNAUTHORIZED
    );
    assert_eq!(
        app.login("mei", "a brand new password").await.status,
        StatusCode::OK
    );
}

#[tokio::test]
async fn deleting_a_user_removes_their_data_and_unknown_ids_are_404() {
    let app = TestApp::running().await;
    let (_, admin) = app.sign_in_as_admin("henry").await;
    let user = app.sign_in_as("mei").await;
    create_bookmark(&app, &user, "private", &[]).await;
    let listed = app.send(Send::get("/api/users").cookie(&admin)).await;
    let id = user_id(&listed, "mei");

    let uri = format!("/api/users/{id}");
    let delete = Send::delete(&uri).cookie(&admin);
    assert_eq!(app.send(delete).await.status, StatusCode::NO_CONTENT);
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM bookmarks")
        .fetch_one(&app.state.pool)
        .await
        .unwrap();
    assert_eq!(count, 0);
    assert_eq!(
        app.send(Send::get("/api/bookmarks").cookie(&user))
            .await
            .status,
        StatusCode::UNAUTHORIZED
    );
    let again = Send::delete(&uri).cookie(&admin);
    assert_eq!(app.send(again).await.status, StatusCode::NOT_FOUND);
    let role = Send::put("/api/users/9999/role", json!({"is_admin": true})).cookie(&admin);
    assert_eq!(app.send(role).await.status, StatusCode::NOT_FOUND);
}

#[tokio::test]
async fn a_cross_site_user_change_is_refused() {
    let app = TestApp::running().await;
    let (_, admin) = app.sign_in_as_admin("henry").await;
    let body = json!({"username": "mei", "password": PASSWORD});
    let forged = Send::post("/api/users", body)
        .cookie(&admin)
        .origin("https://evil.test");
    assert_eq!(app.send(forged).await.status, StatusCode::FORBIDDEN);
}

// ------------------------------------------------------ import and export

const FIREFOX_FILE: &str = r#"<!DOCTYPE NETSCAPE-Bookmark-file-1>
<META HTTP-EQUIV="Content-Type" CONTENT="text/html; charset=UTF-8">
<TITLE>Bookmarks</TITLE>
<H1>Bookmarks Menu</H1>
<DL><p>
    <DT><H3 ADD_DATE="1600000000" LAST_MODIFIED="1600000001">Recipes</H3>
    <DL><p>
        <DT><A HREF="https://soup.test/" ADD_DATE="1600000002" TAGS="Winter">Soup &amp; bread</A>
        <DD>use less salt
        <DT><A HREF="place:parent=1&amp;type=7">Smart bookmark</A>
    </DL><p>
    <DT><A HREF="https://news.test/" ADD_DATE="1600000003">News</A>
    <DT><A HREF="https://news.test/">News again</A>
</DL><p>
"#;

async fn import_file(app: &TestApp, token: &str, html: &str) -> Reply {
    app.send(
        Send::new(Method::POST, "/api/import")
            .raw("text/html; charset=utf-8", html)
            .cookie(token),
    )
    .await
}

#[tokio::test]
async fn importing_a_browser_file_adds_skips_and_reports() {
    let app = TestApp::running().await;
    let token = app.sign_in_as("henry").await;

    let reply = import_file(&app, &token, FIREFOX_FILE).await;
    assert_eq!(reply.status, StatusCode::OK);
    assert_eq!(reply.body["added"], 2);
    assert_eq!(reply.body["duplicates"], 1);
    assert_eq!(reply.body["invalid"], 1);
    assert_eq!(
        reply.body["invalid_examples"][0]["url"],
        "place:parent=1&type=7"
    );

    let listed = app.send(Send::get("/api/bookmarks").cookie(&token)).await;
    let soup = listed.body["bookmarks"]
        .as_array()
        .unwrap()
        .iter()
        .find(|bookmark| bookmark["title"] == "Soup & bread")
        .unwrap();
    assert_eq!(soup["tags"], json!(["recipes", "winter"]));
    assert_eq!(soup["note"], "use less salt");
    assert_eq!(soup["created_at"], 1_600_000_002);

    let again = import_file(&app, &token, FIREFOX_FILE).await;
    assert_eq!(
        (
            again.body["added"].as_i64(),
            again.body["duplicates"].as_i64()
        ),
        (Some(0), Some(3))
    );
}

#[tokio::test]
async fn importing_needs_a_session_and_the_html_content_type() {
    let app = TestApp::running().await;
    let token = app.sign_in_as("henry").await;

    let anonymous = app
        .send(Send::new(Method::POST, "/api/import").raw("text/html", FIREFOX_FILE))
        .await;
    assert_eq!(anonymous.status, StatusCode::UNAUTHORIZED);

    for content_type in [
        "application/x-www-form-urlencoded",
        "text/plain",
        "application/json",
    ] {
        let reply = app
            .send(
                Send::new(Method::POST, "/api/import")
                    .raw(content_type, FIREFOX_FILE)
                    .cookie(&token),
            )
            .await;
        assert_eq!(
            reply.status,
            StatusCode::UNSUPPORTED_MEDIA_TYPE,
            "{content_type}"
        );
    }
    let cross_site = Send::new(Method::POST, "/api/import")
        .raw("text/html", FIREFOX_FILE)
        .cookie(&token)
        .origin("https://evil.test");
    assert_eq!(app.send(cross_site).await.status, StatusCode::FORBIDDEN);
}

#[tokio::test]
async fn an_import_lands_only_in_the_callers_account() {
    let app = TestApp::running().await;
    let henry = app.sign_in_as("henry").await;
    let mei = app.sign_in_as("mei").await;

    import_file(&app, &henry, FIREFOX_FILE).await;
    let theirs = app.send(Send::get("/api/bookmarks").cookie(&mei)).await;
    assert!(titles(&theirs).is_empty());

    // The same URLs are not duplicates for another user.
    let reply = import_file(&app, &mei, FIREFOX_FILE).await;
    assert_eq!(reply.body["added"], 2);
}

#[tokio::test]
async fn a_file_over_the_import_limit_is_refused_but_a_large_one_is_not() {
    let app = TestApp::running().await;
    let token = app.sign_in_as("henry").await;

    // Far over the 64 KiB JSON limit.
    let mut large = String::from("<DL>");
    for number in 0..3000 {
        large.push_str(&format!(
            "<DT><A HREF=\"https://a.test/{number}\">link {number}</A>\n"
        ));
    }
    large.push_str("</DL>");
    assert!(large.len() > 64 * 1024);
    let reply = import_file(&app, &token, &large).await;
    assert_eq!(reply.body["added"], 3000);

    let too_big = "x".repeat(super::MAX_IMPORT_BYTES + 1);
    let refused = import_file(&app, &token, &too_big).await;
    assert_eq!(refused.status, StatusCode::PAYLOAD_TOO_LARGE);
}

#[tokio::test]
async fn export_gives_only_the_callers_bookmarks_as_html_or_json() {
    let app = TestApp::running().await;
    let henry = app.sign_in_as("henry").await;
    let mei = app.sign_in_as("mei").await;
    create_bookmark(&app, &henry, "henry page", &["a", "b"]).await;
    create_bookmark(&app, &mei, "mei page", &[]).await;

    let html = app
        .send(Send::get("/api/export?format=html").cookie(&henry))
        .await;
    assert_eq!(html.status, StatusCode::OK);
    assert!(html.text.contains("henry page") && !html.text.contains("mei page"));
    assert!(html.text.contains("TAGS=\"a,b\""));
    let disposition = html.headers[header::CONTENT_DISPOSITION].to_str().unwrap();
    assert!(disposition.starts_with("attachment;"), "{disposition}");

    let json_file = app
        .send(Send::get("/api/export?format=json").cookie(&henry))
        .await;
    assert_eq!(json_file.body["bookmarks"][0]["title"], "henry page");
    assert_eq!(json_file.body["bookmarks"][0]["tags"], json!(["a", "b"]));
    assert_eq!(json_file.body["bookmarks"].as_array().unwrap().len(), 1);

    let bad = app
        .send(Send::get("/api/export?format=xml").cookie(&henry))
        .await;
    assert_eq!(bad.status, StatusCode::BAD_REQUEST);
    let anonymous = app.send(Send::get("/api/export?format=html")).await;
    assert_eq!(anonymous.status, StatusCode::UNAUTHORIZED);
}

#[tokio::test]
async fn an_exported_file_imports_back_into_another_account() {
    let app = TestApp::running().await;
    let henry = app.sign_in_as("henry").await;
    let mei = app.sign_in_as("mei").await;
    import_file(&app, &henry, FIREFOX_FILE).await;

    let exported = app
        .send(Send::get("/api/export?format=html").cookie(&henry))
        .await;
    let reply = import_file(&app, &mei, &exported.text).await;
    assert_eq!(
        (reply.body["added"].as_i64(), reply.body["invalid"].as_i64()),
        (Some(2), Some(0))
    );

    let original = app.send(Send::get("/api/bookmarks").cookie(&henry)).await;
    let copy = app.send(Send::get("/api/bookmarks").cookie(&mei)).await;
    let essentials = |reply: &Reply| -> Vec<Value> {
        reply.body["bookmarks"]
            .as_array()
            .unwrap()
            .iter()
            .map(|b| json!([b["url"], b["title"], b["note"], b["tags"], b["created_at"]]))
            .collect()
    };
    assert_eq!(essentials(&original), essentials(&copy));
}

// -------------------------------------------------------------- front end

async fn fetch_page(app: &TestApp, uri: &str) -> (StatusCode, HeaderMap, String) {
    let mut request = Request::builder()
        .uri(uri)
        .header(header::HOST, "bkd.test")
        .body(Body::empty())
        .unwrap();
    let client: SocketAddr = "192.0.2.1:50000".parse().unwrap();
    request.extensions_mut().insert(ConnectInfo(client));
    let response = app.router.clone().oneshot(request).await.unwrap();
    let (parts, body) = response.into_parts();
    let bytes = axum::body::to_bytes(body, usize::MAX).await.unwrap();
    let text = String::from_utf8(bytes.to_vec()).unwrap();
    (parts.status, parts.headers, text)
}

#[tokio::test]
async fn the_front_end_is_served_with_a_strict_content_security_policy() {
    let app = TestApp::running().await;
    for (uri, content_type) in [
        ("/", "text/html"),
        ("/app.css", "text/css"),
        ("/app.js", "text/javascript"),
    ] {
        let (status, headers, body) = fetch_page(&app, uri).await;
        assert_eq!(status, StatusCode::OK, "{uri}");
        let actual = headers[header::CONTENT_TYPE].to_str().unwrap();
        assert!(actual.starts_with(content_type), "{uri}: {actual}");
        assert!(!body.is_empty());
        let policy = headers[header::CONTENT_SECURITY_POLICY].to_str().unwrap();
        assert!(policy.contains("default-src 'none'"));
        assert!(!policy.contains("unsafe-inline"));
        assert_eq!(headers[header::X_CONTENT_TYPE_OPTIONS], "nosniff");
    }
}

#[tokio::test]
async fn the_page_has_no_inline_script_or_style_that_the_policy_would_block() {
    let app = TestApp::running().await;
    let (_, _, html) = fetch_page(&app, "/").await;
    assert!(html.contains(r#"<script src="/app.js"></script>"#));
    assert!(!html.contains("<style"));
    assert!(!html.contains(" style="));
    assert!(!html.contains("onclick="));
    assert_eq!(html.matches("<script").count(), 1);
}

#[tokio::test]
async fn the_brand_links_home() {
    let app = TestApp::running().await;
    let (_, _, html) = fetch_page(&app, "/").await;
    assert_eq!(
        html.matches(r#"<p class="brand"><a href="/">bkd</a></p>"#)
            .count(),
        2
    );
}

#[tokio::test]
async fn unknown_paths_are_not_found() {
    let app = TestApp::running().await;
    let (status, _, _) = fetch_page(&app, "/nope").await;
    assert_eq!(status, StatusCode::NOT_FOUND);
}

// --- Link checking ---

/// A web server on this machine with a few fixed pages, and a count of requests it got.
struct FakeSite {
    port: u16,
    requests: std::sync::Arc<std::sync::atomic::AtomicUsize>,
}

impl FakeSite {
    async fn start() -> Self {
        use std::sync::atomic::{AtomicUsize, Ordering};
        use tokio::io::{AsyncReadExt, AsyncWriteExt};

        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let port = listener.local_addr().unwrap().port();
        let requests = std::sync::Arc::new(AtomicUsize::new(0));
        let counter = requests.clone();
        tokio::spawn(async move {
            loop {
                let Ok((mut socket, _)) = listener.accept().await else {
                    return;
                };
                counter.fetch_add(1, Ordering::SeqCst);
                tokio::spawn(async move {
                    let mut buffer = [0u8; 2048];
                    let read = socket.read(&mut buffer).await.unwrap_or(0);
                    let head = String::from_utf8_lossy(&buffer[..read]).into_owned();
                    let mut words = head.split_whitespace();
                    let (method, path) = (words.next().unwrap_or(""), words.next().unwrap_or(""));
                    let reply = match (method, path) {
                        (_, "/ok") => "200 OK",
                        (_, "/missing") => "404 Not Found",
                        (_, "/forbidden") => "403 Forbidden",
                        ("HEAD", "/no-head") => "405 Method Not Allowed",
                        (_, "/no-head") => "200 OK",
                        (_, "/moved") => "301 Moved Permanently\r\nLocation: /ok",
                        (_, "/loop") => "302 Found\r\nLocation: /loop",
                        (_, "/inside") => "302 Found\r\nLocation: http://10.0.0.1/secret",
                        _ => "500 Internal Server Error",
                    };
                    let response = format!(
                        "HTTP/1.1 {reply}\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
                    );
                    let _ = socket.write_all(response.as_bytes()).await;
                });
            }
        });
        Self { port, requests }
    }

    fn url(&self, path: &str) -> String {
        format!("http://127.0.0.1:{}{path}", self.port)
    }

    fn requests(&self) -> usize {
        self.requests.load(std::sync::atomic::Ordering::SeqCst)
    }
}

async fn create_at(app: &TestApp, token: &str, url: &str, title: &str) -> i64 {
    let reply = app
        .send(Send::post("/api/bookmarks", bookmark(url, title, &[])).cookie(token))
        .await;
    assert_eq!(reply.status, StatusCode::CREATED);
    reply.body["id"].as_i64().unwrap()
}

async fn check_links(app: &TestApp, token: &str, ids: &[i64]) -> Reply {
    let request = Send::post("/api/bookmarks/check", json!({ "ids": ids })).cookie(token);
    app.send(request).await
}

/// The `data:` of every event with this name in a server-sent event stream.
fn events_named(reply: &Reply, name: &str) -> Vec<Value> {
    reply
        .text
        .split("\n\n")
        .filter(|event| event.lines().any(|line| line == format!("event: {name}")))
        .filter_map(|event| event.lines().find_map(|line| line.strip_prefix("data: ")))
        .map(|data| serde_json::from_str(data).unwrap())
        .collect()
}

fn link_of(reply: &Reply, id: i64) -> (String, String) {
    let results = events_named(reply, "result");
    let found = results
        .iter()
        .find(|result| result["id"] == id)
        .unwrap_or_else(|| panic!("no result for {id}"));
    (
        found["link"]["status"].as_str().unwrap().to_owned(),
        found["link"]["detail"].as_str().unwrap().to_owned(),
    )
}

#[tokio::test]
async fn checking_links_sorts_them_into_ok_redirected_broken_and_unknown() {
    let app = TestApp::running().await;
    let token = app.sign_in_as("henry").await;
    let site = FakeSite::start().await;
    let mut ids = Vec::new();
    for path in [
        "/ok",
        "/no-head",
        "/moved",
        "/missing",
        "/forbidden",
        "/loop",
        "/inside",
    ] {
        ids.push(create_at(&app, &token, &site.url(path), path).await);
    }

    let reply = check_links(&app, &token, &ids).await;
    assert_eq!(reply.status, StatusCode::OK);
    assert_eq!(reply.headers[header::CONTENT_TYPE], "text/event-stream");
    assert_eq!(reply.headers["x-accel-buffering"], "no");
    // One event per link, then a single `done` that ends the stream.
    assert_eq!(events_named(&reply, "result").len(), ids.len());
    assert_eq!(events_named(&reply, "done").len(), 1);
    assert!(reply.text.trim_end().ends_with("data: {}"));
    let status = |index: usize| link_of(&reply, ids[index]);
    assert_eq!(status(0), ("ok".to_owned(), String::new()));
    // The server refuses HEAD but serves GET, so the link is fine.
    assert_eq!(status(1), ("ok".to_owned(), String::new()));
    assert_eq!(status(2), ("redirected".to_owned(), site.url("/ok")));
    assert_eq!(status(3), ("broken".to_owned(), "HTTP 404".to_owned()));
    assert_eq!(status(4), ("unknown".to_owned(), "HTTP 403".to_owned()));
    assert_eq!(
        status(5),
        ("broken".to_owned(), "Too many redirects".to_owned())
    );
    // A redirect into a private address is not followed.
    assert_eq!(status(6).0, "unknown");
    assert_eq!(status(6).1, "Address is not public");

    let list = app.send(Send::get("/api/bookmarks").cookie(&token)).await;
    let stored = list.body["bookmarks"]
        .as_array()
        .unwrap()
        .iter()
        .find(|bookmark| bookmark["id"] == ids[3])
        .unwrap();
    assert_eq!(stored["link"]["status"], "broken");
    assert_eq!(stored["link"]["detail"], "HTTP 404");
    assert!(stored["link"]["checked_at"].as_i64().unwrap() > 0);
}

#[tokio::test]
async fn editing_the_url_clears_the_check_but_editing_the_title_keeps_it() {
    let app = TestApp::running().await;
    let token = app.sign_in_as("henry").await;
    let site = FakeSite::start().await;
    let id = create_at(&app, &token, &site.url("/missing"), "gone").await;
    check_links(&app, &token, &[id]).await;
    let uri = format!("/api/bookmarks/{id}");

    let renamed = bookmark(&site.url("/missing"), "renamed", &[]);
    let renamed = app.send(Send::put(&uri, renamed).cookie(&token)).await;
    assert_eq!(renamed.body["link"]["status"], "broken");

    let moved = bookmark(&site.url("/ok"), "renamed", &[]);
    let moved = app.send(Send::put(&uri, moved).cookie(&token)).await;
    assert_eq!(moved.status, StatusCode::OK);
    assert!(moved.body["link"].is_null());
}

#[tokio::test]
async fn a_host_that_refuses_connections_is_broken() {
    let app = TestApp::running().await;
    let token = app.sign_in_as("henry").await;
    // Bind and drop to get a port nobody listens on.
    let closed = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let port = closed.local_addr().unwrap().port();
    drop(closed);
    let id = create_at(&app, &token, &format!("http://127.0.0.1:{port}/"), "dead").await;
    let reply = check_links(&app, &token, &[id]).await;
    assert_eq!(
        link_of(&reply, id),
        ("broken".to_owned(), "Could not connect".to_owned())
    );
}

#[tokio::test]
async fn the_server_never_contacts_a_private_address_for_a_user() {
    let strict = TestApp::with_checker(None, Checker::public_only().unwrap()).await;
    let token = strict.sign_in_as("henry").await;
    let site = FakeSite::start().await;
    let by_address = create_at(&strict, &token, &site.url("/ok"), "by address").await;
    let by_name = create_at(
        &strict,
        &token,
        &format!("http://localhost:{}/ok", site.port),
        "by name",
    )
    .await;

    let reply = check_links(&strict, &token, &[by_address, by_name]).await;
    for id in [by_address, by_name] {
        let (status, detail) = link_of(&reply, id);
        assert_eq!(status, "unknown");
        assert_eq!(detail, "Address is not public");
    }
    assert_eq!(site.requests(), 0);
}

#[tokio::test]
async fn link_checks_cover_only_the_callers_own_bookmarks_and_reject_bad_requests() {
    let app = TestApp::running().await;
    let henry = app.sign_in_as("henry").await;
    let mei = app.sign_in_as("mei").await;
    let site = FakeSite::start().await;
    let mine = create_at(&app, &henry, &site.url("/ok"), "mine").await;
    let theirs = create_at(&app, &mei, &site.url("/ok"), "theirs").await;

    assert_eq!(
        check_links(&app, &henry, &[mine, theirs]).await.status,
        StatusCode::NOT_FOUND
    );
    assert_eq!(
        check_links(&app, &henry, &[mine, 99999]).await.status,
        StatusCode::NOT_FOUND
    );
    assert_eq!(
        site.requests(),
        0,
        "nothing is checked when any id is refused"
    );
    let too_many: Vec<i64> = (1..=201).collect();
    for ids in [vec![], too_many] {
        assert_eq!(
            check_links(&app, &henry, &ids).await.status,
            StatusCode::BAD_REQUEST
        );
    }
    let meis = app.send(Send::get("/api/bookmarks").cookie(&mei)).await;
    assert!(meis.body["bookmarks"][0]["link"].is_null());
}

// ------------------------------------------------- sessions and confirmation

/// Ages every session's confirmation, as if its password was typed long ago.
async fn make_sessions_stale(app: &TestApp) {
    sqlx::query("UPDATE sessions SET confirmed_at = 0")
        .execute(&app.state.pool)
        .await
        .unwrap();
}

async fn confirm(app: &TestApp, token: &str, password: &str) -> Reply {
    let body = json!({"password": password});
    app.send(Send::post("/api/account/confirm", body).cookie(token))
        .await
}

#[tokio::test]
async fn an_admin_change_needs_a_recent_password_confirmation() {
    let app = TestApp::running().await;
    let (_, admin) = app.sign_in_as_admin("root").await;
    let mei = app.add_user("mei", false).await;
    let promote = |token: &str| {
        let uri = format!("/api/users/{mei}/role");
        Send::put(&uri, json!({"is_admin": true})).cookie(token)
    };

    // Signing in counts as a confirmation.
    make_sessions_stale(&app).await;
    let refused = app.send(promote(&admin)).await;
    assert_eq!(refused.status, StatusCode::FORBIDDEN);
    assert_eq!(refused.body["code"], "reauth_required");
    // Reading is not a change.
    let list = app.send(Send::get("/api/users").cookie(&admin)).await;
    assert_eq!(list.status, StatusCode::OK);

    let wrong = confirm(&app, &admin, "not the password").await;
    assert_eq!(wrong.status, StatusCode::FORBIDDEN);
    assert!(wrong.body["code"].is_null());
    assert_eq!(
        app.send(promote(&admin)).await.status,
        StatusCode::FORBIDDEN
    );

    let right = confirm(&app, &admin, PASSWORD).await;
    assert_eq!(right.status, StatusCode::NO_CONTENT);
    assert_eq!(
        app.send(promote(&admin)).await.status,
        StatusCode::NO_CONTENT
    );
}

#[tokio::test]
async fn confirming_needs_a_session_and_is_rate_limited() {
    let app = TestApp::running().await;
    let body = json!({"password": PASSWORD});
    let anonymous = app.send(Send::post("/api/account/confirm", body)).await;
    assert_eq!(anonymous.status, StatusCode::UNAUTHORIZED);

    let token = app.sign_in_as("henry").await;
    for _ in 0..super::LOGIN_ATTEMPTS_PER_WINDOW {
        let reply = confirm(&app, &token, "not the password").await;
        assert_eq!(reply.status, StatusCode::FORBIDDEN);
    }
    let blocked = confirm(&app, &token, PASSWORD).await;
    assert_eq!(blocked.status, StatusCode::TOO_MANY_REQUESTS);
}

async fn session_list(app: &TestApp, token: &str) -> Reply {
    app.send(Send::get("/api/account/sessions").cookie(token))
        .await
}

#[tokio::test]
async fn the_session_list_shows_where_each_session_started() {
    let app = TestApp::running().await;
    let first = app.sign_in_as("henry").await;
    let second = session_token(&app.login("henry", PASSWORD).await);
    let other = app.sign_in_as("mei").await;

    let list = session_list(&app, &second).await;
    assert_eq!(list.status, StatusCode::OK);
    let sessions = list.body.as_array().unwrap();
    assert_eq!(sessions.len(), 2, "only henry's sessions");
    let current = sessions.iter().filter(|s| s["is_current"] == true);
    assert_eq!(current.count(), 1);
    assert_eq!(sessions[0]["ip"], "192.0.2.1");
    assert!(sessions[0].get("token_hash").is_none());

    // Another user's session id is a 404, and nothing is ended.
    let mei_id = session_list(&app, &other).await.body[0]["id"]
        .as_str()
        .unwrap()
        .to_owned();
    let foreign = app
        .send(Send::delete(&format!("/api/account/sessions/{mei_id}")).cookie(&second))
        .await;
    assert_eq!(foreign.status, StatusCode::NOT_FOUND);
    let still = app.send(Send::get("/api/bookmarks").cookie(&other)).await;
    assert_eq!(still.status, StatusCode::OK);

    let first_id = sessions.iter().find(|s| s["is_current"] == false).unwrap()["id"]
        .as_str()
        .unwrap();
    let ended = app
        .send(Send::delete(&format!("/api/account/sessions/{first_id}")).cookie(&second))
        .await;
    assert_eq!(ended.status, StatusCode::NO_CONTENT);
    let gone = app.send(Send::get("/api/bookmarks").cookie(&first)).await;
    assert_eq!(gone.status, StatusCode::UNAUTHORIZED);
}

#[tokio::test]
async fn ending_the_other_sessions_keeps_this_one() {
    let app = TestApp::running().await;
    let first = app.sign_in_as("henry").await;
    let second = session_token(&app.login("henry", PASSWORD).await);

    let reply = app
        .send(Send::delete("/api/account/sessions").cookie(&second))
        .await;
    assert_eq!(reply.status, StatusCode::NO_CONTENT);
    let gone = app.send(Send::get("/api/bookmarks").cookie(&first)).await;
    assert_eq!(gone.status, StatusCode::UNAUTHORIZED);
    let kept = app.send(Send::get("/api/bookmarks").cookie(&second)).await;
    assert_eq!(kept.status, StatusCode::OK);
}

#[tokio::test]
async fn a_session_records_a_cut_down_user_agent() {
    let app = TestApp::running().await;
    app.add_user("henry", false).await;
    let body = json!({"username": "henry", "password": PASSWORD});
    let mut request = Request::builder()
        .method(Method::POST)
        .uri("/api/login")
        .header(header::HOST, "bkd.test")
        .header(header::CONTENT_TYPE, "application/json")
        .header(header::USER_AGENT, "A".repeat(500))
        .body(Body::from(body.to_string()))
        .unwrap();
    let client: SocketAddr = "192.0.2.1:50000".parse().unwrap();
    request.extensions_mut().insert(ConnectInfo(client));
    let response = app.router.clone().oneshot(request).await.unwrap();
    assert_eq!(response.status(), StatusCode::OK);

    let stored: String = sqlx::query_scalar("SELECT user_agent FROM sessions")
        .fetch_one(&app.state.pool)
        .await
        .unwrap();
    assert_eq!(stored.len(), 200);
}

// ---- forward auth ----

const PROXY_IP: &str = "10.0.0.2";

impl TestApp {
    /// A server that believes `Remote-User` from 10.0.0.0/8 only.
    async fn behind_proxy(admin_group: Option<&str>) -> Self {
        let (dir, pool) = test_pool().await;
        let forward_auth = ForwardAuth::new(ForwardAuthConfig {
            trusted_proxies: vec!["10.0.0.0/8".into()],
            user_header: "Remote-User".into(),
            groups_header: "Remote-Groups".into(),
            admin_group: admin_group.map(str::to_owned),
        })
        .unwrap();
        let state = AppState::new(Settings {
            pool,
            setup_token: None,
            secure_cookies: false,
            forward_auth: Some(forward_auth),
            link_checker: Checker::allowing_loopback().unwrap(),
        });
        Self {
            router: router(state.clone()),
            state,
            setup_display: None,
            _dir: dir,
        }
    }
}

/// A request as the proxy forwards it for `user`.
fn proxied(request: Send, user: &'static str) -> Send {
    request.ip(PROXY_IP).header("remote-user", user)
}

async fn forwarded_state(app: &TestApp, user: &'static str, groups: &'static str) -> Reply {
    let request = proxied(Send::get("/api/state"), user).header("remote-groups", groups);
    app.send(request).await
}

#[tokio::test]
async fn a_trusted_proxy_signs_a_new_user_in() {
    let app = TestApp::behind_proxy(None).await;
    app.add_user("root", true).await;

    let reply = forwarded_state(&app, "Mei", "").await;
    assert_eq!(reply.body["user"]["username"], "mei");
    assert_eq!(reply.body["user"]["forwarded"], true);
    assert_eq!(reply.body["user"]["is_admin"], false);
    assert!(reply.headers.get(header::SET_COOKIE).is_none());

    let created = app
        .send(proxied(
            Send::post(
                "/api/bookmarks",
                json!({"url": "https://a.test", "title": "A"}),
            ),
            "mei",
        ))
        .await;
    assert_eq!(created.status, StatusCode::CREATED);
    let again = forwarded_state(&app, "mei", "").await;
    assert_eq!(
        again.body["user"]["id"], reply.body["user"]["id"],
        "one account, not two"
    );
}

#[tokio::test]
async fn the_header_is_ignored_unless_it_comes_from_a_trusted_proxy() {
    let app = TestApp::behind_proxy(None).await;
    app.add_user("root", true).await;

    let direct = app
        .send(
            Send::get("/api/state")
                .ip("192.0.2.1")
                .header("remote-user", "root"),
        )
        .await;
    assert_eq!(direct.body["user"], Value::Null);
    let bookmarks = app
        .send(
            Send::get("/api/bookmarks")
                .ip("192.0.2.1")
                .header("remote-user", "root"),
        )
        .await;
    assert_eq!(bookmarks.status, StatusCode::UNAUTHORIZED);
}

#[tokio::test]
async fn a_name_bkd_cannot_hold_signs_nobody_in() {
    let app = TestApp::behind_proxy(None).await;
    app.add_user("root", true).await;
    let reply = forwarded_state(&app, "a@b.test", "").await;
    assert_eq!(reply.body["user"], Value::Null);
}

#[tokio::test]
async fn the_proxy_wins_over_a_cookie() {
    let app = TestApp::behind_proxy(None).await;
    app.add_user("root", true).await;
    let mei_cookie = app.sign_in_as("mei").await;

    let request = proxied(Send::get("/api/state"), "root").cookie(&mei_cookie);
    let reply = app.send(request).await;
    assert_eq!(reply.body["user"]["username"], "root");
}

#[tokio::test]
async fn without_the_header_the_cookie_still_works() {
    let app = TestApp::behind_proxy(None).await;
    let cookie = app.sign_in_as("mei").await;
    let reply = app
        .send(Send::get("/api/state").ip(PROXY_IP).cookie(&cookie))
        .await;
    assert_eq!(reply.body["user"]["username"], "mei");
    assert_eq!(reply.body["user"]["forwarded"], false);
}

#[tokio::test]
async fn an_account_with_a_password_is_the_same_person() {
    let app = TestApp::behind_proxy(None).await;
    let id = app.add_user("mei", true).await;
    let reply = forwarded_state(&app, "mei", "").await;
    assert_eq!(reply.body["user"]["id"], id);
    assert_eq!(
        reply.body["user"]["is_admin"], true,
        "no admin group: the role is untouched"
    );
}

#[tokio::test]
async fn the_first_forwarded_user_is_the_admin_and_the_next_is_a_member() {
    let app = TestApp::behind_proxy(None).await;
    let first = forwarded_state(&app, "mei", "").await;
    assert_eq!(first.body["user"]["is_admin"], true);
    let second = forwarded_state(&app, "henry", "").await;
    assert_eq!(second.body["user"]["is_admin"], false);
}

#[tokio::test]
async fn two_first_requests_at_once_make_one_admin() {
    let app = TestApp::behind_proxy(None).await;
    let names = ["mei", "henry", "alice", "bob", "carol", "dave"];
    let requests = names.map(|name| forwarded_state(&app, name, ""));
    futures_util::future::join_all(requests).await;
    let admins: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM users WHERE is_admin = 1")
        .fetch_one(&app.state.pool)
        .await
        .unwrap();
    assert_eq!(admins, 1);
}

#[tokio::test]
async fn the_first_forwarded_user_closes_setup() {
    let (token, _display) = SetupToken::generate();
    let mut app = TestApp::behind_proxy(None).await;
    app.state = {
        // Same server, but still waiting for setup.
        let (dir, pool) = test_pool().await;
        let forward_auth = ForwardAuth::new(ForwardAuthConfig {
            trusted_proxies: vec!["10.0.0.0/8".into()],
            user_header: "Remote-User".into(),
            groups_header: "Remote-Groups".into(),
            admin_group: None,
        })
        .unwrap();
        app._dir = dir;
        AppState::new(Settings {
            pool,
            setup_token: Some(token),
            secure_cookies: false,
            forward_auth: Some(forward_auth),
            link_checker: Checker::allowing_loopback().unwrap(),
        })
    };
    app.router = router(app.state.clone());

    let before = app.send(Send::get("/api/state")).await;
    assert_eq!(before.body["setup_required"], true);
    forwarded_state(&app, "mei", "").await;
    let after = app.send(Send::get("/api/state")).await;
    assert_eq!(after.body["setup_required"], false);
}

#[tokio::test]
async fn the_admin_group_makes_the_first_admin() {
    let app = TestApp::behind_proxy(Some("admins")).await;
    let reply = forwarded_state(&app, "henry", "dev,admins").await;
    assert_eq!(reply.body["user"]["is_admin"], true);
}

#[tokio::test]
async fn the_admin_group_promotes_and_demotes_but_keeps_the_last_admin() {
    let app = TestApp::behind_proxy(Some("admins")).await;
    app.add_user("root", true).await;

    let promoted = forwarded_state(&app, "mei", "admins").await;
    assert_eq!(promoted.body["user"]["is_admin"], true);
    let demoted = forwarded_state(&app, "mei", "dev").await;
    assert_eq!(demoted.body["user"]["is_admin"], false);

    // root is the only admin left: leaving the group must not lock everyone out.
    let kept = forwarded_state(&app, "root", "dev").await;
    assert_eq!(kept.body["user"]["is_admin"], true);
}

#[tokio::test]
async fn a_forwarded_admin_needs_no_password_confirmation() {
    let app = TestApp::behind_proxy(Some("admins")).await;
    let mei = app.add_user("mei", false).await;
    forwarded_state(&app, "henry", "admins").await;

    let request = proxied(
        Send::put(
            &format!("/api/users/{mei}/disabled"),
            json!({"is_disabled": true}),
        ),
        "henry",
    )
    .header("remote-groups", "admins");
    let reply = app.send(request).await;
    assert_eq!(reply.status, StatusCode::NO_CONTENT);
}

#[tokio::test]
async fn a_disabled_account_gets_no_access_through_the_proxy() {
    let app = TestApp::behind_proxy(None).await;
    app.add_user("root", true).await;
    let mei = app.add_user("mei", false).await;
    sqlx::query("UPDATE users SET disabled_at = 1 WHERE id = ?1")
        .bind(mei)
        .execute(&app.state.pool)
        .await
        .unwrap();
    let reply = forwarded_state(&app, "mei", "").await;
    assert_eq!(reply.body["user"], Value::Null);
}

#[tokio::test]
async fn a_forwarded_user_has_no_password_to_change_or_confirm() {
    let app = TestApp::behind_proxy(None).await;
    app.add_user("root", true).await;
    let change = app
        .send(proxied(
            Send::put(
                "/api/account/password",
                json!({"current": "x", "new": "a-long-new-password"}),
            ),
            "mei",
        ))
        .await;
    assert_eq!(change.status, StatusCode::FORBIDDEN);
    let confirm = app
        .send(proxied(
            Send::post("/api/account/confirm", json!({"password": "x"})),
            "mei",
        ))
        .await;
    assert_eq!(confirm.status, StatusCode::FORBIDDEN);
}

#[tokio::test]
async fn an_account_made_by_the_proxy_cannot_sign_in_with_a_password() {
    let app = TestApp::behind_proxy(None).await;
    app.add_user("root", true).await;
    forwarded_state(&app, "mei", "").await;
    for password in ["!", "", "anything at all"] {
        let reply = app.login("mei", password).await;
        assert_eq!(reply.status, StatusCode::UNAUTHORIZED, "{password:?}");
    }
}
