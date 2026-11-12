//! The account page (password and signed-in browsers) and the import and export page.

use std::net::SocketAddr;

use axum::Json;
use axum::extract::{ConnectInfo, DefaultBodyLimit, Form, Multipart, Path, Query, State};
use axum::http::{HeaderMap, StatusCode};
use axum::response::{IntoResponse, Response};
use serde::Deserialize;

use super::support::{
    Active, Notice, Page, PageError, Section, Shell, View, form_refusal, redirect_keeping_cookie,
    redirect_with_notice, render, render_with_status,
};
use super::view::SavedTime;
use maud::{Markup, html};

use super::layout::{error_line, framed, time_tag};
use crate::auth::session::{self, SessionUser};
use crate::auth::unix_now;
use crate::import::{self, InvalidLink};
use crate::web::SharedState;
use crate::web::account::{self, ChangePasswordRequest};
use crate::web::cookie;
use crate::web::error::ApiError;
use crate::web::guard::CurrentUser;

struct SessionRow {
    id: String,
    agent: String,
    ip: String,
    last_used: SavedTime,
    is_current: bool,
}

struct AccountPage {
    shell: Shell,
    sessions: Vec<SessionRow>,
    error: Option<String>,
}

impl View for AccountPage {
    fn markup(&self) -> Markup {
        let content = html! {
            h1 { "Account" }
            h2 { "Change password" }
            p.summary {
                "Use at least 12 characters, and do not put your username in it. "
                "Changing it signs you out everywhere else."
            }
            form.stack."password-form" method="post" action="/account/password" {
                label {
                    "Current password "
                    input type="password" name="current" autocomplete="current-password" required;
                }
                label {
                    "New password "
                    input type="password" name="new" autocomplete="new-password" minlength="12" required;
                }
                label {
                    "Repeat new password "
                    input type="password" name="again" autocomplete="new-password" required;
                }
                (error_line(self.error.as_deref()))
                div { button type="submit" { "Change password" } }
            }
            h2 { "Signed-in browsers" }
            p.summary { "Every browser that is signed in to your account. End the ones you do not recognize." }
            div."session-list" {
                @for session in &self.sessions { (session_row(session)) }
            }
            @if self.sessions.len() > 1 {
                form.downloads method="post" action="/account/sessions/revoke-others" {
                    button.quiet type="submit" { "Sign out all other browsers" }
                }
            }
        };
        framed("Account · bkd", &self.shell, content)
    }
}

fn session_row(session: &SessionRow) -> Markup {
    html! {
        div."user-row" {
            span.name {
                (session.agent)
                @if session.is_current { span.here { "This browser" } }
            }
            span.meta { (session.ip) " · last used " (time_tag(&session.last_used)) }
            @if !session.is_current {
                form method="post" action={ "/account/sessions/" (session.id) "/revoke" } {
                    button.quiet.danger type="submit" aria-label={ "Sign out " (session.agent) } {
                        "Sign out"
                    }
                }
            }
        }
    }
}

impl View for TransferPage {
    fn markup(&self) -> Markup {
        let content = html! {
            h1 { "Import and export" }
            h2 { "Import" }
            p.summary {
                "Choose a bookmarks file exported from Chrome, Firefox, Safari, Pocket or Raindrop "
                "(an .html file). Folders become tags. A link you already saved is skipped."
            }
            form.stack."import-form" method="post" action="/transfer/import" enctype="multipart/form-data" {
                label {
                    "Bookmarks file "
                    input type="file" name="file" accept=".html,.htm,text/html" required;
                }
                (error_line(self.error.as_deref()))
                div { button type="submit" { "Import bookmarks" } }
            }
            @if let Some(report) = &self.report { (import_result(report)) }
            h2 { "Export" }
            p.summary {
                "Download all of your bookmarks. Use HTML to move them to a browser or another service, "
                "and JSON as a plain backup."
            }
            p.downloads {
                a.button href="/api/export?format=html" download { "Download HTML" }
                " "
                a.button href="/api/export?format=json" download { "Download JSON" }
            }
        };
        framed("Import and export · bkd", &self.shell, content)
    }
}

fn import_result(report: &ImportReport) -> Markup {
    html! {
        div."import-result" role="status" {
            p {
                "Added " (report.added) ", skipped " (report.duplicates) " you already had"
                @if report.invalid > 0 {
                    ", and ignored " (report.invalid) " that are not web links"
                }
                "."
            }
            @if !report.examples.is_empty() {
                ul {
                    @for example in &report.examples {
                        li { (example.url) " (" (example.reason) ")" }
                    }
                }
            }
        }
    }
}

async fn shell(
    state: &SharedState,
    user: &SessionUser,
    section: Section,
    notice: Option<&str>,
) -> Result<Shell, PageError> {
    let active = Active {
        section,
        tag: "",
        status: "",
        notice,
    };
    Shell::load(state, user, active).await
}

