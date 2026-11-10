//! Routes about who you are: the first-run setup, login, logout and the state the
//! front end reads on load.

use std::net::SocketAddr;
use std::time::Instant;

use axum::Json;
use axum::extract::{ConnectInfo, State};
use axum::http::{HeaderMap, StatusCode, header};
use axum::response::{IntoResponse, Response};
use serde::{Deserialize, Serialize};

use super::SharedState;
use super::cookie;
use super::error::ApiError;
use super::guard::user_from_headers;
use crate::auth::session::{self, SessionUser};
use crate::auth::{first_admin, password, unix_now, username};
use crate::users;

#[derive(Serialize)]
pub struct UserView {
    id: i64,
    username: String,
    is_admin: bool,
}

impl From<SessionUser> for UserView {
    fn from(user: SessionUser) -> Self {
        Self {
            id: user.user_id,
            username: user.username,
            is_admin: user.is_admin,
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
    headers: HeaderMap,
) -> Result<Json<StateView>, ApiError> {
    let user = user_from_headers(&state, &headers).await?;
    Ok(Json(StateView {
        setup_required: state.setup_token().is_some(),
        user: user.map(UserView::from),
    }))
}

#[derive(Deserialize)]
pub struct SetupRequest {
    token: String,
    username: String,
    password: String,
}

pub async fn setup(
    State(state): State<SharedState>,
    ConnectInfo(client): ConnectInfo<SocketAddr>,
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
    password::validate(&request.password)?;
    let password_hash = password::hash_async(request.password).await?;
    let created = first_admin::create(&state.pool, &username, &password_hash, unix_now()).await?;

    // Whoever lost the race, or arrived after the winner, must not see the form again.
    state.clear_setup_token();
    let Some(user_id) = created else {
        return Err(ApiError::Conflict("setup is already complete"));
    };
    tracing::info!(%ip, user_id, "first admin created, setup token discarded");
    start_session(&state, user_id, StatusCode::CREATED).await
}

#[derive(Deserialize)]
pub struct LoginRequest {
    username: String,
    password: String,
}

pub async fn login(
    State(state): State<SharedState>,
    ConnectInfo(client): ConnectInfo<SocketAddr>,
    Json(request): Json<LoginRequest>,
) -> Result<Response, ApiError> {
    let ip = client.ip();
    state
        .login_limiter
        .try_acquire(ip, Instant::now())
        .map_err(ApiError::TooManyRequests)?;

    let Some(user_id) = authenticate(&state, request).await? else {
        tracing::warn!(%ip, "login failed");
        return Err(ApiError::InvalidCredentials);
    };
    state.login_limiter.refund(ip);
    start_session(&state, user_id, StatusCode::OK).await
}

/// Returns the user id when the credentials are right and the account is active.
/// Every failure costs about the same time, so timing does not reveal which usernames exist.
async fn authenticate(state: &SharedState, request: LoginRequest) -> Result<Option<i64>, ApiError> {
    let record = match username::normalize(&request.username) {
        Ok(name) => users::find_for_login(&state.pool, &name).await?,
        Err(_) => None,
    };
    let Some(record) = record.filter(|record| !record.is_disabled) else {
        password::verify_against_dummy_async(request.password).await?;
        return Ok(None);
    };
    let correct = password::verify_async(request.password, record.password_hash).await?;
    Ok(correct.then_some(record.id))
}

async fn start_session(
    state: &SharedState,
    user_id: i64,
    status: StatusCode,
) -> Result<Response, ApiError> {
    let token = session::create(&state.pool, user_id, unix_now()).await?;
    let cookie = cookie::set(&token, state.secure_cookies);
    Ok((status, [(header::SET_COOKIE, cookie)]).into_response())
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
