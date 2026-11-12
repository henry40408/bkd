//! The server-rendered pages. They work with no script: every action is a link or a
//! form. `/app.js` only makes them faster. The pages call the same handlers as the JSON
//! API (`/api`), so the rules live in one place.

mod account_pages;
mod auth_pages;
mod bookmark_pages;
mod layout;
mod support;
mod user_pages;
mod view;

use axum::Router;
use axum::extract::DefaultBodyLimit;
use axum::middleware;
use axum::routing::{get, post};

use super::{MAX_BODY_BYTES, SharedState};

pub fn router() -> Router<SharedState> {
    Router::new()
        .route("/", get(bookmark_pages::list))
        .route("/random", get(bookmark_pages::random))
        .route("/bookmarks", post(bookmark_pages::create))
        .route("/bookmarks/bulk", post(bookmark_pages::bulk))
        .route("/bookmarks/delete", post(bookmark_pages::delete_submit))
        .route(
            "/bookmarks/{id}/edit",
            get(bookmark_pages::edit_form).post(bookmark_pages::edit_submit),
        )
        .route("/bookmarks/{id}/delete", get(bookmark_pages::delete_form))
        .route(
            "/login",
            get(auth_pages::login_form).post(auth_pages::login_submit),
        )
        .route(
            "/setup",
            get(auth_pages::setup_form).post(auth_pages::setup_submit),
        )
        .route("/logout", post(auth_pages::logout))
        .route(
            "/confirm",
            get(auth_pages::confirm_form).post(auth_pages::confirm_submit),
        )
        .route("/users", get(user_pages::list).post(user_pages::create))
        .route("/users/{id}/role", post(user_pages::set_role))
        .route("/users/{id}/disabled", post(user_pages::set_disabled))
        .route(
            "/users/{id}/password",
            get(user_pages::password_form).post(user_pages::reset_password),
        )
        .route(
            "/users/{id}/delete",
            get(user_pages::delete_form).post(user_pages::delete_submit),
        )
        .route("/account", get(account_pages::account))
        .route("/account/password", post(account_pages::change_password))
        .route(
            "/account/sessions/{id}/revoke",
            post(account_pages::revoke_session),
        )
        .route(
            "/account/sessions/revoke-others",
            post(account_pages::revoke_other_sessions),
        )
        .route("/transfer", get(account_pages::transfer))
        .route(
            "/transfer/import",
            post(account_pages::import_submit).layer(account_pages::import_body_limit()),
        )
        .layer(middleware::from_fn(support::form_origin_required))
        .layer(middleware::from_fn(support::page_headers))
        .layer(DefaultBodyLimit::max(MAX_BODY_BYTES))
}
