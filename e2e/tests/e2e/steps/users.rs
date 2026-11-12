//! Admin user management.

use anyhow::{Result, ensure};
use bkd_e2e::api::PASSWORD;
use bkd_e2e::world::BkdWorld;
use cucumber::{given, then, when};
use thirtyfour::prelude::*;

const NEW_USER_FORM: &str = ".new-user";

#[given(expr = "the member {string} exists")]
async fn member_exists(world: &mut BkdWorld, username: String) -> Result<()> {
    let admin = world.my_session()?.clone();
    world
        .api()
        .create_user(&admin, &username, PASSWORD, false)
        .await
}

#[given(expr = "the member {string} exists and is signed in elsewhere")]
async fn member_signed_in_elsewhere(world: &mut BkdWorld, username: String) -> Result<()> {
    member_exists(world, username.clone()).await?;
    let session = world.api().login(&username, PASSWORD).await?;
    world.remember(&username, session);
    Ok(())
}

#[when("I open Manage users")]
async fn open_manage_users(world: &mut BkdWorld) -> Result<()> {
    world.click(super::MANAGE_USERS_LINK).await?;
    world.element(".user-list").await?;
    Ok(())
}

#[when(expr = "I create the user {string} with a temporary password")]
async fn create_user(world: &mut BkdWorld, username: String) -> Result<()> {
    world
        .fill(&format!("{NEW_USER_FORM} input[name=username]"), &username)
        .await?;
    world
        .fill(&format!("{NEW_USER_FORM} input[name=password]"), PASSWORD)
        .await?;
    world
        .click(&format!("{NEW_USER_FORM} button[type=submit]"))
        .await
}

#[when(expr = "I disable {string}")]
async fn disable_user(world: &mut BkdWorld, username: String) -> Result<()> {
    world
        .click(&format!("button[aria-label='Disable {username}']"))
        .await
}

#[when(expr = "I start resetting the password of {string}")]
async fn start_resetting_password(world: &mut BkdWorld, username: String) -> Result<()> {
    world
        .click(&format!("a[aria-label='Reset password of {username}']"))
        .await?;
    world.element(".password-form").await?;
    Ok(())
}

#[when(expr = "I reset the password of {string} to {string}")]
async fn reset_password(world: &mut BkdWorld, username: String, password: String) -> Result<()> {
    start_resetting_password(world, username).await?;
    world
        .fill(".password-form input[name=password]", &password)
        .await?;
    world.click(".password-form button[type=submit]").await
}

#[when(expr = "I start deleting the user {string}")]
async fn start_deleting_user(world: &mut BkdWorld, username: String) -> Result<()> {
    world
        .click(&format!("a[aria-label='Delete {username}']"))
        .await
}

#[when(expr = "I delete {string}")]
async fn delete_user(world: &mut BkdWorld, username: String) -> Result<()> {
    start_deleting_user(world, username).await?;
    world.click("main button.danger-button").await
}

#[then(expr = "the user list shows {string}")]
async fn user_list_shows(world: &mut BkdWorld, username: String) -> Result<()> {
    super::text_contains(world, ".user-list", &username).await
}

#[then(expr = "the user list does not show {string}")]
async fn user_list_does_not_show(world: &mut BkdWorld, username: String) -> Result<()> {
    super::text_lacks(world, ".user-list", &username).await
}

fn user_row(username: &str) -> String {
    format!("//div[contains(@class, 'user-row')][.//span[@class='name' and text()='{username}']]")
}

#[then(expr = "the user list shows {string} as disabled")]
async fn user_list_shows_disabled(world: &mut BkdWorld, username: String) -> Result<()> {
    let row = user_row(&username);
    let driver = world.driver()?;
    world
        .browser()?
        .eventually(&format!("{username} is listed as disabled"), || async {
            let text = driver.find(By::XPath(&row)).await?.text().await?;
            Ok(text.contains("Disabled"))
        })
        .await
}

#[then(expr = "{string} can sign in with the temporary password")]
async fn can_sign_in_with_temporary_password(world: &mut BkdWorld, username: String) -> Result<()> {
    world.api().login(&username, PASSWORD).await?;
    Ok(())
}

#[then(expr = "{string} can sign in with the password {string}")]
async fn can_sign_in_with_password(
    world: &mut BkdWorld,
    username: String,
    password: String,
) -> Result<()> {
    world.api().login(&username, &password).await?;
    Ok(())
}

#[then(expr = "the session of {string} no longer works")]
async fn session_no_longer_works(world: &mut BkdWorld, username: String) -> Result<()> {
    let session = world.session(&username)?;
    let user = world.api().signed_in_user(session).await?;
    ensure!(user.is_none(), "{username} is still signed in");
    Ok(())
}

#[then("I cannot open Manage users")]
async fn cannot_open_manage_users(world: &mut BkdWorld) -> Result<()> {
    world.element(".me strong").await?;
    let links = world
        .driver()?
        .find_all(By::Css(super::MANAGE_USERS_LINK))
        .await?;
    ensure!(links.is_empty(), "a Manage users link is offered");
    Ok(())
}

#[then(expr = "the row of {string} has no Demote, Disable, Delete or Reset password control")]
async fn row_has_no_controls(world: &mut BkdWorld, username: String) -> Result<()> {
    let row = world.element_by(By::XPath(user_row(&username))).await?;
    let controls = row.find_all(By::Css("button, a")).await?;
    ensure!(
        controls.is_empty(),
        "the row offers {} controls",
        controls.len()
    );
    Ok(())
}
