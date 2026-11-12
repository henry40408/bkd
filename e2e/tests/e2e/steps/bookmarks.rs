//! Adding, editing, deleting, searching and paging bookmarks.

use std::time::Duration;

use anyhow::{Result, ensure};
use bkd_e2e::api::NewBookmark;
use bkd_e2e::world::BkdWorld;
use cucumber::{given, then, when};
use serde_json::json;
use thirtyfour::prelude::*;

const SEARCH_BAR: &str = "[role=search]";
const ADD_FORM: &str = "#add form";
const EDIT_FORM: &str = ".edit-form";

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

/// The add form sits in a `<details>` that starts closed.
async fn open_add_form(world: &BkdWorld) -> Result<()> {
    let form = world.driver()?.find(By::Css(ADD_FORM)).await?;
    if !form.is_displayed().await? {
        world.click("#add summary").await?;
    }
    Ok(())
}

async fn fill_add_form(world: &BkdWorld, url: &str, title: &str) -> Result<()> {
    open_add_form(world).await?;
    world
        .fill(&format!("{ADD_FORM} input[name=url]"), url)
        .await?;
    world
        .fill(&format!("{ADD_FORM} input[name=title]"), title)
        .await
}

#[when(expr = "I add the bookmark {string} titled {string} tagged {string}")]
async fn add_bookmark(
    world: &mut BkdWorld,
    url: String,
    title: String,
    tags: String,
) -> Result<()> {
    fill_add_form(world, &url, &title).await?;
    world
        .fill(&format!("{ADD_FORM} input[name=tags]"), &tags)
        .await?;
    world
        .click(&format!("{ADD_FORM} button[type=submit]"))
        .await
}

#[when(expr = "I press Enter twice to add {string} titled {string}")]
async fn submit_add_form_twice(world: &mut BkdWorld, url: String, title: String) -> Result<()> {
    fill_add_form(world, &url, &title).await?;
    super::press_enter_twice(world, &format!("{ADD_FORM} input[name=title]")).await
}

/// The Edit and Delete links show only while the pointer is over the row.
async fn click_row_action(world: &BkdWorld, title: &str, action: &str) -> Result<()> {
    let row = By::XPath(format!(
        "//li[contains(@class, 'item')][.//a[@class='title' and text()='{title}']]"
    ));
    let row = world.element_by(row).await?;
    world.hover(&row).await?;
    world
        .click(&format!("a[aria-label='{action} {title}']"))
        .await
}

#[when(expr = "I start editing {string}")]
async fn start_editing(world: &mut BkdWorld, title: String) -> Result<()> {
    click_row_action(world, &title, "Edit").await?;
    world.element(EDIT_FORM).await?;
    Ok(())
}

#[when(expr = "I edit {string} to the title {string}")]
async fn edit_bookmark(world: &mut BkdWorld, old_title: String, new_title: String) -> Result<()> {
    start_editing(world, old_title).await?;
    world
        .fill(&format!("{EDIT_FORM} input[name=title]"), &new_title)
        .await?;
    world
        .click(&format!("{EDIT_FORM} button[type=submit]"))
        .await
}

#[when("I cancel editing")]
async fn cancel_editing(world: &mut BkdWorld) -> Result<()> {
    world.click(&format!("{EDIT_FORM} a.quiet-link")).await
}

#[when(expr = "I start deleting {string}")]
async fn start_deleting(world: &mut BkdWorld, title: String) -> Result<()> {
    click_row_action(world, &title, "Delete").await
}

#[when("I keep it")]
async fn keep_it(world: &mut BkdWorld) -> Result<()> {
    world.click("main a.quiet-link").await
}

#[when("I confirm the deletion")]
async fn confirm_deletion(world: &mut BkdWorld) -> Result<()> {
    world.click("main button.danger-button").await
}

#[then(expr = "I am asked {string}")]
async fn am_asked(world: &mut BkdWorld, question: String) -> Result<()> {
    super::text_contains(world, "main h1", &question).await
}

#[when(expr = "I search for {string}")]
async fn search_for(world: &mut BkdWorld, query: String) -> Result<()> {
    world
        .fill(&format!("{SEARCH_BAR} input[name=q]"), &query)
        .await?;
    world
        .click(&format!("{SEARCH_BAR} button[type=submit]"))
        .await
}

/// Holds back the answer to one search for 1.5 s in the browser, and records when it was
/// sent and when it arrived, so a scenario can make an old answer land after a newer one.
const SLOW_SEARCH: &str = "const term = arguments[0]; const slowMs = 1500; \
    window.searchSent = []; window.searchAnswered = []; \
    const fetchNow = window.fetch; \
    window.fetch = async (url, ...rest) => { \
      const slow = new URL(url, location.href).searchParams.get('q') === term; \
      if (slow) window.searchSent.push(term); \
      const response = await fetchNow(url, ...rest); \
      if (!slow) return response; \
      await new Promise((resolve) => setTimeout(resolve, slowMs)); \
      window.searchAnswered.push(term); \
      return response; \
    };";

