//! Admin-only user management. Every handler takes `AdminUser`, which re-reads the
//! role from the database, so a demoted admin loses access on the next request.

use axum::Json;
use axum::extract::{Path, State};
use axum::http::StatusCode;
use axum::response::{IntoResponse, Response};
use serde::{Deserialize, Serialize};

use super::SharedState;
use super::error::ApiError;
use super::guard::AdminUser;
use crate::auth::{password, unix_now, username};
use crate::users::{self, Change, NewAccount, UserRecord};

#[derive(Serialize)]
pub struct ManagedUser {
    id: i64,
    username: String,
    is_admin: bool,
    is_disabled: bool,
    created_at: i64,
}

impl From<UserRecord> for ManagedUser {
    fn from(user: UserRecord) -> Self {
        Self {
            id: user.id,
            username: user.username,
            is_admin: user.is_admin,
            is_disabled: user.is_disabled,
            created_at: user.created_at,
        }
    }
}

#[derive(Serialize)]
pub struct UserList {
    users: Vec<ManagedUser>,
}

pub async fn list(
    _admin: AdminUser,
    State(state): State<SharedState>,
) -> Result<Json<UserList>, ApiError> {
    let users = users::list(&state.pool).await?;
    Ok(Json(UserList {
        users: users.into_iter().map(ManagedUser::from).collect(),
    }))
}

#[derive(Deserialize)]
pub struct NewUser {
    username: String,
    password: String,
    #[serde(default)]
    is_admin: bool,
}

pub async fn create(
    admin: AdminUser,
    State(state): State<SharedState>,
    Json(request): Json<NewUser>,
) -> Result<Response, ApiError> {
    let username = username::normalize(&request.username)?;
    password::validate(&request.password)?;
    let password_hash = password::hash_async(request.password).await?;
    let account = NewAccount {
        username: &username,
        password_hash: &password_hash,
        is_admin: request.is_admin,
        now: unix_now(),
    };
    let user = match users::create(&state.pool, admin.0.user_id, &account).await? {
        Err(_) => return Err(ApiError::Forbidden("admin access required")),
        Ok(None) => return Err(ApiError::Conflict("username is already taken")),
        Ok(Some(user)) => user,
    };
    tracing::info!(admin = admin.0.user_id, user_id = user.id, "user created");
    Ok((StatusCode::CREATED, Json(ManagedUser::from(user))).into_response())
}

#[derive(Deserialize)]
pub struct RoleRequest {
    is_admin: bool,
}

pub async fn set_role(
    admin: AdminUser,
    State(state): State<SharedState>,
    Path(id): Path<i64>,
    Json(request): Json<RoleRequest>,
) -> Result<StatusCode, ApiError> {
    let change = users::set_admin(&state.pool, admin.0.user_id, id, request.is_admin).await?;
    finish(change, &admin, id, "role changed")
}

#[derive(Deserialize)]
pub struct DisabledRequest {
    is_disabled: bool,
}

pub async fn set_disabled(
    admin: AdminUser,
    State(state): State<SharedState>,
    Path(id): Path<i64>,
    Json(request): Json<DisabledRequest>,
) -> Result<StatusCode, ApiError> {
    let disabled_at = request.is_disabled.then(unix_now);
    let change = users::set_disabled(&state.pool, admin.0.user_id, id, disabled_at).await?;
    finish(change, &admin, id, "disabled flag changed")
}

#[derive(Deserialize)]
pub struct PasswordRequest {
    password: String,
}

pub async fn reset_password(
    admin: AdminUser,
    State(state): State<SharedState>,
    Path(id): Path<i64>,
    Json(request): Json<PasswordRequest>,
) -> Result<StatusCode, ApiError> {
    password::validate(&request.password)?;
    let password_hash = password::hash_async(request.password).await?;
    let change = users::reset_password(&state.pool, admin.0.user_id, id, &password_hash).await?;
    finish(change, &admin, id, "password reset")
}

pub async fn delete(
    admin: AdminUser,
    State(state): State<SharedState>,
    Path(id): Path<i64>,
) -> Result<StatusCode, ApiError> {
    let change = users::delete(&state.pool, admin.0.user_id, id).await?;
    finish(change, &admin, id, "user deleted")
}

fn finish(
    change: Change,
    admin: &AdminUser,
    user_id: i64,
    action: &'static str,
) -> Result<StatusCode, ApiError> {
    match change {
        Change::Done => {
            tracing::info!(admin = admin.0.user_id, user_id, action);
            Ok(StatusCode::NO_CONTENT)
        }
        Change::NotFound => Err(ApiError::NotFound),
        Change::CallerNotAdmin => Err(ApiError::Forbidden("admin access required")),
        Change::LastAdmin => Err(ApiError::Conflict("at least one active admin must remain")),
    }
}
