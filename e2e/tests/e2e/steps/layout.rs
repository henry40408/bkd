//! The page frame: the drawer on a narrow window, pages without script, and form checks.

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

/// Called before the scenario opens the page it wants to see without script.
#[given("scripts are switched off")]
async fn scripts_off(world: &mut BkdWorld) -> Result<()> {
    world.browser()?.set_scripts_enabled(false).await
}

/// The script is what makes a refusal show up as a message instead of the browser's own bubble.
#[when("the browser's own form checks are switched off")]
async fn form_checks_off(world: &mut BkdWorld) -> Result<()> {
    world
        .driver()?
        .execute(
            "for (const form of document.forms) form.noValidate = true;",
            vec![],
        )
        .await?;
    Ok(())
}

#[then(expr = "the browser refuses the field {string}")]
async fn browser_refuses_field(world: &mut BkdWorld, selector: String) -> Result<()> {
    let field = world.element(&selector).await?;
    let invalid = world
        .driver()?
        .execute(
            "return arguments[0].matches(':invalid')",
            vec![field.to_json()?],
        )
        .await?;
    ensure!(
        invalid.json().as_bool() == Some(true),
        "{selector} is not refused"
    );
    Ok(())
}

#[when("I open the menu")]
async fn open_menu(world: &mut BkdWorld) -> Result<()> {
    world.click(".menu").await
}

/// The drawer covers the left of the backdrop, so the tap lands on the page's right edge.
#[when("I tap the backdrop")]
async fn tap_backdrop(world: &mut BkdWorld) -> Result<()> {
    let backdrop = world.element(".backdrop").await?;
    let right_of_drawer = i64::from(PHONE.width / 2) - 10;
    world
        .driver()?
        .action_chain()
        .move_to_element_with_offset(&backdrop, right_of_drawer, 0)
        .click()
        .perform()
        .await?;
    Ok(())
}

async fn shown_is(world: &BkdWorld, selector: &str, shown: bool) -> Result<()> {
    let driver = world.driver()?;
    world
        .browser()?
        .eventually(&format!("{selector} shown is {shown}"), || async {
            Ok(driver.find(By::Css(selector)).await?.is_displayed().await? == shown)
        })
        .await
}

#[then("the sidebar is visible")]
async fn sidebar_visible(world: &mut BkdWorld) -> Result<()> {
    shown_is(world, "#sidebar", true).await
}

#[then("the sidebar is hidden")]
async fn sidebar_hidden(world: &mut BkdWorld) -> Result<()> {
    shown_is(world, "#sidebar", false).await
}

#[then("the menu button is visible")]
async fn menu_button_visible(world: &mut BkdWorld) -> Result<()> {
    shown_is(world, ".menu", true).await
}

#[then("the menu button is hidden")]
async fn menu_button_hidden(world: &mut BkdWorld) -> Result<()> {
    shown_is(world, ".menu", false).await
}

#[then("the backdrop covers the page")]
async fn backdrop_covers_page(world: &mut BkdWorld) -> Result<()> {
    shown_is(world, ".backdrop", true).await
}

#[then("the backdrop is hidden")]
async fn backdrop_hidden(world: &mut BkdWorld) -> Result<()> {
    shown_is(world, ".backdrop", false).await
}

#[then(expr = "the link {string} is visible without hovering")]
async fn link_visible(world: &mut BkdWorld, label: String) -> Result<()> {
    world.element(&format!("a[aria-label='{label}']")).await?;
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
