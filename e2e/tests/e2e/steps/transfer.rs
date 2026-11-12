//! Importing and exporting bookmarks.

use std::io::Write;

use anyhow::{Result, ensure};
use bkd_e2e::world::BkdWorld;
use cucumber::{then, when};
use thirtyfour::prelude::*;

/// What Firefox writes: a folder, two web links (one with a note and a tag) and a
/// smart bookmark that is not a web link.
const BROWSER_FILE: &str = r#"<!DOCTYPE NETSCAPE-Bookmark-file-1>
<META HTTP-EQUIV="Content-Type" CONTENT="text/html; charset=UTF-8">
<TITLE>Bookmarks</TITLE>
<H1>Bookmarks Menu</H1>
<DL><p>
    <DT><H3>Recipes</H3>
    <DL><p>
        <DT><A HREF="https://soup.test/" ADD_DATE="1600000002" TAGS="winter">Tomato soup</A>
        <DD>use less salt
        <DT><A HREF="https://bread.test/" ADD_DATE="1600000003">Fresh bread</A>
        <DT><A HREF="place:parent=1&amp;type=7">Smart bookmark</A>
    </DL><p>
</DL><p>
"#;

#[when("I open Import and export")]
async fn open_import_and_export(world: &mut BkdWorld) -> Result<()> {
    world.click(".me a[href='/transfer']").await?;
    world.element(".import-form").await?;
    Ok(())
}

#[when("I import a bookmarks file exported from a browser")]
async fn import_browser_file(world: &mut BkdWorld) -> Result<()> {
    // The browser reads the file when the form is submitted, so it must outlive the click.
    let mut file = tempfile::Builder::new().suffix(".html").tempfile()?;
    file.write_all(BROWSER_FILE.as_bytes())?;
    let path = file.path().to_string_lossy().into_owned();
    world
        .element(".import-form input[type=file]")
        .await?
        .send_keys(path)
        .await?;
    world.click(".import-form button[type=submit]").await?;
    world.element(".import-result").await?;
    Ok(())
}

#[when("I import without choosing a file")]
async fn import_without_a_file(world: &mut BkdWorld) -> Result<()> {
    world.click(".import-form button[type=submit]").await
}

#[then(expr = "the import result says {string}")]
async fn import_result_says(world: &mut BkdWorld, expected: String) -> Result<()> {
    super::text_contains(world, ".import-result", &expected).await
}

#[then("importing my own export adds nothing new")]
async fn own_export_adds_nothing(world: &mut BkdWorld) -> Result<()> {
    let session = world.my_session()?.clone();
    let before = world.api().bookmarks(&session).await?.len();
    let exported = world.api().export_html(&session).await?;
    let report = world.api().import_html(&session, &exported).await?;
    ensure!(report["added"] == 0, "the import added bookmarks: {report}");
    ensure!(report["invalid"] == 0, "the import refused links: {report}");
    ensure!(
        report["duplicates"] == before,
        "unexpected report: {report}"
    );
    ensure!(world.api().bookmarks(&session).await?.len() == before);
    Ok(())
}

#[then("the download links point to the HTML and JSON exports")]
async fn download_links(world: &mut BkdWorld) -> Result<()> {
    for (label, href) in [
        ("Download HTML", "/api/export?format=html"),
        ("Download JSON", "/api/export?format=json"),
    ] {
        let link = By::XPath(format!("//a[normalize-space(.)='{label}']"));
        let found = world.element_by(link).await?;
        let actual = found.attr("href").await?.unwrap_or_default();
        ensure!(actual == href, "{label} points to {actual:?}");
        ensure!(
            found.attr("download").await?.is_some(),
            "{label} is not a download"
        );
    }
    Ok(())
}
