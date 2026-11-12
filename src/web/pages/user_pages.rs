//! Admin pages for accounts. A change needs `ConfirmedAdmin`; when the password was not
//! typed lately the extractor sends the admin to `/confirm` first.

use axum::Json;
use axum::extract::{Form, Path, Query, State};
use axum::response::{IntoResponse, Response};
use serde::Deserialize;

use super::bookmark_pages::{ConfirmDeletePage, HiddenField};
use super::support::{
    Active, Notice, Page, PageError, Section, Shell, View, form_refusal, redirect_with_notice,
    render, render_with_status,
};
use super::view::SavedTime;
use maud::{Markup, html};

use super::layout::{error_line, framed, time_tag};
use crate::auth::session::SessionUser;
use crate::auth::unix_now;
use crate::users::{self, UserRecord};
use crate::web::SharedState;
use crate::web::guard::{AdminUser, ConfirmedAdmin};
use crate::web::user_routes::{self, DisabledRequest, NewUser, PasswordRequest, RoleRequest};

struct UserRow {
    id: i64,
    username: String,
    is_admin: bool,
    is_disabled: bool,
    created: SavedTime,
    /// Your own row has no buttons, as before: the server would allow it, but a slip
    /// there locks you out.
    can_change: bool,
}

struct UsersPage {
    shell: Shell,
    users: Vec<UserRow>,
    new_username: String,
    new_is_admin: bool,
    error: Option<String>,
}

struct PasswordPage {
    shell: Shell,
    id: i64,
    username: String,
    error: Option<String>,
}

impl View for UsersPage {
    fn markup(&self) -> Markup {
        let content = html! {
            h1 { "Users" }
            div."user-list" {
                @for user in &self.users { (user_row(user)) }
            }
            p.summary { "Deleting a user also deletes all of their bookmarks." }
            form.stack."new-user" method="post" action="/users" {
                label {
                    "Username "
                    input type="text" name="username" value=(self.new_username) autocomplete="off";
                }
                label {
                    "Temporary password "
                    input type="password" name="password" autocomplete="new-password";
                }
                label.inline {
                    input type="checkbox" name="is_admin" checked[self.new_is_admin];
                    " Admin (can manage users)"
                }
                (error_line(self.error.as_deref()))
                div { button type="submit" { "Create user" } }
            }
        };
        framed("Users · bkd", &self.shell, content)
    }
}

fn user_row(user: &UserRow) -> Markup {
    html! {
        div."user-row".off[user.is_disabled] {
            span.name { (user.username) }
            span.meta {
                @if user.is_admin { "Admin · " }
                @if user.is_disabled { "Disabled · " }
                "Created " (time_tag(&user.created))
            }
            @if user.can_change { (user_actions(user)) }
        }
    }
}

fn user_actions(user: &UserRow) -> Markup {
    html! {
        div."user-actions" {
            form method="post" action={ "/users/" (user.id) "/role" } {
                input type="hidden" name="is_admin" value=(!user.is_admin);
                @let role = if user.is_admin { "Demote" } else { "Promote" };
                button.quiet type="submit" aria-label={ (role) " " (user.username) } { (role) }
            }
            form method="post" action={ "/users/" (user.id) "/disabled" } {
                input type="hidden" name="is_disabled" value=(!user.is_disabled);
                @let state = if user.is_disabled { "Enable" } else { "Disable" };
                button.quiet type="submit" aria-label={ (state) " " (user.username) } { (state) }
            }
            a.quiet href={ "/users/" (user.id) "/password" }
                aria-label={ "Reset password of " (user.username) } { "Reset password" }
            a.quiet.danger href={ "/users/" (user.id) "/delete" }
                aria-label={ "Delete " (user.username) } { "Delete" }
        }
    }
}

impl View for PasswordPage {
    fn markup(&self) -> Markup {
        let content = html! {
            h1 { "Reset password for " (self.username) }
            p.summary { "Their other browsers are signed out when you save." }
            form.stack."password-form" method="post" action={ "/users/" (self.id) "/password" } {
                label {
                    "New password "
                    input type="password" name="password" autocomplete="new-password" minlength="12" required autofocus;
                }
                (error_line(self.error.as_deref()))
                div {
                    button type="submit" { "Reset password" }
                    " "
                    a."quiet-link" href="/users" { "Cancel" }
                }
            }
        };
        framed("Reset password · bkd", &self.shell, content)
    }
}

async fn shell(
    state: &SharedState,
    user: &SessionUser,
    notice: Option<&str>,
) -> Result<Shell, PageError> {
    let active = Active {
        section: Section::Users,
        tag: "",
        status: "",
        notice,
    };
    Shell::load(state, user, active).await
}

async fn users_page(
    state: &SharedState,
    me: &SessionUser,
    notice: Option<&str>,
) -> Result<UsersPage, PageError> {
    let now = unix_now();
    let rows = users::list(&state.pool)
        .await?
        .into_iter()
        .map(|user: UserRecord| UserRow {
            can_change: user.id != me.user_id,
            created: SavedTime::new(now, user.created_at),
            id: user.id,
            username: user.username,
            is_admin: user.is_admin,
            is_disabled: user.is_disabled,
        })
        .collect();
    Ok(UsersPage {
        shell: shell(state, me, notice).await?,
        users: rows,
        new_username: String::new(),
        new_is_admin: false,
        error: None,
    })
}

