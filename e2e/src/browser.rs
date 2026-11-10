//! The browser session: a headless Chrome or Chromium, driven over `WebDriver`.
//!
//! `WebDriver::managed` downloads only the driver, never the browser, so a local
//! Chrome or Chromium must exist.

use std::time::{Duration, Instant};

use anyhow::{Context, Result, bail};
use thirtyfour::prelude::*;

use crate::api::SESSION_COOKIE;

/// How long a query waits before giving up. Generous because slow runners took
/// over 10 seconds to settle a page; only a real failure pays it in full.
pub const WAIT_TIMEOUT: Duration = Duration::from_secs(20);
const WAIT_INTERVAL: Duration = Duration::from_millis(100);

#[derive(Debug, Clone, Copy)]
pub struct Viewport {
    pub width: u32,
    pub height: u32,
}

pub const DESKTOP: Viewport = Viewport {
    width: 1280,
    height: 800,
};
pub const PHONE: Viewport = Viewport {
    width: 390,
    height: 800,
};

#[derive(Debug)]
pub struct Browser {
    driver: WebDriver,
}

impl Browser {
    pub async fn open() -> Result<Self> {
        let mut caps = DesiredCapabilities::chrome();
        caps.add_arg("--headless=new")?;
        // Containers get a 64 MB /dev/shm by default, which Chrome outgrows.
        caps.add_arg("--disable-dev-shm-usage")?;
        // Headless Linux reports `hover: none`, which would hide the hover-only
        // actions. Blink enums: kHoverHoverType=2, kPointerFine=4.
        caps.add_arg(
            "--blink-settings=primaryHoverType=2,availableHoverTypes=2,\
             primaryPointerType=4,availablePointerTypes=4",
        )?;
        // Classic scrollbars would take width off the viewport on Linux only.
        caps.add_arg("--hide-scrollbars")?;
        let driver = WebDriver::managed(caps).await.context(
            "could not start a browser session: a local Chrome or Chromium is required \
             (`brew install --cask ungoogled-chromium`); the driver manager downloads only the driver",
        )?;
        let browser = Self { driver };
        browser.set_viewport(DESKTOP).await?;
        Ok(browser)
    }

    /// Downloads the driver once before parallel sessions start, so they do not
    /// all fetch it at the same time on a cold cache.
    pub async fn prepare() -> Result<()> {
        Self::open().await?.quit().await
    }

    pub fn driver(&self) -> &WebDriver {
        &self.driver
    }

    /// Resizes the viewport exactly. `WebDriver` window sizes include the browser
    /// chrome, which would miss the layout breakpoint.
    pub async fn set_viewport(&self, viewport: Viewport) -> Result<()> {
        self.driver
            .cdp()
            .send_raw(
                "Emulation.setDeviceMetricsOverride",
                serde_json::json!({
                    "width": viewport.width,
                    "height": viewport.height,
                    "deviceScaleFactor": 1,
                    "mobile": false,
                }),
            )
            .await?;
        Ok(())
    }

    /// Signs the browser in without going through the login form.
    pub async fn adopt_session(&self, base_url: &str, token: &str) -> Result<()> {
        // A cookie can only be set for the page the browser is on.
        self.driver.goto(&format!("{base_url}/")).await?;
        let mut cookie = Cookie::new(SESSION_COOKIE, token);
        cookie.set_path("/");
        cookie.set_http_only(true);
        self.driver.add_cookie(cookie).await?;
        Ok(())
    }

    /// Polls `probe` until it holds. A stale element means the page rebuilt the
    /// region mid-poll, which is "not yet", not a failure.
    pub async fn eventually<F, Fut>(&self, what: &str, mut probe: F) -> Result<()>
    where
        F: FnMut() -> Fut,
        Fut: Future<Output = Result<bool>>,
    {
        let deadline = Instant::now() + WAIT_TIMEOUT;
        loop {
            match probe().await {
                Ok(true) => return Ok(()),
                Ok(false) => {}
                Err(error) if is_transient(&error) => {}
                Err(error) => return Err(error),
            }
            if Instant::now() >= deadline {
                bail!("{what}: still not true after {WAIT_TIMEOUT:?}");
            }
            tokio::time::sleep(WAIT_INTERVAL).await;
        }
    }

    pub async fn quit(self) -> Result<()> {
        self.driver.quit().await?;
        Ok(())
    }
}

fn is_transient(error: &anyhow::Error) -> bool {
    error
        .chain()
        .filter_map(|cause| cause.downcast_ref::<thirtyfour::error::WebDriverError>())
        .any(|error| {
            matches!(
                error.as_inner(),
                thirtyfour::error::WebDriverErrorInner::StaleElementReference(_)
                    | thirtyfour::error::WebDriverErrorInner::NoSuchElement(_)
            )
        })
}