async fn account_page(
    state: &SharedState,
    user: &SessionUser,
    headers: &HeaderMap,
    notice: Option<&str>,
) -> Result<AccountPage, PageError> {
    if user.via_proxy {
        return Err(
            ApiError::Forbidden("your sign-in is managed by the single sign-on provider").into(),
        );
    }
    let token = cookie::read_token(headers, state.secure_cookies).unwrap_or_default();
    let now = unix_now();
    let sessions = session::list_for_user(&state.pool, user.user_id, token, now)
        .await?
        .into_iter()
        .map(|info| SessionRow {
            last_used: SavedTime::new(now, info.last_used_at),
            agent: if info.user_agent.is_empty() {
                "Unknown browser".to_owned()
            } else {
                info.user_agent
            },
            id: info.id,
            ip: info.ip,
            is_current: info.is_current,
        })
        .collect();
    Ok(AccountPage {
        shell: shell(state, user, Section::Account, notice).await?,
        sessions,
        error: None,
    })
}

#[derive(Deserialize, Default)]
pub struct NoticeQuery {
    notice: Option<String>,
}

pub async fn account(
    State(state): State<SharedState>,
    headers: HeaderMap,
    Page(CurrentUser(user)): Page<CurrentUser>,
    Query(query): Query<NoticeQuery>,
) -> Result<Response, PageError> {
    let page = account_page(&state, &user, &headers, query.notice.as_deref()).await?;
    Ok(render(&page)?.into_response())
}

#[derive(Deserialize)]
pub struct PasswordForm {
    current: String,
    new: String,
    again: String,
}

pub async fn change_password(
    State(state): State<SharedState>,
    ConnectInfo(client): ConnectInfo<SocketAddr>,
    headers: HeaderMap,
    Page(user): Page<CurrentUser>,
    Form(form): Form<PasswordForm>,
) -> Result<Response, PageError> {
    let me = user.0.clone();
    if form.new != form.again {
        let page = AccountPage {
            error: Some("The two new passwords are different.".to_owned()),
            ..account_page(&state, &me, &headers, None).await?
        };
        return render_with_status(StatusCode::BAD_REQUEST, &page);
    }
    let request = Json(ChangePasswordRequest {
        current: form.current,
        new: form.new,
    });
    let outcome = account::change_password(
        user,
        State(state.clone()),
        ConnectInfo(client),
        headers.clone(),
        request,
    )
    .await;
    match outcome {
        Ok(response) => Ok(redirect_keeping_cookie(
            &response,
            "/account?notice=password-changed",
        )),
        Err(error) => {
            let (status, message) = form_refusal(error)?;
            let page = AccountPage {
                error: Some(message),
                ..account_page(&state, &me, &headers, None).await?
            };
            render_with_status(status, &page)
        }
    }
}

pub async fn revoke_session(
    State(state): State<SharedState>,
    Page(user): Page<CurrentUser>,
    Path(id): Path<String>,
) -> Result<Response, PageError> {
    account::revoke_session(user, State(state), Path(id)).await?;
    Ok(redirect_with_notice("/account", Notice::SessionEnded))
}

pub async fn revoke_other_sessions(
    State(state): State<SharedState>,
    headers: HeaderMap,
    Page(user): Page<CurrentUser>,
) -> Result<Response, PageError> {
    account::revoke_other_sessions(user, State(state), headers).await?;
    Ok(redirect_with_notice("/account", Notice::OthersEnded))
}

struct ImportReport {
    added: usize,
    duplicates: usize,
    invalid: usize,
    examples: Vec<InvalidLink>,
}

struct TransferPage {
    shell: Shell,
    report: Option<ImportReport>,
    error: Option<String>,
}

async fn transfer_page(
    state: &SharedState,
    user: &SessionUser,
    outcome: (Option<ImportReport>, Option<String>),
) -> Result<TransferPage, PageError> {
    let (report, error) = outcome;
    Ok(TransferPage {
        shell: shell(state, user, Section::Transfer, None).await?,
        report,
        error,
    })
}

pub async fn transfer(
    State(state): State<SharedState>,
    Page(CurrentUser(user)): Page<CurrentUser>,
) -> Result<Response, PageError> {
    let page = transfer_page(&state, &user, (None, None)).await?;
    Ok(render(&page)?.into_response())
}

pub async fn import_submit(
    State(state): State<SharedState>,
    Page(CurrentUser(user)): Page<CurrentUser>,
    mut form: Multipart,
) -> Result<Response, PageError> {
    let Some(file) = next_file(&mut form).await? else {
        let page = transfer_page(
            &state,
            &user,
            (None, Some("Choose a file first.".to_owned())),
        )
        .await?;
        return render_with_status(StatusCode::BAD_REQUEST, &page);
    };
    let html = String::from_utf8_lossy(&file);
    let report = import::import_html(&state.pool, user.user_id, &html, unix_now()).await?;
    let report = ImportReport {
        added: report.added,
        duplicates: report.duplicates,
        invalid: report.invalid,
        examples: report.invalid_examples,
    };
    let page = transfer_page(&state, &user, (Some(report), None)).await?;
    Ok(render(&page)?.into_response())
}

async fn next_file(form: &mut Multipart) -> Result<Option<axum::body::Bytes>, PageError> {
    while let Some(field) = form
        .next_field()
        .await
        .map_err(|_| PageError::bad_request("The upload could not be read."))?
    {
        if field.name() != Some("file") {
            continue;
        }
        let bytes = field
            .bytes()
            .await
            .map_err(|_| PageError::bad_request("The upload could not be read."))?;
        return Ok((!bytes.is_empty()).then_some(bytes));
    }
    Ok(None)
}

/// The import form is the one page route that takes a large body.
pub fn import_body_limit() -> DefaultBodyLimit {
    DefaultBodyLimit::max(crate::web::MAX_IMPORT_BYTES)
}
