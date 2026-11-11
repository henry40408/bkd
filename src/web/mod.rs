//! The HTTP layer: a JSON API under `/api` and the front-end files at the root.

mod account;
mod assets;
mod bookmark_routes;
mod cookie;
mod error;
mod guard;
#[cfg(test)]
mod tests;
mod transfer_routes;
mod user_routes;

use std::net::SocketAddr;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use anyhow::Result;
use axum::extract::DefaultBodyLimit;
use axum::routing::{delete, get, post, put};
use axum::{Router, middleware};
use sqlx::SqlitePool;
use tokio::net::TcpListener;

use crate::auth::rate_limit::RateLimiter;
use crate::auth::session;
use crate::auth::setup_token::SetupToken;
use crate::linkcheck::{ActiveChecks, Checker};

// A bookmark is a few kilobytes at most.
const MAX_BODY_BYTES: usize = 64 * 1024;

// A bookmark file from a browser: tens of thousands of links.
const MAX_IMPORT_BYTES: usize = 20 * 1024 * 1024;

const LOGIN_ATTEMPTS_PER_WINDOW: u32 = 10;
const SETUP_ATTEMPTS_PER_WINDOW: u32 = 5;
const RATE_LIMIT_WINDOW: Duration = Duration::from_secs(60);
const SHUTDOWN_GRACE: Duration = Duration::from_secs(10);
const EXPIRED_SESSION_SWEEP_INTERVAL: Duration = Duration::from_secs(60 * 60);

pub type SharedState = Arc<AppState>;

pub struct AppState {
    pub pool: SqlitePool,
    pub login_limiter: RateLimiter,
    pub setup_limiter: RateLimiter,
    /// `Some` only while no user exists. Held in memory, never stored.
    setup_token: Mutex<Option<Arc<SetupToken>>>,
    pub secure_cookies: bool,
    pub link_checker: Checker,
    pub active_checks: ActiveChecks,
}

pub struct Settings {
    pub pool: SqlitePool,
    pub setup_token: Option<SetupToken>,
    /// Mark the session cookie `Secure`. Turn it on when the site is served over HTTPS.
    pub secure_cookies: bool,
    pub link_checker: Checker,
}

impl AppState {
    pub fn new(settings: Settings) -> SharedState {
        Arc::new(Self {
            pool: settings.pool,
            login_limiter: RateLimiter::new(LOGIN_ATTEMPTS_PER_WINDOW, RATE_LIMIT_WINDOW),
            setup_limiter: RateLimiter::new(SETUP_ATTEMPTS_PER_WINDOW, RATE_LIMIT_WINDOW),
            setup_token: Mutex::new(settings.setup_token.map(Arc::new)),
            secure_cookies: settings.secure_cookies,
            link_checker: settings.link_checker,
            active_checks: ActiveChecks::default(),
        })
    }

    /// A handle that stays valid after the token is cleared, so no lock is held
    /// across an `await`.
    fn setup_token(&self) -> Option<Arc<SetupToken>> {
        self.setup_token
            .lock()
            .expect("setup token lock poisoned")
            .clone()
    }

    fn clear_setup_token(&self) {
        *self.setup_token.lock().expect("setup token lock poisoned") = None;
    }
}

pub fn router(state: SharedState) -> Router {
    let api = Router::new()
        .route("/state", get(account::current_state))
        .route("/setup", post(account::setup))
        .route("/login", post(account::login))
        .route("/logout", post(account::logout))
        .route("/account/password", put(account::change_password))
        .route("/account/confirm", post(account::confirm_password))
        .route(
            "/account/sessions",
            get(account::list_sessions).delete(account::revoke_other_sessions),
        )
        .route("/account/sessions/{id}", delete(account::revoke_session))
        .route(
            "/bookmarks",
            get(bookmark_routes::list).post(bookmark_routes::create),
        )
        .route("/bookmarks/random", get(bookmark_routes::random))
        .route("/bookmarks/bulk", post(bookmark_routes::bulk))
        .route("/bookmarks/check", post(bookmark_routes::check))
        .route(
            "/bookmarks/{id}",
            put(bookmark_routes::update).delete(bookmark_routes::delete),
        )
        .route("/tags", get(bookmark_routes::tags))
        .route(
            "/import",
            post(transfer_routes::import).layer(DefaultBodyLimit::max(MAX_IMPORT_BYTES)),
        )
        .route("/export", get(transfer_routes::export))
        .route("/users", get(user_routes::list).post(user_routes::create))
        .route("/users/{id}", delete(user_routes::delete))
        .route("/users/{id}/role", put(user_routes::set_role))
        .route("/users/{id}/disabled", put(user_routes::set_disabled))
        .route("/users/{id}/password", put(user_routes::reset_password))
        .layer(middleware::from_fn(guard::same_origin_only))
        .layer(middleware::from_fn(guard::api_headers))
        .layer(DefaultBodyLimit::max(MAX_BODY_BYTES));
    Router::new()
        .route("/", get(assets::index))
        .route("/app.css", get(assets::stylesheet))
        .route("/app.js", get(assets::script))
        .nest("/api", api)
        .layer(middleware::from_fn_with_state(
            state.clone(),
            guard::security_headers,
        ))
        .with_state(state)
}

/// Serves until Ctrl-C, then lets in-flight requests finish.
pub async fn serve(state: SharedState, listener: TcpListener) -> Result<()> {
    tokio::spawn(sweep_expired_sessions(state.clone()));
    let app = router(state).into_make_service_with_connect_info::<SocketAddr>();
    let server = axum::serve(listener, app).with_graceful_shutdown(announce_shutdown());
    // A client that stalls mid-request must not keep the process alive forever.
    tokio::select! {
        result = server => result?,
        () = forced_shutdown_after_signal() => tracing::warn!("shutdown timed out, closing connections"),
    }
    Ok(())
}

/// Ctrl-C in a terminal, or SIGTERM from systemd and Docker.
async fn shutdown_signal() {
    let terminate = async {
        match tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate()) {
            Ok(mut signal) => {
                signal.recv().await;
            }
            Err(_) => std::future::pending().await,
        }
    };
    tokio::select! {
        _ = tokio::signal::ctrl_c() => {}
        () = terminate => {}
    }
}

/// Logs once; `forced_shutdown_after_signal` waits for the same signal and must stay silent.
async fn announce_shutdown() {
    shutdown_signal().await;
    tracing::info!("shutting down");
}

async fn forced_shutdown_after_signal() {
    shutdown_signal().await;
    tokio::time::sleep(SHUTDOWN_GRACE).await;
}

async fn sweep_expired_sessions(state: SharedState) {
    let mut ticker = tokio::time::interval(EXPIRED_SESSION_SWEEP_INTERVAL);
    loop {
        ticker.tick().await;
        match session::delete_expired(&state.pool, crate::auth::unix_now()).await {
            Ok(0) => {}
            Ok(count) => tracing::info!(count, "deleted expired sessions"),
            Err(error) => tracing::error!("cannot delete expired sessions: {error:#}"),
        }
    }
}
