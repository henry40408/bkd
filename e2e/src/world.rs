//! The Cucumber world: one server, one browser and the accounts of one scenario.

use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::OnceLock;

use anyhow::{Context, Result};
use cucumber::World;
use thirtyfour::prelude::*;

use crate::api::{Api, Session};
use crate::browser::Browser;
use crate::server::Harness;

/// The server binary, built once by the runner before any scenario starts.
static BINARY: OnceLock<PathBuf> = OnceLock::new();

/// Publishes the built binary to the worlds.
///
/// # Panics
///
/// Panics if called twice.
pub fn set_binary(path: PathBuf) {
    BINARY.set(path).expect("the binary is published once");
}

#[derive(Debug, World)]
#[world(init = Self::new)]
pub struct BkdWorld {
    harness: Harness,
    api: Api,
    browser: Option<Browser>,
    /// Accounts created through the API, by username.
    sessions: HashMap<String, Session>,
    /// The account the browser is signed in as, once a step signs it in.
    pub me: Option<String>,
    /// How many bulk bookmarks exist, so each batch gets fresh titles.
    pub bulk_count: usize,
}

impl BkdWorld {
    async fn new() -> Result<Self> {
        let binary = BINARY
            .get()
            .context("the runner did not publish the binary")?;
        let harness = Harness::start(binary).await?;
        let api = Api::new(harness.endpoints());
        Ok(Self {
            harness,
            api,
            browser: None,
            sessions: HashMap::new(),
            me: None,
            bulk_count: 0,
        })
    }

    pub async fn open_browser(&mut self) -> Result<()> {
        self.browser = Some(Browser::open().await?);
        Ok(())
    }

    pub async fn close_browser(&mut self) -> Result<()> {
        if let Some(browser) = self.browser.take() {
            browser.quit().await?;
        }
        Ok(())
    }

    pub fn api(&self) -> &Api {
        &self.api
    }

    pub fn base_url(&self) -> &str {
        &self.harness.endpoints().base_url
    }

    pub fn db_path(&self) -> &std::path::Path {
        &self.harness.endpoints().db_path
    }

    pub fn setup_token(&self) -> &str {
        &self.harness.endpoints().setup_token
    }

    pub fn browser(&self) -> Result<&Browser> {
        self.browser
            .as_ref()
            .context("no browser: the `before` hook did not open one")
    }

    pub fn driver(&self) -> Result<&WebDriver> {
        Ok(self.browser()?.driver())
    }

    pub fn remember(&mut self, username: &str, session: Session) {
        self.sessions.insert(username.to_owned(), session);
    }

    pub fn session(&self, username: &str) -> Result<&Session> {
        self.sessions
            .get(username)
            .with_context(|| format!("no account {username:?}: no step created it"))
    }

    pub async fn goto(&self, path: &str) -> Result<()> {
        self.driver()?
            .goto(format!("{}{path}", self.base_url()))
            .await?;
        Ok(())
    }

    /// The first element matching `selector` that is on screen, waiting for it.
    pub async fn element(&self, selector: &str) -> Result<WebElement> {
        self.element_by(By::Css(selector)).await
    }

    pub async fn element_by(&self, by: By) -> Result<WebElement> {
        Ok(self.driver()?.query(by).and_displayed().first().await?)
    }

    /// Moves the pointer over the element, as a person reaching for its hidden actions would.
    pub async fn hover(&self, element: &WebElement) -> Result<()> {
        self.driver()?
            .action_chain()
            .move_to_element_center(element)
            .perform()
            .await?;
        Ok(())
    }

    /// The session of the account the browser is signed in as.
    pub fn my_session(&self) -> Result<&Session> {
        let me = self.me.as_deref().context("no step signed an account in")?;
        self.session(me)
    }

    /// Replaces the contents of the field matching `selector`.
    pub async fn fill(&self, selector: &str, text: &str) -> Result<()> {
        let field = self.element(selector).await?;
        field.clear().await?;
        field.send_keys(text).await?;
        Ok(())
    }

    pub async fn click(&self, selector: &str) -> Result<()> {
        self.element(selector).await?.click().await?;
        Ok(())
    }

    /// Waits until the visible text of the page contains `text`.
    pub async fn expect_text(&self, text: &str) -> Result<()> {
        let driver = self.driver()?;
        self.browser()?
            .eventually(&format!("the page shows {text:?}"), || async {
                let body = driver.find(By::Tag("body")).await?.text().await?;
                Ok(body.contains(text))
            })
            .await
    }

    /// Waits until the visible text of the page no longer contains `text`.
    pub async fn expect_no_text(&self, text: &str) -> Result<()> {
        let driver = self.driver()?;
        self.browser()?
            .eventually(&format!("the page no longer shows {text:?}"), || async {
                let body = driver.find(By::Tag("body")).await?.text().await?;
                Ok(!body.contains(text))
            })
            .await
    }
}
