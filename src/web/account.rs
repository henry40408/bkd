//! Routes about who you are: the first-run setup, login, logout and the state the
//! front end reads on load.

use std::net::SocketAddr;
use std::time::Instant;

use axum::Json;
use axum::extract::{ConnectInfo, Path, State};
use axum::http::{HeaderMap, StatusCode, header};
use axum::response::{IntoResponse, Response};
use serde::{Deserialize, Serialize};

use super::SharedState;
use super::cookie;
use super::error::ApiError;
use super::guard::{CurrentUser, user_from_request};
use crate::auth::session::{self, SessionStart, SessionUser};
use crate::auth::{first_admin, password, unix_now, username};
use crate::users;

#[derive(Serialize)]
pub struct UserView {
    id: i64,
    username: String,
    is_admin: bool,
    /// Signed in by the reverse proxy: no password and no Sign out here.
    forwarded: bool,
}

impl From<SessionUser> for UserView {
    fn from(user: SessionUser) -> Self {
        Self {
            id: user.user_id,
            username: user.username,
            is_admin: user.is_admin,
            forwarded: user.via_proxy,
        }
    }
}

#[derive(Serialize)]
pub struct StateView {
    /// True until the first admin exists: the front end shows the setup form.
    setup_required: bool,
    user: Option<UserView>,
}

/// What the front end needs to pick its first screen. Open to everyone.
pub async fn current_state(
    State(state): State<SharedState>,
    ConnectInfo(client): ConnectInfo<SocketAddr>,
    headers: HeaderMap,
) -> Result<Json<StateView>, ApiError> {
    let user = user_from_request(&state, Some(client.ip()), &headers).await?;
    Ok(Json(StateView {
        setup_required: state.setup_token().is_some(),
        user: user.map(UserView::from),
    }))
}

#[derive(Deserialize)]
pub struct SetupRequest {
    pub(super) token: String,
    pub(super) username: String,
    pub(super) password: String,
}

pub async fn setup(
    State(state): State<SharedState>,
    ConnectInfo(client): ConnectInfo<SocketAddr>,
    headers: HeaderMap,
    Json(request): Json<SetupRequest>,
) -> Result<Response, ApiError> {
    let ip = client.ip();
    state
        .setup_limiter
        .try_acquire(ip, Instant::now())
        .map_err(ApiError::TooManyRequests)?;

    let Some(token) = state.setup_token() else {
        return Err(ApiError::Conflict("setup is already complete"));
    };
    if !token.matches(&request.token) {
        tracing::warn!(%ip, "setup refused: wrong setup token");
        return Err(ApiError::Forbidden("invalid setup token"));
    }
    // The caller proved they can read the server log, so a typo in the form below
    // should not cost them an attempt.
    state.setup_limiter.refund(ip);

    let username = username::normalize(&request.username)?;
    password::validate(&request.password, &username)?;
    let password_hash = password::hash_async(request.password).await?;
    let created = first_admin::create(&state.pool, &username, &password_hash, unix_now()).await?;

    // Whoever lost the race, or arrived after the winner, must not see the form again.
    state.clear_setup_token();
    let Some(user_id) = created else {
        return Err(ApiError::Conflict("setup is already complete"));
    };
    tracing::info!(%ip, user_id, "first admin created, setup token discarded");
    start_session(
        &state,
        &session_start(user_id, client, &headers),
        StatusCode::CREATED,
    )
    .await
}

#[derive(Deserialize)]
pub struct LoginRequest {
    pub(super) username: String,
    pub(super) password: String,
}

pub async fn login(
    State(state): State<SharedState>,
    ConnectInfo(client): ConnectInfo<SocketAddr>,
    headers: HeaderMap,
    Json(request): Json<LoginRequest>,
) -> Result<Response, ApiError> {
    let ip = client.ip();
    state
        .login_limiter
        .try_acquire(ip, Instant::now())
        .map_err(ApiError::TooManyRequests)?;

    let Some(verified) = authenticate(&state, request).await? else {
        tracing::warn!(%ip, "login failed");
        return Err(ApiError::InvalidCredentials);
    };
    let start = session_start(verified.id, client, &headers);
    let token = session::create_if_unchanged(&state.pool, &start, &verified.password_hash).await?;
    let Some(token) = token else {
        tracing::warn!(%ip, "login refused: the account changed while the password was checked");
        return Err(ApiError::InvalidCredentials);
    };
    state.login_limiter.refund(ip);
    Ok(session_response(&state, &token, StatusCode::OK))
}

