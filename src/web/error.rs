//! One error type for every handler, so each failure maps to one status code and a
//! JSON body of the form `{"error": "..."}`.

use std::time::Duration;

use axum::Json;
use axum::http::{HeaderValue, StatusCode, header};
use axum::response::{IntoResponse, Response};
use serde::Serialize;

use crate::auth::password::PasswordError;
use crate::auth::username::UsernameError;
use crate::bookmarks::ValidationError;

#[derive(Debug)]
pub enum ApiError {
    BadRequest(String),
    /// Wrong username or password. One message for every cause, so a caller cannot
    /// tell a missing user from a wrong password or a disabled account.
    InvalidCredentials,
    /// No valid session.
    Unauthorized,
    Forbidden(&'static str),
    /// An admin change that needs the password typed again. The front end looks for the
    /// `code`, asks for the password and repeats the request.
    ReauthRequired,
    NotFound,
    Conflict(&'static str),
    UnsupportedMediaType(&'static str),
    TooManyRequests(Duration),
    Internal(anyhow::Error),
}

#[derive(Serialize)]
struct ErrorBody<'a> {
    error: &'a str,
    #[serde(skip_serializing_if = "Option::is_none")]
    code: Option<&'a str>,
}

impl ApiError {
    /// The status and the text a client sees. The page routes show the same text.
    pub fn status_and_message(&self) -> (StatusCode, &str) {
        match self {
            Self::BadRequest(message) => (StatusCode::BAD_REQUEST, message.as_str()),
            Self::InvalidCredentials => (StatusCode::UNAUTHORIZED, "invalid username or password"),
            Self::Unauthorized => (StatusCode::UNAUTHORIZED, "not signed in"),
            Self::Forbidden(message) => (StatusCode::FORBIDDEN, *message),
            Self::ReauthRequired => (StatusCode::FORBIDDEN, "confirm your password to continue"),
            Self::NotFound => (StatusCode::NOT_FOUND, "not found"),
            Self::Conflict(message) => (StatusCode::CONFLICT, *message),
            Self::UnsupportedMediaType(message) => (StatusCode::UNSUPPORTED_MEDIA_TYPE, *message),
            Self::TooManyRequests(_) => (
                StatusCode::TOO_MANY_REQUESTS,
                "too many attempts, try again later",
            ),
            Self::Internal(error) => {
                tracing::error!("internal error: {error:#}");
                (StatusCode::INTERNAL_SERVER_ERROR, "internal error")
            }
        }
    }
}

impl IntoResponse for ApiError {
    fn into_response(self) -> Response {
        let (status, message) = self.status_and_message();
        let code = matches!(self, Self::ReauthRequired).then_some("reauth_required");
        let body = ErrorBody {
            error: message,
            code,
        };
        let mut response = (status, Json(body)).into_response();
        if let Self::TooManyRequests(wait) = self {
            let seconds = wait.as_secs() + u64::from(wait.subsec_nanos() > 0);
            response
                .headers_mut()
                .insert(header::RETRY_AFTER, HeaderValue::from(seconds));
        }
        response
    }
}

impl From<anyhow::Error> for ApiError {
    fn from(error: anyhow::Error) -> Self {
        Self::Internal(error)
    }
}

impl From<sqlx::Error> for ApiError {
    fn from(error: sqlx::Error) -> Self {
        Self::Internal(error.into())
    }
}

impl From<ValidationError> for ApiError {
    fn from(error: ValidationError) -> Self {
        Self::BadRequest(error.to_string())
    }
}

impl From<UsernameError> for ApiError {
    fn from(error: UsernameError) -> Self {
        Self::BadRequest(error.to_string())
    }
}

impl From<PasswordError> for ApiError {
    fn from(error: PasswordError) -> Self {
        Self::BadRequest(error.to_string())
    }
}