#[derive(Deserialize, Default)]
pub struct NoticeQuery {
    notice: Option<String>,
}

pub async fn list(
    State(state): State<SharedState>,
    Page(AdminUser(me)): Page<AdminUser>,
    Query(query): Query<NoticeQuery>,
) -> Result<Response, PageError> {
    let page = users_page(&state, &me, query.notice.as_deref()).await?;
    Ok(render(&page)?.into_response())
}

#[derive(Deserialize)]
pub struct NewUserForm {
    username: String,
    password: String,
    is_admin: Option<String>,
}

pub async fn create(
    State(state): State<SharedState>,
    Page(admin): Page<ConfirmedAdmin>,
    Form(form): Form<NewUserForm>,
) -> Result<Response, PageError> {
    let me = admin.0.clone();
    let (username, is_admin) = (form.username.clone(), form.is_admin.is_some());
    let request = NewUser {
        username: form.username,
        password: form.password,
        is_admin,
    };
    match user_routes::create(admin, State(state.clone()), Json(request)).await {
        Ok(_) => Ok(redirect_with_notice("/users", Notice::UserChanged)),
        Err(error) => {
            let (status, message) = form_refusal(error)?;
            let page = UsersPage {
                new_username: username,
                new_is_admin: is_admin,
                error: Some(message),
                ..users_page(&state, &me, None).await?
            };
            render_with_status(status, &page)
        }
    }
}

#[derive(Deserialize)]
pub struct RoleForm {
    is_admin: bool,
}

pub async fn set_role(
    State(state): State<SharedState>,
    Page(admin): Page<ConfirmedAdmin>,
    Path(id): Path<i64>,
    Form(form): Form<RoleForm>,
) -> Result<Response, PageError> {
    let request = Json(RoleRequest {
        is_admin: form.is_admin,
    });
    user_routes::set_role(admin, State(state), Path(id), request).await?;
    Ok(redirect_with_notice("/users", Notice::UserChanged))
}

#[derive(Deserialize)]
pub struct DisabledForm {
    is_disabled: bool,
}

pub async fn set_disabled(
    State(state): State<SharedState>,
    Page(admin): Page<ConfirmedAdmin>,
    Path(id): Path<i64>,
    Form(form): Form<DisabledForm>,
) -> Result<Response, PageError> {
    let request = Json(DisabledRequest {
        is_disabled: form.is_disabled,
    });
    user_routes::set_disabled(admin, State(state), Path(id), request).await?;
    Ok(redirect_with_notice("/users", Notice::UserChanged))
}

async fn username_or_not_found(state: &SharedState, id: i64) -> Result<String, PageError> {
    users::username_of(&state.pool, id)
        .await?
        .ok_or_else(PageError::not_found)
}

pub async fn password_form(
    State(state): State<SharedState>,
    Page(AdminUser(me)): Page<AdminUser>,
    Path(id): Path<i64>,
) -> Result<Response, PageError> {
    let page = PasswordPage {
        shell: shell(&state, &me, None).await?,
        username: username_or_not_found(&state, id).await?,
        id,
        error: None,
    };
    Ok(render(&page)?.into_response())
}

#[derive(Deserialize)]
pub struct PasswordForm {
    password: String,
}

pub async fn reset_password(
    State(state): State<SharedState>,
    Page(admin): Page<ConfirmedAdmin>,
    Path(id): Path<i64>,
    Form(form): Form<PasswordForm>,
) -> Result<Response, PageError> {
    let me = admin.0.clone();
    let request = Json(PasswordRequest {
        password: form.password,
    });
    match user_routes::reset_password(admin, State(state.clone()), Path(id), request).await {
        Ok(_) => Ok(redirect_with_notice("/users", Notice::UserChanged)),
        Err(error) => {
            let (status, message) = form_refusal(error)?;
            let page = PasswordPage {
                shell: shell(&state, &me, None).await?,
                username: username_or_not_found(&state, id).await?,
                id,
                error: Some(message),
            };
            render_with_status(status, &page)
        }
    }
}

pub async fn delete_form(
    State(state): State<SharedState>,
    Page(AdminUser(me)): Page<AdminUser>,
    Path(id): Path<i64>,
) -> Result<Response, PageError> {
    let username = username_or_not_found(&state, id).await?;
    let page = ConfirmDeletePage {
        shell: shell(&state, &me, None).await?,
        heading: format!("Delete {username}?"),
        detail: "Their bookmarks and sessions are deleted with the account.".to_owned(),
        action: format!("/users/{id}/delete"),
        hidden: Vec::<HiddenField>::new(),
        cancel_href: "/users".to_owned(),
    };
    Ok(render(&page)?.into_response())
}

pub async fn delete_submit(
    State(state): State<SharedState>,
    Page(admin): Page<ConfirmedAdmin>,
    Path(id): Path<i64>,
) -> Result<Response, PageError> {
    user_routes::delete(admin, State(state), Path(id)).await?;
    Ok(redirect_with_notice("/users", Notice::UserChanged))
}