#[when(expr = "the server answers the search for {string} slowly")]
async fn slow_search(world: &mut BkdWorld, term: String) -> Result<()> {
    world
        .driver()?
        .execute(SLOW_SEARCH, vec![json!(term)])
        .await?;
    Ok(())
}

#[then(expr = "the search for {string} has been sent")]
async fn search_sent(world: &mut BkdWorld, term: String) -> Result<()> {
    search_recorded(world, "searchSent", &term).await
}

#[then("no slow answer has arrived yet")]
async fn no_slow_answer_yet(world: &mut BkdWorld) -> Result<()> {
    let value = world
        .driver()?
        .execute("return window.searchAnswered.length", vec![])
        .await?;
    ensure!(
        value.json().as_u64() == Some(0),
        "a slow answer has already arrived, so the order is not what the scenario needs"
    );
    Ok(())
}

#[when(expr = "the slow answer to the search for {string} has arrived")]
async fn search_answered(world: &mut BkdWorld, term: String) -> Result<()> {
    search_recorded(world, "searchAnswered", &term).await?;
    // Let the page process the answer before the scenario looks at the list.
    tokio::time::sleep(Duration::from_millis(300)).await;
    Ok(())
}

async fn search_recorded(world: &BkdWorld, list: &str, term: &str) -> Result<()> {
    let driver = world.driver()?;
    let script = format!("return window.{list}.includes(arguments[0])");
    world
        .browser()?
        .eventually(&format!("{list} has {term}"), || async {
            let value = driver.execute(&script, vec![json!(term)]).await?;
            Ok(value.json().as_bool() == Some(true))
        })
        .await
}

/// A sidebar filter link; its label is followed by a count.
async fn choose_sidebar_filter(world: &BkdWorld, label: &str) -> Result<()> {
    let link = By::XPath(format!(
        "//nav[@id='sidebar']//li/a[starts-with(normalize-space(.), '{label}')]"
    ));
    world.element_by(link).await?.click().await?;
    Ok(())
}

#[when(expr = "I choose the tag {string}")]
async fn choose_tag(world: &mut BkdWorld, tag: String) -> Result<()> {
    choose_sidebar_filter(world, &tag).await
}

#[when(expr = "I choose the link filter {string}")]
async fn choose_link_filter(world: &mut BkdWorld, label: String) -> Result<()> {
    choose_sidebar_filter(world, &label).await
}

#[then(expr = "the sidebar lists the link filter {string}")]
async fn sidebar_lists_link_filter(world: &mut BkdWorld, label: String) -> Result<()> {
    super::text_contains(world, "#sidebar", &label).await
}

#[when("I choose Random")]
async fn choose_random(world: &mut BkdWorld) -> Result<()> {
    super::click_link(world, SEARCH_BAR, "Random").await
}

#[when("I choose Another")]
async fn choose_another(world: &mut BkdWorld) -> Result<()> {
    super::click_link(world, "section.pick", "Another").await
}

#[when("I close the pick")]
async fn close_pick(world: &mut BkdWorld) -> Result<()> {
    super::click_link(world, "section.pick", "Close").await
}

#[then(expr = "the pick shows {string}")]
async fn pick_shows(world: &mut BkdWorld, text: String) -> Result<()> {
    super::text_contains(world, "section.pick", &text).await
}

#[then(expr = "the pick does not show {string}")]
async fn pick_does_not_show(world: &mut BkdWorld, text: String) -> Result<()> {
    // The pick can only be absent after a positive check, so this one waits for it.
    super::text_lacks(world, "section.pick", &text).await
}

#[then(expr = "the add form holds {string} as the title")]
async fn add_form_holds_title(world: &mut BkdWorld, expected: String) -> Result<()> {
    let field = world
        .element(&format!("{ADD_FORM} input[name=title]"))
        .await?;
    let actual = field.value().await?.unwrap_or_default();
    ensure!(actual == expected, "the title field holds {actual:?}");
    Ok(())
}

#[then("there is no pick")]
async fn no_pick(world: &mut BkdWorld) -> Result<()> {
    let driver = world.driver()?;
    world
        .browser()?
        .eventually("the pick is gone", || async {
            Ok(driver.find_all(By::Css("section.pick")).await?.is_empty())
        })
        .await
}

#[then(expr = "I am told {string}")]
async fn am_told(world: &mut BkdWorld, text: String) -> Result<()> {
    world.expect_text(&text).await
}

#[when(expr = "I choose {string} in the pager")]
async fn choose_in_pager(world: &mut BkdWorld, label: String) -> Result<()> {
    super::click_link(world, "nav.pager", &label).await
}

// ---------------------------------------------------------- assertions

#[then(expr = "the list shows {string}")]
async fn list_shows(world: &mut BkdWorld, text: String) -> Result<()> {
    super::text_contains(world, "#main", &text).await
}

#[then(expr = "the list does not show {string}")]
async fn list_does_not_show(world: &mut BkdWorld, text: String) -> Result<()> {
    super::text_lacks(world, "#main", &text).await
}

/// The `<time>` element in the row of the bookmark with this title.
fn age_of(title: &str) -> By {
    By::XPath(format!(
        "//li[contains(@class, 'item')][.//a[@class='title' and text()='{title}']]//time"
    ))
}

