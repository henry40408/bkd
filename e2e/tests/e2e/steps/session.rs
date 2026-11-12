//! First-run setup, sign-in, sign-out, the address bar and the account's own pages.

use anyhow::{Context, Result, ensure};
use bkd_e2e::api::{PASSWORD, SESSION_COOKIE};
use bkd_e2e::world::BkdWorld;
use cucumber::{given, then, when};
use thirtyfour::prelude::*;

const LOGIN_FORM: &str = "form[action='/login']";
const SETUP_FORM: &str = "form[action='/setup']";
const CONFIRM_FORM: &str = "form[action='/confirm']";
const PASSWORD_FORM: &str = ".password-form";
const SEARCH_BOX: &str = "[role=search] input[name=q]";

#[given(expr = "the admin {string} exists")]
async fn admin_exists(world: &mut BkdWorld, username: String) -> Result<()> {
    let session = world.api().setup(&username, PASSWORD).await?;
    world.remember(&username, session);
    Ok(())
}

#[given(expr = "I am signed in as the admin {string}")]
async fn signed_in_as_admin(world: &mut BkdWorld, username: String) -> Result<()> {
    let session = world.api().setup(&username, PASSWORD).await?;
    let token = session.token().to_owned();
    world.remember(&username, session);
    world.me = Some(username.clone());
    world
        .browser()?
        .adopt_session(world.base_url(), &token)
        .await?;
    world.goto("/").await?;
    signed_in_as(world, username).await
}

#[when("I open the site")]
async fn open_the_site(world: &mut BkdWorld) -> Result<()> {
    world.goto("/").await
}

#[when(expr = "I open the site at {string}")]
async fn open_the_site_at(world: &mut BkdWorld, path: String) -> Result<()> {
    world.goto(&path).await
}

#[when("I reload the page")]
async fn reload_the_page(world: &mut BkdWorld) -> Result<()> {
    world.driver()?.refresh().await?;
    Ok(())
}

#[when("I go back")]
async fn go_back(world: &mut BkdWorld) -> Result<()> {
    world.driver()?.back().await?;
    Ok(())
}

/// The script changes the address after a click, so the check waits for it.
#[then(expr = "the address is {string}")]
async fn address_is(world: &mut BkdWorld, expected: String) -> Result<()> {
    let driver = world.driver()?;
    let last_seen = std::cell::RefCell::new(String::new());
    world
        .browser()?
        .eventually(&format!("the address is {expected:?}"), || async {
            let url = driver.current_url().await?;
            let mut address = url.path().to_owned();
            if let Some(query) = url.query() {
                address = format!("{address}?{query}");
            }
            let matches = address == expected;
            *last_seen.borrow_mut() = address;
            Ok(matches)
        })
        .await
        .map_err(|error| error.context(format!("the address was {:?}", last_seen.borrow())))
}

#[then(expr = "the search box holds {string}")]
async fn search_box_holds(world: &mut BkdWorld, expected: String) -> Result<()> {
    let field = world.element(SEARCH_BOX).await?;
    let actual = field.value().await?.unwrap_or_default();
    ensure!(actual == expected, "the search box holds {actual:?}");
    Ok(())
}

#[then("I see the setup page")]
#[then("I still see the setup page")]
async fn setup_page(world: &mut BkdWorld) -> Result<()> {
    world.element(SETUP_FORM).await?;
    Ok(())
}

#[then("I see the sign-in page")]
#[then("I still see the sign-in page")]
async fn sign_in_page(world: &mut BkdWorld) -> Result<()> {
    world.element(LOGIN_FORM).await?;
    Ok(())
}

#[when(expr = "I create the first admin {string} with the setup token")]
async fn create_first_admin(world: &mut BkdWorld, username: String) -> Result<()> {
    let token = world.setup_token().to_owned();
    submit_setup_form(world, &token, &username).await?;
    world.me = Some(username);
    Ok(())
}

#[when(expr = "I try to create the first admin {string} with the token {string}")]
async fn try_create_first_admin(
    world: &mut BkdWorld,
    username: String,
    token: String,
) -> Result<()> {
    submit_setup_form(world, &token, &username).await
}

async fn submit_setup_form(world: &BkdWorld, token: &str, username: &str) -> Result<()> {
    world
        .fill(&format!("{SETUP_FORM} input[name=token]"), token)
        .await?;
    world
        .fill(&format!("{SETUP_FORM} input[name=username]"), username)
        .await?;
    world
        .fill(&format!("{SETUP_FORM} input[name=password]"), PASSWORD)
        .await?;
    world
        .click(&format!("{SETUP_FORM} button[type=submit]"))
        .await
}

