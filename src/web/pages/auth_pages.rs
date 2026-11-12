//! Setup, sign-in, sign-out and the password confirmation, as forms. Each one calls the
//! API handler of the same name, so the rate limits, the timing guards and the session
//! rules exist once.

use std::net::SocketAddr;

use axum::Json;
use axum::extract::{ConnectInfo, Form, Query, State};
use axum::http::HeaderMap;
use axum::response::{IntoResponse, Redirect, Response};
use serde::Deserialize;

use super::support::{
    Active, Notice, Page, PageError, Section, Shell, View, form_refusal, path_or_home,
    redirect_keeping_cookie, redirect_with_notice, render, render_with_status,
};
use maud::{Markup, html};

use super::layout::{bare, error_line, framed};
use crate::web::SharedState;
use crate::web::account::{self, ConfirmRequest, LoginRequest, SetupRequest};
use crate::web::guard::{CurrentUser, user_from_request};

const SESSION_ENDED_TEXT: &str = "Your session ended. Sign in again.";

struct LoginPage {
    error: Option<String>,
    username: String,
    /// Where to go after signing in; already checked to be a path on this site.
    next: String,
    session_ended: bool,
}

struct SetupPage {
    error: Option<String>,
    token: String,
    username: String,
}

struct ConfirmPage {
    shell: Shell,
    error: Option<String>,
}

impl View for LoginPage {
    fn markup(&self) -> Markup {
        let content = html! {
            form.card method="post" action="/login" {
                h1 { "bkd" }
                @if self.session_ended { p.notice role="status" { (SESSION_ENDED_TEXT) } }
                (error_line(self.error.as_deref()))
                input type="hidden" name="next" value=(self.next);
                label {
                    "Username "
                    input type="text" name="username" value=(self.username) autocomplete="username"
                        autocapitalize="none" spellcheck="false" required autofocus;
                }
                label {
                    "Password "
                    input type="password" name="password" autocomplete="current-password" required;
                }
                button type="submit" { "Sign in" }
            }
        };
        bare("Sign in · bkd", content)
    }
}

impl View for SetupPage {
    fn markup(&self) -> Markup {
        let content = html! {
            form.card method="post" action="/setup" {
                h1 { "Set up bkd" }
                p."ok-hint" {
                    "Create the first admin. The setup token is in the server log from when bkd started."
                }
                (error_line(self.error.as_deref()))
                label {
                    "Setup token "
                    input type="text" name="token" value=(self.token) autocomplete="off"
                        autocapitalize="characters" spellcheck="false" required autofocus;
                }
                label {
                    "Username "
                    input type="text" name="username" value=(self.username) autocomplete="username"
                        autocapitalize="none" spellcheck="false" required;
                }
                label {
                    "Password "
                    input type="password" name="password" autocomplete="new-password" minlength="12" required;
                }
                button type="submit" { "Create admin" }
            }
        };
        bare("Set up · bkd", content)
    }
}

impl View for ConfirmPage {
    fn markup(&self) -> Markup {
        let content = html! {
            h1 { "Confirm your password" }
            p.summary { "Changing accounts needs your password again. It stays confirmed for a few minutes." }
            form.stack."password-form" method="post" action="/confirm" {
                (error_line(self.error.as_deref()))
                label {
                    "Password "
                    input type="password" name="password" autocomplete="current-password" required autofocus;
                }
                div {
                    button type="submit" { "Confirm" }
                    " "
                    a."quiet-link" href="/users" { "Cancel" }
                }
            }
        };
        framed("Confirm password · bkd", &self.shell, content)
    }
}

#[derive(Deserialize, Default)]
pub struct LoginQuery {
    next: Option<String>,
    notice: Option<String>,
}

pub async fn login_form(
    State(state): State<SharedState>,
    ConnectInfo(client): ConnectInfo<SocketAddr>,
    headers: HeaderMap,
    Query(query): Query<LoginQuery>,
) -> Result<Response, PageError> {
    let next = path_or_home(query.next.as_deref());
    if user_from_request(&state, Some(client.ip()), &headers)
        .await?
        .is_some()
    {
        return Ok(Redirect::to(&next).into_response());
    }
    if state.setup_token().is_some() {
        return Ok(Redirect::to("/setup").into_response());
    }
    Ok(render(&LoginPage {
        error: None,
        username: String::new(),
        next,
        session_ended: query.notice.as_deref() == Some("session-ended"),
    })?
    .into_response())
}

