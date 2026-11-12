//! Routes for a signed-in user's own bookmarks and tags.

use std::convert::Infallible;
use std::time::Duration;

use axum::Json;
use axum::extract::{Path, Query, State};
use axum::http::{HeaderName, StatusCode};
use axum::response::IntoResponse;
use axum::response::sse::{Event, KeepAlive, Sse};
use futures_util::stream;
use serde::{Deserialize, Serialize};
use sqlx::SqlitePool;

use super::SharedState;
use super::error::ApiError;
use super::guard::CurrentUser;
use crate::auth::unix_now;
use crate::bookmarks::{self, Bookmark, BookmarkInput, LinkCheck, LinkCount, Page, TagCount};
use crate::bulk::{self, BulkInput, BulkOutcome};
use crate::linkcheck::{self, ActiveCheck, CheckInput, RunningCheck};
use crate::search::Filter;

#[derive(Deserialize)]
pub struct ListParams {
    q: Option<String>,
    tag: Option<String>,
    status: Option<String>,
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
    let filter = Filter::new(params.q.as_deref(), params.tag.as_deref())
        .with_status(params.status.as_deref());
    let page = Page::new(params.limit, params.offset);
    let bookmarks = bookmarks::list(&state.pool, user.user_id, &filter, page).await?;
    Ok(Json(BookmarkList { bookmarks }))
}

#[derive(Deserialize)]
pub struct RandomParams {
    q: Option<String>,
    tag: Option<String>,
    status: Option<String>,
}

pub async fn random(
    State(state): State<SharedState>,
    CurrentUser(user): CurrentUser,
    Query(params): Query<RandomParams>,
) -> Result<Json<Bookmark>, ApiError> {
    let filter = Filter::new(params.q.as_deref(), params.tag.as_deref())
        .with_status(params.status.as_deref());
    bookmarks::random(&state.pool, user.user_id, &filter)
        .await?
        .map(Json)
        .ok_or(ApiError::NotFound)
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
    links: Vec<LinkCount>,
}

pub async fn tags(
    State(state): State<SharedState>,
    CurrentUser(user): CurrentUser,
) -> Result<Json<TagList>, ApiError> {
    let tags = bookmarks::tag_counts(&state.pool, user.user_id).await?;
    let links = bookmarks::link_counts(&state.pool, user.user_id).await?;
    Ok(Json(TagList { tags, links }))
}

#[derive(Serialize)]
pub struct BulkResult {
    pub changed: usize,
}

pub async fn bulk(
    State(state): State<SharedState>,
    CurrentUser(user): CurrentUser,
    Json(input): Json<BulkInput>,
) -> Result<Json<BulkResult>, ApiError> {
    let valid = input.validate()?;
    match bulk::apply(&state.pool, user.user_id, &valid, unix_now()).await? {
        BulkOutcome::Changed(changed) => Ok(Json(BulkResult { changed })),
        BulkOutcome::NotFound => Err(ApiError::NotFound),
        BulkOutcome::TooManyTags => Err(ApiError::BadRequest(
            "a bookmark would get too many tags".to_owned(),
        )),
    }
}

// A check runs for at most about a minute.
const CHECK_RETRY_AFTER: Duration = Duration::from_secs(10);

#[derive(Serialize)]
pub struct CheckedLink {
    id: i64,
    link: LinkCheck,
}

/// Answers with a server-sent event stream: one `result` event as each link is
/// checked, then `done`. Each result is saved as it arrives, so a client that stops
/// early keeps what was already found.
pub async fn check(
    State(state): State<SharedState>,
    CurrentUser(user): CurrentUser,
    Json(input): Json<CheckInput>,
) -> Result<impl IntoResponse, ApiError> {
    let valid = input.validate()?;
    let running = state
        .active_checks
        .start(user.user_id)
        .ok_or(ApiError::TooManyRequests(CHECK_RETRY_AFTER))?;
    let targets = linkcheck::targets(&state.pool, user.user_id, &valid)
        .await?
        .ok_or(ApiError::NotFound)?;
    let progress = CheckProgress {
        checks: state.link_checker.start(targets),
        pool: state.pool.clone(),
        user_id: user.user_id,
        finished: false,
        _slot: running,
    };
    let events = stream::unfold(progress, next_check_event);
    // nginx buffers a response unless told not to, which would deliver the results in bursts.
    let headers = [(HeaderName::from_static("x-accel-buffering"), "no")];
    Ok((headers, Sse::new(events).keep_alive(KeepAlive::default())))
}

/// Everything the event stream owns. Dropping it, when the client disconnects or the
/// stream ends, stops the check and frees the user's slot.
struct CheckProgress {
    checks: RunningCheck,
    pool: SqlitePool,
    user_id: i64,
    finished: bool,
    _slot: ActiveCheck,
}

async fn next_check_event(
    mut progress: CheckProgress,
) -> Option<(Result<Event, Infallible>, CheckProgress)> {
    if progress.finished {
        return None;
    }
    // A result for a bookmark edited during the check is not stored, so it is not sent.
    loop {
        let Some(checked) = progress.checks.next().await else {
            progress.finished = true;
            return Some((Ok(Event::default().event("done").data("{}")), progress));
        };
        let stored =
            linkcheck::store(&progress.pool, progress.user_id, &[checked], unix_now()).await;
        let event = match stored {
            Ok(mut stored) => match stored.pop() {
                Some((id, link)) => result_event(&CheckedLink { id, link }),
                None => continue,
            },
            Err(error) => {
                tracing::error!("storing a link check failed: {error:#}");
                progress.finished = true;
                Event::default().event("error").data("{}")
            }
        };
        return Some((Ok(event), progress));
    }
}

fn result_event(link: &CheckedLink) -> Event {
    Event::default()
        .event("result")
        .json_data(link)
        .unwrap_or_else(|_| Event::default().event("error").data("{}"))
}