#[then(expr = "the age of {string} says {string}")]
async fn age_says(world: &mut BkdWorld, title: String, expected: String) -> Result<()> {
    let age = world.element_by(age_of(&title)).await?;
    let actual = age.text().await?;
    ensure!(actual == expected, "the age says {actual:?}");
    Ok(())
}

/// The script shows the hover text in the reader's locale and time zone, which only the
/// browser itself can tell; the server's text is in UTC.
const BROWSER_FORMAT: &str = "return new Date(arguments[0].dateTime).toLocaleString();";

#[then(expr = "hovering the age of {string} shows the date as the browser formats it")]
async fn age_hover_is_browser_formatted(world: &mut BkdWorld, title: String) -> Result<()> {
    let age = world.element_by(age_of(&title)).await?;
    let hover_text = age.attr("title").await?.unwrap_or_default();
    ensure!(!hover_text.is_empty(), "the age has no hover text");
    let expected = world
        .driver()?
        .execute(BROWSER_FORMAT, vec![age.to_json()?])
        .await?;
    let expected = expected.json().as_str().unwrap_or_default().to_owned();
    // A bookmark made a moment ago: a wrong conversion would still agree with itself
    // above, so the instant is checked against the clock too.
    let seconds_off = world
        .driver()?
        .execute(
            "return Math.abs(Date.now() - Date.parse(arguments[0].dateTime)) / 1000;",
            vec![age.to_json()?],
        )
        .await?;
    let seconds_off = seconds_off.json().as_f64().unwrap_or(f64::MAX);
    ensure!(
        seconds_off < 120.0,
        "datetime is {seconds_off} seconds from now"
    );
    ensure!(
        hover_text == expected,
        "hover text {hover_text:?}, the browser formats {expected:?}"
    );
    Ok(())
}

#[then(expr = "the hover text of the age of {string} names UTC")]
async fn age_hover_names_utc(world: &mut BkdWorld, title: String) -> Result<()> {
    let age = world.element_by(age_of(&title)).await?;
    let hover_text = age.attr("title").await?.unwrap_or_default();
    ensure!(
        hover_text.contains("UTC"),
        "the hover text is {hover_text:?}"
    );
    Ok(())
}

#[then(expr = "the link {string} points to {string}")]
async fn link_points_to(world: &mut BkdWorld, title: String, href: String) -> Result<()> {
    let link = By::XPath(format!("//li//a[@class='title' and text()='{title}']"));
    let actual = world.element_by(link).await?.attr("href").await?;
    ensure!(
        actual.as_deref() == Some(href.as_str()),
        "the link goes to {actual:?}"
    );
    Ok(())
}

#[then(expr = "the sidebar lists the tag {string}")]
async fn sidebar_lists_tag(world: &mut BkdWorld, tag: String) -> Result<()> {
    super::text_contains(world, "#sidebar", &tag).await
}

#[then(expr = "the sidebar does not list the tag {string}")]
async fn sidebar_does_not_list_tag(world: &mut BkdWorld, tag: String) -> Result<()> {
    super::text_lacks(world, "#sidebar", &tag).await
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
    super::text_contains(world, "main mark", &text).await
}

#[then(expr = "{string} is not highlighted")]
async fn is_not_highlighted(world: &mut BkdWorld, text: String) -> Result<()> {
    super::text_lacks(world, "main mark", &text).await
}

#[then(expr = "I see the empty message {string}")]
async fn sees_empty_message(world: &mut BkdWorld, text: String) -> Result<()> {
    super::text_contains(world, "main .empty", &text).await
}

#[then(expr = "the list has {int} bookmarks")]
async fn list_has(world: &mut BkdWorld, expected: usize) -> Result<()> {
    let driver = world.driver()?;
    world
        .browser()?
        .eventually(&format!("the list has {expected} bookmarks"), || async {
            Ok(driver.find_all(By::Css("ul.list li.item")).await?.len() == expected)
        })
        .await
}

#[then(expr = "the pager offers {string}")]
async fn pager_offers(world: &mut BkdWorld, label: String) -> Result<()> {
    pager_has(world, &label, true).await
}

#[then(expr = "the pager does not offer {string}")]
async fn pager_does_not_offer(world: &mut BkdWorld, label: String) -> Result<()> {
    pager_has(world, &label, false).await
}

async fn pager_has(world: &BkdWorld, label: &str, offered: bool) -> Result<()> {
    let driver = world.driver()?;
    let link = By::XPath(format!("//nav[@class='pager']//a[text()='{label}']"));
    world
        .browser()?
        .eventually(
            &format!("the pager offering {label} is {offered}"),
            || async { Ok(!driver.find_all(link.clone()).await?.is_empty() == offered) },
        )
        .await
}

#[then(expr = "the page title is still {string}")]
async fn page_title_is(world: &mut BkdWorld, title: String) -> Result<()> {
    let actual = world.driver()?.title().await?;
    ensure!(actual == title, "the page title is {actual:?}");
    Ok(())
}
