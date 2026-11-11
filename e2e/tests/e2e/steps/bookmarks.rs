//! Adding, editing, deleting, searching and paging bookmarks.

use std::time::Duration;

use anyhow::{Result, ensure};
use bkd_e2e::api::NewBookmark;
use bkd_e2e::world::BkdWorld;
use cucumber::{given, then, when};
use serde_json::json;
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

#[when(expr = "I start editing {string}")]
async fn start_editing(world: &mut BkdWorld, title: String) -> Result<()> {
    click_row_action(world, &title, "Edit").await
}

#[when(expr = "I edit {string} to the title {string}")]
async fn edit_bookmark(world: &mut BkdWorld, old_title: String, new_title: String) -> Result<()> {
    start_editing(world, old_title).await?;
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

#[when(expr = "I choose the tag {string}")]
async fn choose_tag(world: &mut BkdWorld, tag: String) -> Result<()> {
    let link = By::XPath(format!(
        "//ul[@id='tag-list']//a[starts-with(normalize-space(.), '{tag}')]"
    ));
    world.element_by(link).await?.click().await?;
    Ok(())
}

#[when("I choose Random")]
async fn choose_random(world: &mut BkdWorld) -> Result<()> {
    world.click("#random").await
}

#[when("I choose Another")]
async fn choose_another(world: &mut BkdWorld) -> Result<()> {
    world.click("#pick-another").await
}

#[when("I close the pick")]
async fn close_pick(world: &mut BkdWorld) -> Result<()> {
    // "Another" rebuilds the pick, so a Close button found a moment ago can already be gone.
    world
        .browser()?
        .eventually("the pick can be closed", || async {
            let close = By::XPath("//*[@id='pick']//button[text()='Close']".to_owned());
            world.element_by(close).await?.click().await?;
            Ok(true)
        })
        .await
}

#[then(expr = "the pick shows {string}")]
async fn pick_shows(world: &mut BkdWorld, text: String) -> Result<()> {
    super::text_contains(world, "#pick", &text).await
}

// A hidden element has no text, so this also passes once the pick is closed.
#[then(expr = "the pick does not show {string}")]
async fn pick_does_not_show(world: &mut BkdWorld, text: String) -> Result<()> {
    super::text_lacks(world, "#pick", &text).await
}

#[then(expr = "I am told {string}")]
async fn am_told(world: &mut BkdWorld, text: String) -> Result<()> {
    world.expect_text(&text).await
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

/// The `<time>` element in the row of the bookmark with this title.
fn age_of(title: &str) -> By {
    By::XPath(format!(
        "//ul[@id='list']//li[.//a[@class='title' and text()='{title}']]//time"
    ))
}

#[then(expr = "the age of {string} says {string}")]
async fn age_says(world: &mut BkdWorld, title: String, expected: String) -> Result<()> {
    let age = world.element_by(age_of(&title)).await?;
    let actual = age.text().await?;
    ensure!(actual == expected, "the age says {actual:?}");
    Ok(())
}

/// The hover text must match what the browser itself makes of the machine-readable
/// time, so its language, date order and time zone are respected.
const BROWSER_FORMAT: &str = "const format = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }); return format.format(new Date(arguments[0].dateTime));";

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
    // A bookmark made a moment ago: a wrong conversion (a missing `* 1000`, say) would
    // still agree with itself above, so the instant is checked against the clock too.
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

#[then(expr = "{string} is not highlighted")]
async fn is_not_highlighted(world: &mut BkdWorld, text: String) -> Result<()> {
    super::text_lacks(world, "#list mark", &text).await
}

/// The cases the server's parser is tested against (`src/search.rs`).
const SHARED_SEARCH_CASES: &str = include_str!(concat!(
    env!("CARGO_MANIFEST_DIR"),
    "/../tests/search_syntax.json"
));

#[then("the browser reads every shared search case as the server does")]
async fn browser_parses_shared_cases(world: &mut BkdWorld) -> Result<()> {
    let cases: Vec<serde_json::Value> = serde_json::from_str(SHARED_SEARCH_CASES)?;
    ensure!(!cases.is_empty(), "no shared search cases");
    let read = world
        .driver()?
        .execute(
            "return JSON.parse(arguments[0]).map((c) => parseQuery(c.query).map((t) => [t.field, t.text, t.negated]));",
            vec![json!(SHARED_SEARCH_CASES)],
        )
        .await?;
    let read = read.json().as_array().cloned().unwrap_or_default();
    ensure!(
        read.len() == cases.len(),
        "the browser read {} cases",
        read.len()
    );
    for (case, terms) in cases.iter().zip(read) {
        ensure!(
            terms == case["terms"],
            "query {}: the browser read {terms}, the server reads {}",
            case["query"],
            case["terms"]
        );
    }
    Ok(())
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
