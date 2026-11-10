//! Admin user management.

use anyhow::{Result, ensure};
use bkd_e2e::api::PASSWORD;
use bkd_e2e::world::BkdWorld;
use cucumber::{given, then, when};
use thirtyfour::prelude::*;

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
    world
        .element_by(By::XPath(super::MANAGE_USERS_LINK))
        .await?
        .click()
        .await?;
    world.element("#users-view").await?;
    Ok(())
}

#[when(expr = "I create the user {string} with a temporary password")]
async fn create_user(world: &mut BkdWorld, username: String) -> Result<()> {
    world.fill("#user-form input[name=name]", &username).await?;
    world
        .fill("#user-form input[name=password]", PASSWORD)
        .await?;
    world.click("#user-form button[type=submit]").await
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
        .click(&format!(
            "button[aria-label='Reset password of {username}']"
        ))
        .await
}

#[when(expr = "I reset the password of {string} to {string}")]
async fn reset_password(world: &mut BkdWorld, username: String, password: String) -> Result<()> {
    start_resetting_password(world, username.clone()).await?;
    world
        .fill(
            &format!("input[aria-label='New password for {username}']"),
            &password,
        )
        .await?;
    world.click(".user-reset button[type=submit]").await?;
    super::text_contains(world, "#toast", "Reset the password").await
}

#[when(expr = "I delete {string}")]
async fn delete_user(world: &mut BkdWorld, username: String) -> Result<()> {
    world
        .click(&format!("button[aria-label='Delete {username}']"))
        .await?;
    world.click(".user-confirm button.danger").await
}

#[then(expr = "the user list shows {string}")]
async fn user_list_shows(world: &mut BkdWorld, username: String) -> Result<()> {
    super::text_contains(world, "#user-list", &username).await
}

#[then(expr = "the user list does not show {string}")]
async fn user_list_does_not_show(world: &mut BkdWorld, username: String) -> Result<()> {
    super::text_lacks(world, "#user-list", &username).await
}

#[then(expr = "the user list shows {string} as disabled")]
async fn user_list_shows_disabled(world: &mut BkdWorld, username: String) -> Result<()> {
    let row = format!("//div[@id='user-list']/div[.//span[@class='name' and text()='{username}']]");
    let driver = world.driver()?;
    world
        .browser()?
        .eventually(&format!("{username} is listed as disabled"), || async {
            let text = driver.find(By::XPath(&row)).await?.text().await?;
            Ok(text.contains("disabled"))
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
    // The sidebar is built once the page knows who is signed in.
    world.element("#me strong").await?;
    let links = world
        .driver()?
        .find_all(By::XPath(super::MANAGE_USERS_LINK))
        .await?;
    ensure!(links.is_empty(), "a Manage users link is offered");
    Ok(())
}

#[then(expr = "the row of {string} has no Demote, Disable, Delete or Reset password button")]
async fn row_has_no_buttons(world: &mut BkdWorld, username: String) -> Result<()> {
    let row = By::XPath(format!(
        "//div[@id='user-list']/div[.//span[@class='name' and text()='{username}']]"
    ));
    let buttons = world
        .element_by(row)
        .await?
        .find_all(By::Tag("button"))
        .await?;
    ensure!(
        buttons.is_empty(),
        "the row offers {} buttons",
        buttons.len()
    );
    Ok(())
}
