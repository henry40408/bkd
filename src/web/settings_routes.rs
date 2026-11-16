//! The caller's own preferences. Open to every signed-in user, including one the
//! reverse proxy signed in, because nothing here needs a password.

use axum::Json;
use axum::extract::State;
use serde::{Deserialize, Serialize};

use super::SharedState;
use super::error::ApiError;
use super::guard::CurrentUser;
use crate::users;

#[derive(Serialize, Deserialize)]
pub struct SettingsView {
    /// Whether the server may check this user's bookmarks in the background.
    pub link_check_enabled: bool,
}

pub async fn get(
    user: CurrentUser,
    State(state): State<SharedState>,
) -> Result<Json<SettingsView>, ApiError> {
    let link_check_enabled = users::link_check_enabled(&state.pool, user.0.user_id).await?;
    Ok(Json(SettingsView { link_check_enabled }))
}

pub async fn put(
    user: CurrentUser,
    State(state): State<SharedState>,
    Json(settings): Json<SettingsView>,
) -> Result<Json<SettingsView>, ApiError> {
    users::set_link_check_enabled(&state.pool, user.0.user_id, settings.link_check_enabled).await?;
    tracing::info!(
        user_id = user.0.user_id,
        enabled = settings.link_check_enabled,
        "background link check setting changed"
    );
    Ok(Json(settings))
}
