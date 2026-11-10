//! Direct JSON API calls, to set a scenario up quickly. What a scenario is about
//! goes through the browser; what it merely needs to exist goes through here.

use anyhow::{Context, Result, ensure};
use reqwest::{Client, Response, header};
use serde_json::{Value, json};

use crate::server::Endpoints;

/// Long enough to pass the server's password rule.
pub const PASSWORD: &str = "correct-horse-battery";

pub const SESSION_COOKIE: &str = "bkd_session";

/// A signed-in API caller.
#[derive(Debug, Clone)]
pub struct Session {
    token: String,
}

impl Session {
    pub fn token(&self) -> &str {
        &self.token
    }
}

#[derive(Debug)]
pub struct Api {
    client: Client,
    base_url: String,
    setup_token: String,
}

impl Api {
    pub fn new(endpoints: &Endpoints) -> Self {
        Self {
            client: Client::new(),
            base_url: format!("{}/api", endpoints.base_url),
            setup_token: endpoints.setup_token.clone(),
        }
    }

    /// Creates the first admin, as the setup page does.
    pub async fn setup(&self, username: &str, password: &str) -> Result<Session> {
        let body = json!({ "token": self.setup_token, "username": username, "password": password });
        let response = self
            .client
            .post(self.url("/setup"))
            .json(&body)
            .send()
            .await?;
        session_from(&response)
    }

    pub async fn login(&self, username: &str, password: &str) -> Result<Session> {
        let body = json!({ "username": username, "password": password });
        let response = self
            .client
            .post(self.url("/login"))
            .json(&body)
            .send()
            .await?;
        session_from(&response)
    }

    pub async fn create_user(
        &self,
        admin: &Session,
        username: &str,
        password: &str,
        is_admin: bool,
    ) -> Result<()> {
        let body = json!({ "username": username, "password": password, "is_admin": is_admin });
        let response = self
            .send(admin, self.client.post(self.url("/users")).json(&body))
            .await?;
        ensure_success(response, "creating a user").await
    }

    pub async fn create_bookmark(&self, who: &Session, bookmark: &NewBookmark) -> Result<()> {
        let body = json!({
            "url": bookmark.url,
            "title": bookmark.title,
            "note": bookmark.note,
            "tags": bookmark.tags,
        });
        let response = self
            .send(who, self.client.post(self.url("/bookmarks")).json(&body))
            .await?;
        ensure_success(response, "creating a bookmark").await
    }

    /// The bookmarks the account sees, newest first, as the API returns them.
    pub async fn bookmarks(&self, who: &Session) -> Result<Vec<Value>> {
        let request = self.client.get(self.url("/bookmarks?limit=200"));
        let response = self.send(who, request).await?;
        ensure!(
            response.status().is_success(),
            "listing bookmarks failed with {}",
            response.status()
        );
        let body: Value = response.json().await?;
        Ok(body["bookmarks"]
            .as_array()
            .context("no bookmark list")?
            .clone())
    }

    /// The username the server still sees behind this session, or `None` once
    /// the session no longer works.
    pub async fn signed_in_user(&self, who: &Session) -> Result<Option<String>> {
        let response = self.send(who, self.client.get(self.url("/state"))).await?;
        ensure!(
            response.status().is_success(),
            "reading the state failed with {}",
            response.status()
        );
        let body: Value = response.json().await?;
        Ok(body["user"]["username"].as_str().map(str::to_owned))
    }

    async fn send(&self, who: &Session, request: reqwest::RequestBuilder) -> Result<Response> {
        let cookie = format!("{SESSION_COOKIE}={}", who.token);
        Ok(request.header(header::COOKIE, cookie).send().await?)
    }

    fn url(&self, path: &str) -> String {
        format!("{}{path}", self.base_url)
    }
}

#[derive(Debug, Clone, Default)]
pub struct NewBookmark {
    pub url: String,
    pub title: String,
    pub note: String,
    pub tags: Vec<String>,
}

fn session_from(response: &Response) -> Result<Session> {
    let status = response.status();
    let cookie = response
        .headers()
        .get_all(header::SET_COOKIE)
        .iter()
        .filter_map(|value| value.to_str().ok())
        .find_map(|value| value.strip_prefix(&format!("{SESSION_COOKIE}=")))
        .and_then(|rest| rest.split(';').next())
        .map(str::to_owned);
    ensure!(status.is_success(), "sign-in was refused with {status}");
    Ok(Session {
        token: cookie.context("the response set no session cookie")?,
    })
}

async fn ensure_success(response: Response, what: &str) -> Result<()> {
    let status = response.status();
    if status.is_success() {
        return Ok(());
    }
    let body = response.text().await.unwrap_or_default();
    anyhow::bail!("{what} failed with {status}: {body}")
}
