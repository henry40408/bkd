//! The narrow layout: the sidebar becomes a drawer behind a menu button.

use anyhow::{Context, Result, ensure};
use bkd_e2e::browser::{DESKTOP, PHONE};
use bkd_e2e::world::BkdWorld;
use cucumber::{given, then, when};
use thirtyfour::prelude::*;

#[given("the window is phone-sized")]
async fn phone_sized(world: &mut BkdWorld) -> Result<()> {
    world.browser()?.set_viewport(PHONE).await
}

#[when("the window becomes desktop-sized")]
async fn becomes_desktop_sized(world: &mut BkdWorld) -> Result<()> {
    world.browser()?.set_viewport(DESKTOP).await
}

#[when("I open the menu")]
async fn open_menu(world: &mut BkdWorld) -> Result<()> {
    world.click("#menu-button").await
}

async fn shown_is(world: &BkdWorld, id: &str, shown: bool) -> Result<()> {
    // The whole app stays hidden until the page knows who is signed in; "hidden" must
    // not pass just because it has not started yet.
    let driver = world.driver()?;
    world
        .browser()?
        .eventually("the app has started", || async {
            Ok(driver.find(By::Id("app")).await?.is_displayed().await?)
        })
        .await?;
    world
        .browser()?
        .eventually(&format!("#{id} shown is {shown}"), || async {
            Ok(driver.find(By::Id(id)).await?.is_displayed().await? == shown)
        })
        .await
}

#[then("the sidebar is visible")]
async fn sidebar_visible(world: &mut BkdWorld) -> Result<()> {
    shown_is(world, "sidebar", true).await
}

#[then("the sidebar is hidden")]
async fn sidebar_hidden(world: &mut BkdWorld) -> Result<()> {
    shown_is(world, "sidebar", false).await
}

#[then("the menu button is visible")]
async fn menu_button_visible(world: &mut BkdWorld) -> Result<()> {
    shown_is(world, "menu-button", true).await
}

#[then("the menu button is hidden")]
async fn menu_button_hidden(world: &mut BkdWorld) -> Result<()> {
    shown_is(world, "menu-button", false).await
}

#[then("the page behind the drawer is inert")]
async fn page_inert(world: &mut BkdWorld) -> Result<()> {
    main_inert_is(world, true).await
}

#[then("the page is not inert")]
async fn page_not_inert(world: &mut BkdWorld) -> Result<()> {
    main_inert_is(world, false).await
}

async fn main_inert_is(world: &BkdWorld, inert: bool) -> Result<()> {
    let driver = world.driver()?;
    world
        .browser()?
        .eventually(&format!("the page inert is {inert}"), || async {
            let value = driver
                .execute("return document.getElementById('main').inert", vec![])
                .await?;
            Ok(value.json().as_bool() == Some(inert))
        })
        .await
}

#[then(expr = "the button {string} is visible without hovering")]
async fn button_visible(world: &mut BkdWorld, label: String) -> Result<()> {
    world
        .element(&format!("button[aria-label='{label}']"))
        .await?;
    Ok(())
}

#[then("the page does not scroll sideways")]
async fn no_sideways_scroll(world: &mut BkdWorld) -> Result<()> {
    let value = world
        .driver()?
        .execute(
            "const page = document.scrollingElement; return page.scrollWidth - page.clientWidth",
            vec![],
        )
        .await?;
    let overflow = value
        .json()
        .as_i64()
        .context("scrollWidth - clientWidth was not a number")?;
    ensure!(
        overflow <= 0,
        "the page is {overflow}px wider than the window"
    );
    Ok(())
}
