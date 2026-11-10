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
use crate::auth::password;
use crate::auth::setup_token::SetupToken;
use crate::db::test_support::tempdir::TempDir;
use crate::db::test_support::test_pool;

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
}

/// A request to send. Defaults to a JSON body from 192.0.2.1 with no cookie.
struct Send {
    method: Method,
    uri: String,
    body: Option<Value>,
    cookie: Option<String>,
    ip: &'static str,
    origin: Option<&'static str>,
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
    fn cookie(mut self, token: &str) -> Self {
        self.cookie = Some(format!("bkd_session={token}"));
        self
    }
    fn ip(mut self, ip: &'static str) -> Self {
        self.ip = ip;
        self
    }
    fn origin(mut self, origin: &'static str) -> Self {
        self.origin = Some(origin);
        self
    }
}

impl TestApp {
    async fn new(setup_token: Option<SetupToken>) -> Self {
        let (dir, pool) = test_pool().await;
        let setup_display = None;
        let state = AppState::new(Settings {
            pool,
            setup_token,
            secure_cookies: false,
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
        if let Some(origin) = request.origin {
            builder = builder.header(header::ORIGIN, origin);
        }
        let body = match request.body {
            Some(value) => {
                builder = builder.header(header::CONTENT_TYPE, "application/json");
                Body::from(value.to_string())
            }
            None => Body::empty(),
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