/// Returns the account when the credentials are right and the account is active.
/// Every failure costs about the same time, so timing does not reveal which usernames exist.
async fn authenticate(
    state: &SharedState,
    request: LoginRequest,
) -> Result<Option<users::LoginRecord>, ApiError> {
    let record = match username::normalize(&request.username) {
        Ok(name) => users::find_for_login(&state.pool, &name).await?,
        Err(_) => None,
    };
    let Some(record) = record.filter(|record| !record.is_disabled) else {
        password::verify_against_dummy_async(request.password).await?;
        return Ok(None);
    };
    let correct = password::verify_async(request.password, record.password_hash.clone()).await?;
    Ok(correct.then_some(record))
}

/// Where a session starts, for the session list. A client can send any `User-Agent`, so
/// it is cut short: it is displayed, never trusted.
fn session_start(user_id: i64, client: SocketAddr, headers: &HeaderMap) -> SessionStart {
    let user_agent = headers
        .get(header::USER_AGENT)
        .and_then(|value| value.to_str().ok())
        .unwrap_or_default()
        .chars()
        .take(200)
        .collect();
    SessionStart {
        user_id,
        now: unix_now(),
        ip: client.ip().to_canonical().to_string(),
        user_agent,
    }
}

async fn start_session(
    state: &SharedState,
    start: &SessionStart,
    status: StatusCode,
) -> Result<Response, ApiError> {
    let token = session::create(&state.pool, start).await?;
    Ok(session_response(state, &token, status))
}

fn session_response(state: &SharedState, token: &str, status: StatusCode) -> Response {
    let cookie = cookie::set(token, state.secure_cookies);
    (status, [(header::SET_COOKIE, cookie)]).into_response()
}

/// A user the proxy signed in has no password here to change or confirm.
fn refuse_proxy_user(user: &SessionUser) -> Result<(), ApiError> {
    if user.via_proxy {
        return Err(ApiError::Forbidden(
            "your sign-in is managed by the single sign-on provider",
        ));
    }
    Ok(())
}

#[derive(Deserialize)]
pub struct ChangePasswordRequest {
    pub(super) current: String,
    pub(super) new: String,
}

/// Changes the caller's own password. Every session ends, this one included, and the
/// response carries a fresh cookie so the caller stays signed in on this browser only.
pub async fn change_password(
    user: CurrentUser,
    State(state): State<SharedState>,
    ConnectInfo(client): ConnectInfo<SocketAddr>,
    headers: HeaderMap,
    Json(request): Json<ChangePasswordRequest>,
) -> Result<Response, ApiError> {
    refuse_proxy_user(&user.0)?;
    let ip = client.ip();
    // A stolen session must not be a free oracle for the current password.
    state
        .login_limiter
        .try_acquire(ip, Instant::now())
        .map_err(ApiError::TooManyRequests)?;

    let user = user.0;
    password::validate(&request.new, &user.username)?;
    if request.new == request.current {
        return Err(ApiError::BadRequest(
            "new password must differ from the current one".into(),
        ));
    }
    let Some(record) = users::find_for_login(&state.pool, &user.username).await? else {
        return Err(ApiError::Unauthorized);
    };
    let stored_hash = record.password_hash;
    if !password::verify_async(request.current, stored_hash.clone()).await? {
        tracing::warn!(%ip, user_id = user.user_id, "password change refused: wrong current password");
        return Err(ApiError::Forbidden("current password is wrong"));
    }
    let new_hash = password::hash_async(request.new).await?;
    if !users::change_own_password(&state.pool, user.user_id, &stored_hash, &new_hash).await? {
        return Err(ApiError::Conflict("password changed meanwhile, try again"));
    }
    state.login_limiter.refund(ip);
    tracing::info!(user_id = user.user_id, "password changed");
    // Not `session::create`: a reset that lands after the change must not be followed by a session.
    let start = session_start(user.user_id, client, &headers);
    let token = session::create_if_unchanged(&state.pool, &start, &new_hash).await?;
    let Some(token) = token else {
        return Err(ApiError::Conflict(
            "password changed meanwhile, sign in again",
        ));
    };
    Ok(session_response(&state, &token, StatusCode::NO_CONTENT))
}

