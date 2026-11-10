//! First-run setup, sign-in and sign-out.

use anyhow::{Context, Result, ensure};
use bkd_e2e::api::{PASSWORD, SESSION_COOKIE};
use bkd_e2e::world::BkdWorld;
use cucumber::{given, then, when};
use thirtyfour::prelude::*;

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

#[then("I see the setup page")]
#[then("I still see the setup page")]
async fn setup_page(world: &mut BkdWorld) -> Result<()> {
    world.element("#setup-form").await?;
    Ok(())
}

#[then("I see the sign-in page")]
#[then("I still see the sign-in page")]
async fn sign_in_page(world: &mut BkdWorld) -> Result<()> {
    world.element("#login-form").await?;
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
    world.fill("#setup-form input[name=token]", token).await?;
    world.fill("#setup-form input[name=name]", username).await?;
    world
        .fill("#setup-form input[name=password]", PASSWORD)
        .await?;
    world.click("#setup-form button[type=submit]").await
}

#[when(expr = "I sign in as {string} with the right password")]
async fn sign_in_right_password(world: &mut BkdWorld, username: String) -> Result<()> {
    submit_login_form(world, &username, PASSWORD).await?;
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
    world.me = Some(username);
    Ok(())
}

async fn submit_login_form(world: &BkdWorld, username: &str, password: &str) -> Result<()> {
    world.fill("#login-form input[name=name]", username).await?;
    world
        .fill("#login-form input[name=password]", password)
        .await?;
    world.click("#login-form button[type=submit]").await
}

#[when(expr = "I press Enter twice to sign in as {string}")]
async fn submit_sign_in_twice(world: &mut BkdWorld, username: String) -> Result<()> {
    world
        .fill("#login-form input[name=name]", &username)
        .await?;
    world
        .fill("#login-form input[name=password]", PASSWORD)
        .await?;
    super::press_enter_twice(world, "#login-form input[name=password]").await?;
    world.me = Some(username);
    Ok(())
}

/// What expiry or a server-side sign-out looks like to the page: its next call gets a 401.
#[when("my session cookie disappears")]
async fn session_cookie_disappears(world: &mut BkdWorld) -> Result<()> {
    world.driver()?.delete_cookie(SESSION_COOKIE).await?;
    Ok(())
}

#[when("I sign out")]
async fn sign_out(world: &mut BkdWorld) -> Result<()> {
    world.click("#me button").await
}

#[then(expr = "I am signed in as {string}")]
async fn signed_in_as(world: &mut BkdWorld, username: String) -> Result<()> {
    super::text_contains(world, "#me strong", &username).await
}

#[then("I can open Manage users")]
async fn can_open_manage_users(world: &mut BkdWorld) -> Result<()> {
    world.element("#me a").await?;
    Ok(())
}

#[then(expr = "I see the error {string}")]
async fn sees_error(world: &mut BkdWorld, expected: String) -> Result<()> {
    let driver = world.driver()?;
    world
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
        .await
}

#[then("no toast is shown")]
async fn no_toast(world: &mut BkdWorld) -> Result<()> {
    // A toast is written after the next paint, so look for a while, not once.
    for _ in 0..5 {
        let text = world.driver()?.find(By::Id("toast")).await?.text().await?;
        ensure!(text.is_empty(), "a toast says {text:?}");
        tokio::time::sleep(std::time::Duration::from_millis(100)).await;
    }
    Ok(())
}

#[then(expr = "the sign-in error says exactly {string}")]
async fn sign_in_error_says_exactly(world: &mut BkdWorld, expected: String) -> Result<()> {
    let text = world.element("#login-error").await?.text().await?;
    ensure!(text.trim() == expected, "the sign-in error says {text:?}");
    Ok(())
}

#[then("the address has no query string")]
async fn no_query_string(world: &mut BkdWorld) -> Result<()> {
    let url = world.driver()?.current_url().await?;
    ensure!(url.query().is_none(), "the address is {url}");
    Ok(())
}

#[then("the browser reports no policy violation")]
async fn no_policy_violation(world: &mut BkdWorld) -> Result<()> {
    // The violation event fires asynchronously after the rejected submission.
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