#[when(expr = "I sign in as {string} with the right password")]
async fn sign_in_right_password(world: &mut BkdWorld, username: String) -> Result<()> {
    submit_login_form(world, &username, PASSWORD).await?;
    // Navigating away before the sign-in lands would cancel it.
    super::text_contains(world, ".me strong", &username).await?;
    world.me = Some(username);
    Ok(())
}

#[when(expr = "I sign in as {string} with a wrong password")]
async fn sign_in_wrong_password(world: &mut BkdWorld, username: String) -> Result<()> {
    submit_login_form(world, &username, "not-the-password").await
}

/// Starts from a signed-out browser, since the scenario's background may have
/// signed another account in.
#[when(expr = "I sign in as {string} in the browser")]
async fn sign_in_in_browser(world: &mut BkdWorld, username: String) -> Result<()> {
    world.driver()?.delete_all_cookies().await?;
    world.goto("/").await?;
    submit_login_form(world, &username, PASSWORD).await?;
    // Navigating away before the sign-in lands would cancel it.
    super::text_contains(world, ".me strong", &username).await?;
    world.me = Some(username);
    Ok(())
}

async fn submit_login_form(world: &BkdWorld, username: &str, password: &str) -> Result<()> {
    world
        .fill(&format!("{LOGIN_FORM} input[name=username]"), username)
        .await?;
    world
        .fill(&format!("{LOGIN_FORM} input[name=password]"), password)
        .await?;
    world
        .click(&format!("{LOGIN_FORM} button[type=submit]"))
        .await
}

#[when(expr = "I press Enter twice to sign in as {string}")]
async fn submit_sign_in_twice(world: &mut BkdWorld, username: String) -> Result<()> {
    let password_field = format!("{LOGIN_FORM} input[name=password]");
    world
        .fill(&format!("{LOGIN_FORM} input[name=username]"), &username)
        .await?;
    world.fill(&password_field, PASSWORD).await?;
    super::press_enter_twice(world, &password_field).await?;
    world.me = Some(username);
    Ok(())
}

/// What expiry or a server-side sign-out looks like to the page: the cookie is still sent,
/// but names no session. A missing cookie would look like a first visit instead.
#[when("my session stops working")]
async fn session_stops_working(world: &mut BkdWorld) -> Result<()> {
    let mut cookie = Cookie::new(SESSION_COOKIE, "ended");
    cookie.set_path("/");
    world.driver()?.add_cookie(cookie).await?;
    Ok(())
}

#[when("I sign out")]
async fn sign_out(world: &mut BkdWorld) -> Result<()> {
    world.click(".me button").await
}

#[then(expr = "I am signed in as {string}")]
async fn signed_in_as(world: &mut BkdWorld, username: String) -> Result<()> {
    super::text_contains(world, ".me strong", &username).await
}

#[then("I can open Manage users")]
async fn can_open_manage_users(world: &mut BkdWorld) -> Result<()> {
    world.element(super::MANAGE_USERS_LINK).await?;
    Ok(())
}

#[then(expr = "I see the error {string}")]
async fn sees_error(world: &mut BkdWorld, expected: String) -> Result<()> {
    let driver = world.driver()?;
    let outcome = world
        .browser()?
        .eventually(&format!("an error says {expected:?}"), || async {
            for alert in driver.find_all(By::Css("[role=alert]")).await? {
                if alert.is_displayed().await?
                    && alert
                        .text()
                        .await?
                        .to_lowercase()
                        .contains(&expected.to_lowercase())
                {
                    return Ok(true);
                }
            }
            Ok(false)
        })
        .await;
    if outcome.is_err() {
        let body = driver.find(By::Css("body")).await?.text().await?;
        let url = driver.current_url().await?;
        return outcome.with_context(|| format!("{url} shows: {body}"));
    }
    outcome
}

#[then(expr = "the sign-in page says exactly {string}")]
async fn sign_in_page_says_exactly(world: &mut BkdWorld, expected: String) -> Result<()> {
    let text = world
        .element(&format!("{LOGIN_FORM} .notice"))
        .await?
        .text()
        .await?;
    ensure!(text.trim() == expected, "the sign-in page says {text:?}");
    Ok(())
}

#[then("the address has no query string")]
async fn no_query_string(world: &mut BkdWorld) -> Result<()> {
    let url = world.driver()?.current_url().await?;
    ensure!(url.query().is_none(), "the address is {url}");
    Ok(())
}

#[when("I open Account")]
async fn open_account(world: &mut BkdWorld) -> Result<()> {
    world.click(".me a[href='/account']").await?;
    world.element(PASSWORD_FORM).await?;
    Ok(())
}

