//! Importing and exporting a signed-in user's own bookmarks.

use axum::Json;
use axum::body::Bytes;
use axum::extract::{Query, State};
use axum::http::{HeaderMap, HeaderValue, header};
use axum::response::{IntoResponse, Response};
use serde::{Deserialize, Serialize};

use super::SharedState;
use super::error::ApiError;
use super::guard::CurrentUser;
use crate::auth::unix_now;
use crate::bookmarks::{self, Bookmark};
use crate::import::{self, Report};
use crate::netscape;

/// Only `text/html` is taken. A cross-site form cannot send that type, and a script
/// that sets it needs a CORS preflight this server never grants, so the check keeps
/// the CSRF protection the JSON-only routes get from their content type.
pub async fn import(
    State(state): State<SharedState>,
    CurrentUser(user): CurrentUser,
    headers: HeaderMap,
    body: Bytes,
) -> Result<Json<Report>, ApiError> {
    let is_html = headers
        .get(header::CONTENT_TYPE)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value.trim().to_ascii_lowercase().starts_with("text/html"));
    if !is_html {
        return Err(ApiError::UnsupportedMediaType("send the file as text/html"));
    }
    let html = String::from_utf8_lossy(&body);
    let report = import::import_html(&state.pool, user.user_id, &html, unix_now()).await?;
    Ok(Json(report))
}

#[derive(Deserialize)]
pub struct ExportParams {
    format: String,
}

#[derive(Serialize)]
struct ExportedBookmark<'a> {
    url: &'a str,
    title: &'a str,
    note: &'a str,
    tags: &'a [String],
    created_at: i64,
}

#[derive(Serialize)]
struct ExportFile<'a> {
    bookmarks: Vec<ExportedBookmark<'a>>,
}

pub async fn export(
    State(state): State<SharedState>,
    CurrentUser(user): CurrentUser,
    Query(params): Query<ExportParams>,
) -> Result<Response, ApiError> {
    let (content_type, filename) = match params.format.as_str() {
        "html" => ("text/html; charset=utf-8", "bkd-bookmarks.html"),
        "json" => ("application/json", "bkd-bookmarks.json"),
        _ => return Err(ApiError::BadRequest("format must be html or json".into())),
    };
    let bookmarks = bookmarks::all(&state.pool, user.user_id).await?;
    let body = if params.format == "html" {
        netscape::render(&bookmarks)
    } else {
        render_json(&bookmarks)?
    };
    let disposition = format!("attachment; filename=\"{filename}\"");
    let mut response = body.into_response();
    let headers = response.headers_mut();
    headers.insert(header::CONTENT_TYPE, HeaderValue::from_static(content_type));
    headers.insert(
        header::CONTENT_DISPOSITION,
        HeaderValue::from_str(&disposition).map_err(|error| ApiError::Internal(error.into()))?,
    );
    Ok(response)
}

fn render_json(bookmarks: &[Bookmark]) -> Result<String, ApiError> {
    let file = ExportFile {
        bookmarks: bookmarks
            .iter()
            .map(|bookmark| ExportedBookmark {
                url: &bookmark.url,
                title: &bookmark.title,
                note: &bookmark.note,
                tags: &bookmark.tags,
                created_at: bookmark.created_at,
            })
            .collect(),
    };
    serde_json::to_string_pretty(&file).map_err(|error| ApiError::Internal(error.into()))
}
