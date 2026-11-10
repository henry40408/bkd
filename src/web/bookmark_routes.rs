//! Routes for a signed-in user's own bookmarks and tags.

use axum::Json;
use axum::extract::{Path, Query, State};
use axum::http::StatusCode;
use serde::{Deserialize, Serialize};

use super::SharedState;
use super::error::ApiError;
use super::guard::CurrentUser;
use crate::auth::unix_now;
use crate::bookmarks::{self, Bookmark, BookmarkInput, Page, TagCount};
use crate::search::Filter;

#[derive(Deserialize)]
pub struct ListParams {
    q: Option<String>,
    tag: Option<String>,
    limit: Option<u32>,
    offset: Option<u32>,
}

#[derive(Serialize)]
pub struct BookmarkList {
    bookmarks: Vec<Bookmark>,
}

pub async fn list(
    State(state): State<SharedState>,
    CurrentUser(user): CurrentUser,
    Query(params): Query<ListParams>,
) -> Result<Json<BookmarkList>, ApiError> {
    let filter = Filter::new(params.q.as_deref(), params.tag.as_deref());
    let page = Page::new(params.limit, params.offset);
    let bookmarks = bookmarks::list(&state.pool, user.user_id, &filter, page).await?;
    Ok(Json(BookmarkList { bookmarks }))
}

pub async fn create(
    State(state): State<SharedState>,
    CurrentUser(user): CurrentUser,
    Json(input): Json<BookmarkInput>,
) -> Result<(StatusCode, Json<Bookmark>), ApiError> {
    let valid = input.validate()?;
    let created = bookmarks::create(&state.pool, user.user_id, &valid, unix_now()).await?;
    Ok((StatusCode::CREATED, Json(created)))
}

pub async fn update(
    State(state): State<SharedState>,
    CurrentUser(user): CurrentUser,
    Path(id): Path<i64>,
    Json(input): Json<BookmarkInput>,
) -> Result<Json<Bookmark>, ApiError> {
    let valid = input.validate()?;
    bookmarks::update(&state.pool, user.user_id, id, &valid, unix_now())
        .await?
        .map(Json)
        .ok_or(ApiError::NotFound)
}

pub async fn delete(
    State(state): State<SharedState>,
    CurrentUser(user): CurrentUser,
    Path(id): Path<i64>,
) -> Result<StatusCode, ApiError> {
    if bookmarks::delete(&state.pool, user.user_id, id).await? {
        Ok(StatusCode::NO_CONTENT)
    } else {
        Err(ApiError::NotFound)
    }
}

#[derive(Serialize)]
pub struct TagList {
    tags: Vec<TagCount>,
}

pub async fn tags(
    State(state): State<SharedState>,
    CurrentUser(user): CurrentUser,
) -> Result<Json<TagList>, ApiError> {
    let tags = bookmarks::tag_counts(&state.pool, user.user_id).await?;
    Ok(Json(TagList { tags }))
}
