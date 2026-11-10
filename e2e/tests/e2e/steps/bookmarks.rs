//! Adding, editing, deleting, searching and paging bookmarks.

use std::time::Duration;

use anyhow::{Result, ensure};
use bkd_e2e::api::NewBookmark;
use bkd_e2e::world::BkdWorld;
use cucumber::{given, then, when};
use thirtyfour::prelude::*;

// ---------------------------------------------------------------- setup

async fn create(world: &BkdWorld, bookmark: NewBookmark) -> Result<()> {
    world
        .api()
        .create_bookmark(world.my_session()?, &bookmark)
        .await
}

#[given(expr = "I have the bookmark {string} titled {string}")]
async fn have_bookmark(world: &mut BkdWorld, url: String, title: String) -> Result<()> {
    create(
        world,
        NewBookmark {
            url,
            title,
            ..NewBookmark::default()
        },
    )
    .await
}

#[given(expr = "I have the bookmark {string} titled {string} tagged {string}")]
async fn have_tagged_bookmark(
    world: &mut BkdWorld,
    url: String,
    title: String,
    tag: String,
) -> Result<()> {
    create(
        world,
        NewBookmark {
            url,
            title,
            tags: vec![tag],
            ..NewBookmark::default()
        },
    )
    .await
}

#[given(expr = "I have the bookmark {string} titled {string} with the note {string}")]
async fn have_bookmark_with_note(
    world: &mut BkdWorld,
    url: String,
    title: String,
    note: String,
) -> Result<()> {
    create(
        world,
        NewBookmark {
            url,
            title,
            note,
            ..NewBookmark::default()
        },
    )
    .await
}

#[given(expr = "I have {int} bookmark(s)")]
#[given(expr = "I have {int} more bookmark(s)")]
async fn have_bulk_bookmarks(world: &mut BkdWorld, count: usize) -> Result<()> {
    for _ in 0..count {
        world.bulk_count += 1;
        let number = world.bulk_count;
        let bookmark = NewBookmark {
            url: format!("https://bulk.test/{number}"),
            title: format!("Bulk {number}"),
            ..NewBookmark::default()
        };
        create(world, bookmark).await?;
    }
    Ok(())
}

// ------------------------------------------------------------- the page

async fn open_add_form(world: &BkdWorld) -> Result<()> {
    let form = world.driver()?.find(By::Id("add-form")).await?;
    if !form.is_displayed().await? {
        world.click("#add summary").await?;
    }
    Ok(())
}

#[when(expr = "I add the bookmark {string} titled {string} tagged {string}")]
async fn add_bookmark(
    world: &mut BkdWorld,
    url: String,
    title: String,
    tags: String,
) -> Result<()> {
    open_add_form(world).await?;
    world.fill("#add-form input[name=url]", &url).await?;
    world.fill("#add-form input[name=title]", &title).await?;
    world.fill("#add-form input[name=tags]", &tags).await?;
    world.click("#add-form button[type=submit]").await
}

#[when(expr = "I press Enter twice to add {string} titled {string}")]
async fn submit_add_form_twice(world: &mut BkdWorld, url: String, title: String) -> Result<()> {
    open_add_form(world).await?;
    world.fill("#add-form input[name=url]", &url).await?;
    world.fill("#add-form input[name=title]", &title).await?;
    super::press_enter_twice(world, "#add-form input[name=title]").await
}

#[when(expr = "I edit {string} to the title {string}")]
async fn edit_bookmark(world: &mut BkdWorld, old_title: String, new_title: String) -> Result<()> {
    click_row_action(world, &old_title, "Edit").await?;
    world
        .fill("#list form input[name=title]", &new_title)
        .await?;
    world.click("#list form button[type=submit]").await
}

#[when(expr = "I start deleting {string}")]
async fn start_deleting(world: &mut BkdWorld, title: String) -> Result<()> {
    click_row_action(world, &title, "Delete").await
}

/// The Edit and Delete buttons show only while the pointer is over the row.
async fn click_row_action(world: &BkdWorld, title: &str, action: &str) -> Result<()> {
    let row = By::XPath(format!(
        "//ul[@id='list']/li[.//a[@class='title' and text()='{title}']]"
    ));
    let row = world.element_by(row).await?;
    world.hover(&row).await?;
    world
        .click(&format!("button[aria-label='{action} {title}']"))
        .await
}

