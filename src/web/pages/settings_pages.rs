//! The settings page: preferences of the signed-in user.

use axum::Json;
use axum::extract::{Form, Query, State};
use axum::response::{IntoResponse, Response};
use maud::{Markup, html};
use serde::Deserialize;

use super::layout::framed;
use super::support::{
    Active, Notice, Page, PageError, Section, Shell, View, redirect_with_notice, render,
};
use crate::auth::session::SessionUser;
use crate::users;
use crate::web::SharedState;
use crate::web::guard::CurrentUser;
use crate::web::settings_routes::{self, SettingsView};

struct SettingsPage {
    shell: Shell,
    link_check_enabled: bool,
    /// The administrator has turned the background check on for this server.
    server_checks_links: bool,
}

impl View for SettingsPage {
    fn markup(&self) -> Markup {
        let content = html! {
            h1 { "Settings" }
            h2 { "Link check" }
            p.summary {
                "bkd can check your bookmarks in the background and show the result under each one, "
                "as if you had pressed \"Check links\". It visits each address about once a week, "
                "waits between requests to the same site, and leaves a site alone when it asks to."
            }
            @if !self.server_checks_links {
                p.summary role="status" {
                    "Background checks are turned off on this server, so this setting has no effect "
                    "until the administrator turns them on."
                }
            }
            form.stack method="post" action="/settings" {
                label {
                    input type="checkbox" name="link_check_enabled" value="on"
                        checked[self.link_check_enabled];
                    " Check my bookmarks in the background"
                }
                div { button type="submit" { "Save" } }
            }
        };
        framed("Settings · bkd", &self.shell, content)
    }
}

async fn settings_page(
    state: &SharedState,
    user: &SessionUser,
    notice: Option<&str>,
) -> Result<SettingsPage, PageError> {
    let active = Active {
        section: Section::Settings,
        tag: "",
        status: "",
        notice,
    };
    Ok(SettingsPage {
        shell: Shell::load(state, user, active).await?,
        link_check_enabled: users::link_check_enabled(&state.pool, user.user_id).await?,
        server_checks_links: state.background_link_check,
    })
}

#[derive(Deserialize, Default)]
pub struct NoticeQuery {
    notice: Option<String>,
}

pub async fn settings(
    State(state): State<SharedState>,
    Page(CurrentUser(user)): Page<CurrentUser>,
    Query(query): Query<NoticeQuery>,
) -> Result<Response, PageError> {
    let page = settings_page(&state, &user, query.notice.as_deref()).await?;
    Ok(render(&page)?.into_response())
}

#[derive(Deserialize)]
pub struct SettingsForm {
    /// A ticked checkbox sends a value; an unticked one sends nothing.
    link_check_enabled: Option<String>,
}

pub async fn save(
    State(state): State<SharedState>,
    Page(user): Page<CurrentUser>,
    Form(form): Form<SettingsForm>,
) -> Result<Response, PageError> {
    let settings = SettingsView {
        link_check_enabled: form.link_check_enabled.is_some(),
    };
    let _ = settings_routes::put(user, State(state), Json(settings)).await?;
    Ok(redirect_with_notice("/settings", Notice::SettingsSaved))
}