/// The caller's live sessions, so they can spot a browser they do not know.
pub async fn list_sessions(
    user: CurrentUser,
    State(state): State<SharedState>,
    headers: HeaderMap,
) -> Result<Json<Vec<session::SessionInfo>>, ApiError> {
    let token = cookie::read_token(&headers, state.secure_cookies).unwrap_or_default();
    let sessions = session::list_for_user(&state.pool, user.0.user_id, token, unix_now()).await?;
    Ok(Json(sessions))
}

/// Ends one of the caller's own sessions. Another user's id answers 404, like a missing one.
pub async fn revoke_session(
    user: CurrentUser,
    State(state): State<SharedState>,
    Path(id): Path<String>,
) -> Result<StatusCode, ApiError> {
    if !session::revoke_by_id(&state.pool, user.0.user_id, &id).await? {
        return Err(ApiError::NotFound);
    }
    tracing::info!(user_id = user.0.user_id, "session ended by its owner");
    Ok(StatusCode::NO_CONTENT)
}

pub async fn revoke_other_sessions(
    user: CurrentUser,
    State(state): State<SharedState>,
    headers: HeaderMap,
) -> Result<StatusCode, ApiError> {
    let token = cookie::read_token(&headers, state.secure_cookies).unwrap_or_default();
    let count = session::revoke_others(&state.pool, user.0.user_id, token).await?;
    tracing::info!(
        user_id = user.0.user_id,
        count,
        "other sessions ended by their owner"
    );
    Ok(StatusCode::NO_CONTENT)
}

#[derive(Deserialize)]
pub struct ConfirmRequest {
    pub(super) password: String,
}

/// Proves the person at the keyboard knows the password, for the admin changes that ask
/// for it. Shares the login rate limit: it is a password oracle for a stolen session.
pub async fn confirm_password(
    user: CurrentUser,
    State(state): State<SharedState>,
    ConnectInfo(client): ConnectInfo<SocketAddr>,
    headers: HeaderMap,
    Json(request): Json<ConfirmRequest>,
) -> Result<StatusCode, ApiError> {
    refuse_proxy_user(&user.0)?;
    let ip = client.ip();
    state
        .login_limiter
        .try_acquire(ip, Instant::now())
        .map_err(ApiError::TooManyRequests)?;

    let user = user.0;
    let Some(record) = users::find_for_login(&state.pool, &user.username).await? else {
        return Err(ApiError::Unauthorized);
    };
    if !password::verify_async(request.password, record.password_hash).await? {
        tracing::warn!(%ip, user_id = user.user_id, "confirmation refused: wrong password");
        return Err(ApiError::Forbidden("password is wrong"));
    }
    let Some(token) = cookie::read_token(&headers, state.secure_cookies) else {
        return Err(ApiError::Unauthorized);
    };
    session::confirm(&state.pool, token, unix_now()).await?;
    state.login_limiter.refund(ip);
    Ok(StatusCode::NO_CONTENT)
}

pub async fn logout(
    State(state): State<SharedState>,
    headers: HeaderMap,
) -> Result<Response, ApiError> {
    if let Some(token) = cookie::read_token(&headers, state.secure_cookies) {
        session::revoke(&state.pool, token).await?;
    }
    let cookie = cookie::clear(state.secure_cookies);
    Ok((StatusCode::NO_CONTENT, [(header::SET_COOKIE, cookie)]).into_response())
}