#[when("I keep it")]
async fn keep_it(world: &mut BkdWorld) -> Result<()> {
    world.click(".confirm button:not(.danger)").await
}

#[when("I confirm the deletion")]
async fn confirm_deletion(world: &mut BkdWorld) -> Result<()> {
    world.click(".confirm button.danger").await
}

#[when(expr = "I search for {string}")]
async fn search_for(world: &mut BkdWorld, query: String) -> Result<()> {
    world.fill("#search", &query).await
}

#[when(expr = "I choose the tag {string}")]
async fn choose_tag(world: &mut BkdWorld, tag: String) -> Result<()> {
    let link = By::XPath(format!(
        "//ul[@id='tag-list']//a[starts-with(normalize-space(.), '{tag}')]"
    ));
    world.element_by(link).await?.click().await?;
    Ok(())
}

#[when("I choose Show more")]
async fn choose_show_more(world: &mut BkdWorld) -> Result<()> {
    world.click("#more").await
}

// ---------------------------------------------------------- assertions

#[then(expr = "the list shows {string}")]
async fn list_shows(world: &mut BkdWorld, text: String) -> Result<()> {
    super::text_contains(world, "#list", &text).await
}

#[then(expr = "the list does not show {string}")]
async fn list_does_not_show(world: &mut BkdWorld, text: String) -> Result<()> {
    super::text_lacks(world, "#list", &text).await
}

#[then(expr = "the link {string} points to {string}")]
async fn link_points_to(world: &mut BkdWorld, title: String, href: String) -> Result<()> {
    let link = By::XPath(format!(
        "//ul[@id='list']//a[@class='title' and text()='{title}']"
    ));
    let actual = world.element_by(link).await?.attr("href").await?;
    ensure!(
        actual.as_deref() == Some(href.as_str()),
        "the link goes to {actual:?}"
    );
    Ok(())
}

#[then(expr = "the sidebar lists the tag {string}")]
async fn sidebar_lists_tag(world: &mut BkdWorld, tag: String) -> Result<()> {
    super::text_contains(world, "#tag-list", &tag).await
}

#[then(expr = "the account has exactly {int} bookmark(s)")]
async fn account_has_bookmarks(world: &mut BkdWorld, expected: usize) -> Result<()> {
    // A repeated request, if the page wrongly sent one, is still in flight here.
    tokio::time::sleep(Duration::from_millis(500)).await;
    let bookmarks = world.api().bookmarks(world.my_session()?).await?;
    ensure!(
        bookmarks.len() == expected,
        "the account has {} bookmarks",
        bookmarks.len()
    );
    Ok(())
}

#[then(expr = "{string} is highlighted")]
async fn is_highlighted(world: &mut BkdWorld, text: String) -> Result<()> {
    super::text_contains(world, "#list mark", &text).await
}

#[then(expr = "I see the empty message {string}")]
async fn sees_empty_message(world: &mut BkdWorld, text: String) -> Result<()> {
    super::text_contains(world, "#empty", &text).await
}

#[then(expr = "the list has {int} bookmarks")]
async fn list_has(world: &mut BkdWorld, expected: usize) -> Result<()> {
    let driver = world.driver()?;
    world
        .browser()?
        .eventually(&format!("the list has {expected} bookmarks"), || async {
            Ok(driver.find_all(By::Css("#list .item")).await?.len() == expected)
        })
        .await
}

#[then("there is no Show more button")]
async fn no_show_more(world: &mut BkdWorld) -> Result<()> {
    show_more_is(world, false).await
}

#[then("there is a Show more button")]
async fn has_show_more(world: &mut BkdWorld) -> Result<()> {
    show_more_is(world, true).await
}

async fn show_more_is(world: &BkdWorld, shown: bool) -> Result<()> {
    let driver = world.driver()?;
    world
        .browser()?
        .eventually(&format!("Show more shown is {shown}"), || async {
            Ok(driver.find(By::Id("more")).await?.is_displayed().await? == shown)
        })
        .await
}

#[then(expr = "the page title is still {string}")]
async fn page_title_is(world: &mut BkdWorld, title: String) -> Result<()> {
    let actual = world.driver()?.title().await?;
    ensure!(actual == title, "the page title is {actual:?}");
    Ok(())
}
