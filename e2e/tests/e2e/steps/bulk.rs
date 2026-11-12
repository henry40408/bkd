//! Select mode: ticking bookmarks and adding a tag to, removing a tag from, deleting or checking all of them at once.

use anyhow::Result;
use bkd_e2e::world::BkdWorld;
use cucumber::{then, when};
use thirtyfour::prelude::*;

const SEARCH_BAR: &str = "[role=search]";

fn checkbox(title: &str) -> String {
    format!("ul.list input[aria-label='Select {title}']")
}

#[when("I enter select mode")]
async fn enter_select_mode(world: &mut BkdWorld) -> Result<()> {
    super::click_link(world, SEARCH_BAR, "Select").await
}

#[when("I leave select mode")]
async fn leave_select_mode(world: &mut BkdWorld) -> Result<()> {
    super::click_link(world, SEARCH_BAR, "Done").await
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

#[when("I select all on this page")]
async fn select_all(world: &mut BkdWorld) -> Result<()> {
    super::click_link(world, ".bulk", "Select all on this page").await
}

#[when("I clear the selection")]
async fn clear_selection(world: &mut BkdWorld) -> Result<()> {
    super::click_link(world, ".bulk", "Clear selection").await
}

#[when(expr = "I add the tag {string} to the selection")]
async fn add_tag_to_selection(world: &mut BkdWorld, tags: String) -> Result<()> {
    submit_tags(world, "add_tags", &tags).await
}

#[when(expr = "I remove the tag {string} from the selection")]
async fn remove_tag_from_selection(world: &mut BkdWorld, tags: String) -> Result<()> {
    submit_tags(world, "remove_tags", &tags).await
}

async fn submit_tags(world: &BkdWorld, action: &str, tags: &str) -> Result<()> {
    // The script redraws the panel as ticks change, so a field found a moment ago can go stale.
    world
        .browser()?
        .eventually("the tags field and the bulk button", || async {
            world.fill(".bulk-panel input[name=tags]", tags).await?;
            press_bulk_button(world, action).await?;
            Ok(true)
        })
        .await
}

async fn press_bulk_button(world: &BkdWorld, action: &str) -> Result<()> {
    world
        .click(&format!(".bulk button[name=action][value={action}]"))
        .await
}

#[when("I start deleting the selection")]
async fn start_deleting_selection(world: &mut BkdWorld) -> Result<()> {
    press_bulk_button(world, "delete").await
}

#[when("I check the links of the selection")]
async fn check_links_of_selection(world: &mut BkdWorld) -> Result<()> {
    press_bulk_button(world, "check").await
}

#[then(expr = "the selection count says {string}")]
async fn selection_count(world: &mut BkdWorld, text: String) -> Result<()> {
    super::text_contains(world, "#bulk-count", &text).await
}

#[then("the selection count is blank")]
async fn selection_count_is_blank(world: &mut BkdWorld) -> Result<()> {
    super::eventually_text(world, "#bulk-count is blank", "#bulk-count", str::is_empty).await
}

#[then("select mode is on")]
async fn select_mode_is_on(world: &mut BkdWorld) -> Result<()> {
    super::text_contains(world, SEARCH_BAR, "Done").await
}

#[then("select mode is off")]
async fn select_mode_is_off(world: &mut BkdWorld) -> Result<()> {
    super::text_lacks(world, SEARCH_BAR, "Done").await
}

#[then(expr = "the bulk bar shows {string}")]
async fn bulk_bar_shows(world: &mut BkdWorld, text: String) -> Result<()> {
    super::text_contains(world, ".bulk", &text).await
}

#[then(expr = "the tags field holds {string}")]
async fn tags_field_holds(world: &mut BkdWorld, expected: String) -> Result<()> {
    let field = world.element(".bulk-panel input[name=tags]").await?;
    let actual = field.value().await?.unwrap_or_default();
    anyhow::ensure!(actual == expected, "the tags field holds {actual:?}");
    Ok(())
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