#[when("I mark the page")]
async fn mark_page(world: &mut BkdWorld) -> Result<()> {
    world
        .driver()?
        .execute("window.pageMarker = true", vec![])
        .await?;
    Ok(())
}

#[then("the page was not reloaded")]
async fn page_not_reloaded(world: &mut BkdWorld) -> Result<()> {
    let marker = world
        .driver()?
        .execute("return window.pageMarker === true", vec![])
        .await?;
    ensure!(marker.json().as_bool() == Some(true), "the page reloaded");
    Ok(())
}

#[when("I click the bkd brand")]
async fn click_brand(world: &mut BkdWorld) -> Result<()> {
    world.click("#sidebar .brand a").await
}

#[when(expr = "I change my password from {string} to {string}")]
async fn change_password(world: &mut BkdWorld, current: String, new: String) -> Result<()> {
    submit_password_form(world, &current, &new, &new).await
}

#[when(expr = "I change my password from {string} to {string} but repeat {string}")]
async fn change_password_with_typo(
    world: &mut BkdWorld,
    current: String,
    new: String,
    again: String,
) -> Result<()> {
    submit_password_form(world, &current, &new, &again).await
}

async fn submit_password_form(
    world: &BkdWorld,
    current: &str,
    new: &str,
    again: &str,
) -> Result<()> {
    for (name, value) in [("current", current), ("new", new), ("again", again)] {
        world
            .fill(&format!("{PASSWORD_FORM} input[name={name}]"), value)
            .await?;
    }
    world
        .click(&format!("{PASSWORD_FORM} button[type=submit]"))
        .await
}

#[given("I am also signed in on another browser")]
async fn also_signed_in_elsewhere(world: &mut BkdWorld) -> Result<()> {
    let username = world.me.clone().context("nobody is signed in")?;
    let session = world.api().login(&username, PASSWORD).await?;
    world.remember("elsewhere", session);
    Ok(())
}

#[then(expr = "the list of browsers has {int} rows")]
async fn browser_list_has_rows(world: &mut BkdWorld, expected: usize) -> Result<()> {
    let driver = world.driver()?;
    world
        .browser()?
        .eventually(&format!("the list has {expected} rows"), || async {
            let rows = driver.find_all(By::Css(".session-list .user-row")).await?;
            Ok(rows.len() == expected)
        })
        .await
}

#[when("I sign out the other browser")]
async fn sign_out_other_browser(world: &mut BkdWorld) -> Result<()> {
    world.click(".session-list button").await
}

#[when("I sign out all other browsers")]
async fn sign_out_all_other_browsers(world: &mut BkdWorld) -> Result<()> {
    world
        .click("form[action='/account/sessions/revoke-others'] button")
        .await
}

/// The server asks for the password again once the last one is five minutes old. Waiting
/// that long is not an option, so the row is aged in the database.
#[given("my password confirmation is old")]
async fn confirmation_is_old(world: &mut BkdWorld) -> Result<()> {
    let output = tokio::process::Command::new("sqlite3")
        .arg(world.db_path())
        .arg("UPDATE sessions SET confirmed_at = 0")
        .output()
        .await
        .context("running the sqlite3 command-line tool")?;
    ensure!(
        output.status.success(),
        "sqlite3 failed: {}",
        String::from_utf8_lossy(&output.stderr)
    );
    Ok(())
}

#[then("I am asked to confirm my password")]
async fn asked_to_confirm(world: &mut BkdWorld) -> Result<()> {
    world.element(CONFIRM_FORM).await?;
    Ok(())
}

#[when(expr = "I confirm my password with {string}")]
async fn confirm_password(world: &mut BkdWorld, password: String) -> Result<()> {
    world
        .fill(&format!("{CONFIRM_FORM} input[name=password]"), &password)
        .await?;
    world
        .click(&format!("{CONFIRM_FORM} button[type=submit]"))
        .await
}

#[when("I cancel the confirmation")]
async fn cancel_confirmation(world: &mut BkdWorld) -> Result<()> {
    world.click(&format!("{CONFIRM_FORM} a.quiet-link")).await
}

#[then("the browser reports no policy violation")]
async fn no_policy_violation(world: &mut BkdWorld) -> Result<()> {
    // The violation event fires asynchronously after a rejected submission.
    tokio::time::sleep(std::time::Duration::from_millis(500)).await;
    let seen = world
        .driver()?
        .execute("return window.cspViolations", vec![])
        .await?;
    let violations = seen
        .json()
        .as_array()
        .context("the violation watcher is gone: the page reloaded")?;
    ensure!(violations.is_empty(), "the browser reported {violations:?}");
    Ok(())
}
