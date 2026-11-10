//! Where keyboard focus goes after the page rebuilds, and keys that drive it.

use std::time::Duration;

use anyhow::{Result, bail, ensure};
use bkd_e2e::world::BkdWorld;
use cucumber::{then, when};
use thirtyfour::prelude::*;

#[when("I press Enter")]
async fn press_enter(world: &mut BkdWorld) -> Result<()> {
    press(world, Key::Enter).await
}

#[when("I press Escape")]
async fn press_escape(world: &mut BkdWorld) -> Result<()> {
    press(world, Key::Escape).await
}

async fn press(world: &BkdWorld, key: Key) -> Result<()> {
    let focused = world.driver()?.active_element().await?;
    focused.send_keys(char::from(key).to_string()).await?;
    Ok(())
}

/// Focus moves after the next paint, so it is awaited rather than read once. It is then
/// read again after a pause: the browser drops focus from a control that has just been
/// hidden only at its next frame, and that must not pass as "still focused".
#[then(expr = "the focus is on {string}")]
async fn focus_is_on(world: &mut BkdWorld, selector: String) -> Result<()> {
    let driver = world.driver()?;
    let probe = || async { focus_matches(driver, &selector).await };
    let waited = world
        .browser()?
        .eventually(&format!("the focus is on {selector}"), probe)
        .await;
    if let Err(error) = waited {
        bail!("{error}; {}", describe_focus(driver).await?);
    }
    tokio::time::sleep(Duration::from_millis(300)).await;
    ensure!(
        focus_matches(driver, &selector).await?,
        "the focus left {selector}; {}",
        describe_focus(driver).await?
    );
    Ok(())
}

async fn focus_matches(driver: &WebDriver, selector: &str) -> Result<bool> {
    let value = driver
        .execute(
            "return document.activeElement.matches(arguments[0])",
            vec![serde_json::json!(selector)],
        )
        .await?;
    Ok(value.json().as_bool() == Some(true))
}

async fn describe_focus(driver: &WebDriver) -> Result<String> {
    let held = driver
        .execute(
            "return document.activeElement.outerHTML.slice(0, 200)",
            vec![],
        )
        .await?;
    Ok(format!("the focus is on {:?}", held.json()))
}
