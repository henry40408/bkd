//! Select mode: ticking bookmarks and adding a tag to, removing a tag from, or deleting all of them at once.

use anyhow::Result;
use bkd_e2e::world::BkdWorld;
use cucumber::{then, when};
use thirtyfour::prelude::*;

fn checkbox(title: &str) -> String {
    format!("#list input[aria-label='Select {title}']")
}

#[when("I enter select mode")]
async fn enter_select_mode(world: &mut BkdWorld) -> Result<()> {
    world.click("#select-toggle").await
}

#[when("I leave select mode")]
async fn leave_select_mode(world: &mut BkdWorld) -> Result<()> {
    world.click("#select-toggle").await
}

#[when(expr = "I tick {string}")]
async fn tick(world: &mut BkdWorld, title: String) -> Result<()> {
    world.click(&checkbox(&title)).await
}

#[when(expr = "I Shift-click {string}")]
async fn shift_click(world: &mut BkdWorld, title: String) -> Result<()> {
    let box_ = world.element(&checkbox(&title)).await?;
    world
        .driver()?
        .action_chain()
        .key_down(Key::Shift)
        .click_element(&box_)
        .key_up(Key::Shift)
        .perform()
        .await?;
    Ok(())
}

#[when("I select all shown bookmarks")]
async fn select_all(world: &mut BkdWorld) -> Result<()> {
    world.click("#bulk [data-key=select-all]").await
}

#[when(expr = "I add the tag {string} to the selection")]
async fn add_tag_to_selection(world: &mut BkdWorld, tags: String) -> Result<()> {
    submit_tags(world, "add", &tags).await
}

#[when(expr = "I remove the tag {string} from the selection")]
async fn remove_tag_from_selection(world: &mut BkdWorld, tags: String) -> Result<()> {
    submit_tags(world, "remove", &tags).await
}

async fn submit_tags(world: &BkdWorld, panel: &str, tags: &str) -> Result<()> {
    world
        .click(&format!("#bulk [data-key=open-{panel}]"))
        .await?;
    world.fill("#bulk-panel input[name=tags]", tags).await?;
    world.click("#bulk-panel button[type=submit]").await
}

#[when("I start deleting the selection")]
async fn start_deleting_selection(world: &mut BkdWorld) -> Result<()> {
    world.click("#bulk [data-key=open-delete]").await
}

#[when("I confirm deleting the selection")]
async fn confirm_deleting_selection(world: &mut BkdWorld) -> Result<()> {
    world.click("#bulk-panel button.danger").await
}

#[when("I keep the selection")]
async fn keep_the_selection(world: &mut BkdWorld) -> Result<()> {
    world.click("#bulk-panel button:not(.danger)").await
}

#[then(expr = "the selection count says {string}")]
async fn selection_count(world: &mut BkdWorld, text: String) -> Result<()> {
    super::text_contains(world, "#bulk .bulk-count", &text).await
}

#[then("select mode is on")]
async fn select_mode_is_on(world: &mut BkdWorld) -> Result<()> {
    super::text_contains(world, "#select-toggle", "Done").await
}

#[then("select mode is off")]
async fn select_mode_is_off(world: &mut BkdWorld) -> Result<()> {
    super::text_contains(world, "#select-toggle", "Select").await
}

#[then(expr = "the bulk bar shows {string}")]
async fn bulk_bar_shows(world: &mut BkdWorld, text: String) -> Result<()> {
    super::text_contains(world, "#bulk", &text).await
}

#[then(expr = "the bulk bar does not show {string}")]
async fn bulk_bar_lacks(world: &mut BkdWorld, text: String) -> Result<()> {
    super::text_lacks(world, "#bulk", &text).await
}

#[then(expr = "{string} is ticked")]
async fn is_ticked(world: &mut BkdWorld, title: String) -> Result<()> {
    ticked_is(world, &title, true).await
}

#[then(expr = "{string} is not ticked")]
async fn is_not_ticked(world: &mut BkdWorld, title: String) -> Result<()> {
    ticked_is(world, &title, false).await
}

async fn ticked_is(world: &BkdWorld, title: &str, expected: bool) -> Result<()> {
    let driver = world.driver()?;
    let selector = checkbox(title);
    world
        .browser()?
        .eventually(&format!("{title} is ticked: {expected}"), || async {
            let box_ = driver.find(By::Css(&selector)).await?;
            Ok(box_.is_selected().await? == expected)
        })
        .await
}
