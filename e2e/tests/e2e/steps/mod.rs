//! Step definitions, grouped by the part of the app they drive.

mod bookmarks;
mod bulk;
mod keyboard;
mod layout;
mod links;
mod session;
mod transfer;
mod users;

use anyhow::Result;
use bkd_e2e::world::BkdWorld;
use thirtyfour::prelude::*;

/// The sidebar link only an admin sees.
const MANAGE_USERS_LINK: &str = ".me a[href='/users']";

/// Slows every request down and records each Content Security Policy violation.
const SLOW_FETCH_AND_WATCH_CSP: &str = "window.cspViolations = []; document.addEventListener('securitypolicyviolation', (e) => window.cspViolations.push(e.violatedDirective)); const fetchNow = window.fetch; window.fetch = (...args) => new Promise((resolve) => setTimeout(resolve, 500)).then(() => fetchNow(...args));";

/// Presses Enter twice in a row in the field, as an impatient user would.
/// The page's script must send one request and leave the form alone for the second key.
async fn press_enter_twice(world: &BkdWorld, selector: &str) -> Result<()> {
    // A local server answers before the second key arrives; slow requests keep the
    // first one running, as they would over a real network.
    world
        .driver()?
        .execute(SLOW_FETCH_AND_WATCH_CSP, vec![])
        .await?;
    let enter = char::from(Key::Enter);
    world
        .element(selector)
        .await?
        .send_keys(format!("{enter}{enter}"))
        .await?;
    Ok(())
}

/// Waits until the text of the element matching `selector` satisfies `holds`.
/// The element may not exist yet, or may be rebuilt mid-poll; both count as "not yet".
async fn eventually_text(
    world: &BkdWorld,
    what: &str,
    selector: &str,
    holds: impl Fn(&str) -> bool,
) -> Result<()> {
    let driver = world.driver()?;
    let last_seen = std::cell::RefCell::new(String::new());
    world
        .browser()?
        .eventually(what, || async {
            let text = driver.find(By::Css(selector)).await?.text().await?;
            let holds_now = holds(&text);
            *last_seen.borrow_mut() = text;
            Ok(holds_now)
        })
        .await
        .map_err(|error| error.context(format!("the text was {:?}", last_seen.borrow())))
}

async fn text_contains(world: &BkdWorld, selector: &str, expected: &str) -> Result<()> {
    eventually_text(
        world,
        &format!("{selector} contains {expected:?}"),
        selector,
        |text| text.contains(expected),
    )
    .await
}

async fn text_lacks(world: &BkdWorld, selector: &str, unexpected: &str) -> Result<()> {
    eventually_text(
        world,
        &format!("{selector} no longer contains {unexpected:?}"),
        selector,
        |text| !text.contains(unexpected),
    )
    .await
}

/// Clicks the link whose text is exactly `text`, inside the element matching `scope`.
async fn click_link(world: &BkdWorld, scope: &str, text: &str) -> Result<()> {
    let driver = world.driver()?;
    // The page may still be replacing itself after the last click, which makes the
    // elements found a moment ago stale; looking again finds the new ones.
    world
        .browser()?
        .eventually(&format!("the link {text:?} in {scope}"), || async {
            let link = driver
                .query(By::Css(scope))
                .and_displayed()
                .first()
                .await?
                .query(By::XPath(format!(".//a[normalize-space(.)='{text}']")))
                .and_displayed()
                .first()
                .await?;
            link.click().await?;
            Ok(true)
        })
        .await
}