#[derive(Deserialize)]
pub struct LoginForm {
    username: String,
    password: String,
    next: Option<String>,
}

pub async fn login_submit(
    State(state): State<SharedState>,
    ConnectInfo(client): ConnectInfo<SocketAddr>,
    headers: HeaderMap,
    Form(form): Form<LoginForm>,
) -> Result<Response, PageError> {
    let username = form.username.clone();
    let next = path_or_home(form.next.as_deref());
    let request = Json(LoginRequest {
        username: form.username,
        password: form.password,
    });
    match account::login(State(state), ConnectInfo(client), headers, request).await {
        Ok(response) => Ok(redirect_keeping_cookie(&response, &next)),
        Err(error) => {
            let (status, message) = form_refusal(error)?;
            render_with_status(
                status,
                &LoginPage {
                    error: Some(message),
                    username,
                    next,
                    session_ended: false,
                },
            )
        }
    }
}

pub async fn setup_form(State(state): State<SharedState>) -> Result<Response, PageError> {
    if state.setup_token().is_none() {
        return Ok(Redirect::to("/login").into_response());
    }
    Ok(render(&SetupPage {
        error: None,
        token: String::new(),
        username: String::new(),
    })?
    .into_response())
}

#[derive(Deserialize)]
pub struct SetupForm {
    token: String,
    username: String,
    password: String,
}

pub async fn setup_submit(
    State(state): State<SharedState>,
    ConnectInfo(client): ConnectInfo<SocketAddr>,
    headers: HeaderMap,
    Form(form): Form<SetupForm>,
) -> Result<Response, PageError> {
    let (token, username) = (form.token.clone(), form.username.clone());
    let request = Json(SetupRequest {
        token: form.token,
        username: form.username,
        password: form.password,
    });
    match account::setup(State(state), ConnectInfo(client), headers, request).await {
        Ok(response) => Ok(redirect_keeping_cookie(&response, "/")),
        Err(error) => {
            let (status, message) = form_refusal(error)?;
            render_with_status(
                status,
                &SetupPage {
                    error: Some(message),
                    token,
                    username,
                },
            )
        }
    }
}

pub async fn logout(
    State(state): State<SharedState>,
    headers: HeaderMap,
) -> Result<Response, PageError> {
    let response = account::logout(State(state), headers).await?;
    Ok(redirect_keeping_cookie(&response, "/login"))
}

#[derive(Deserialize, Default)]
pub struct NoticeQuery {
    pub notice: Option<String>,
}

pub async fn confirm_form(
    State(state): State<SharedState>,
    Page(CurrentUser(user)): Page<CurrentUser>,
    Query(query): Query<NoticeQuery>,
) -> Result<Response, PageError> {
    let shell = confirm_shell(&state, &user, query.notice.as_deref()).await?;
    Ok(render(&ConfirmPage { shell, error: None })?.into_response())
}

async fn confirm_shell(
    state: &SharedState,
    user: &crate::auth::session::SessionUser,
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

#[derive(Deserialize)]
pub struct ConfirmForm {
    password: String,
}

pub async fn confirm_submit(
    State(state): State<SharedState>,
    ConnectInfo(client): ConnectInfo<SocketAddr>,
    headers: HeaderMap,
    Page(user): Page<CurrentUser>,
    Form(form): Form<ConfirmForm>,
) -> Result<Response, PageError> {
    let session_user = user.0.clone();
    let request = Json(ConfirmRequest {
        password: form.password,
    });
    let outcome = account::confirm_password(
        user,
        State(state.clone()),
        ConnectInfo(client),
        headers,
        request,
    )
    .await;
    match outcome {
        Ok(_) => Ok(redirect_with_notice("/users", Notice::Confirmed)),
        Err(error) => {
            let (status, message) = form_refusal(error)?;
            let shell = confirm_shell(&state, &session_user, None).await?;
            render_with_status(
                status,
                &ConfirmPage {
                    shell,
                    error: Some(message),
                },
            )
        }
    }
}
