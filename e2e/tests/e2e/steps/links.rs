//! Link check progress, which only the script can show.

use anyhow::Result;
use bkd_e2e::world::BkdWorld;
use cucumber::{then, when};
use serde_json::json;

/// Waits before the request is sent, so that Stop can still end it, and the progress
/// line stays on screen long enough to be read.
const SLOW_REQUESTS: &str = "const part = arguments[0]; const slowMs = 2000; \
    const fetchNow = window.fetch; \
    window.fetch = (url, ...rest) => String(url).includes(part) \
      ? new Promise((resolve) => setTimeout(resolve, slowMs)).then(() => fetchNow(url, ...rest)) \
      : fetchNow(url, ...rest);";

#[when(expr = "the server answers requests for {string} slowly")]
async fn slow_requests(world: &mut BkdWorld, part: String) -> Result<()> {
    world
        .driver()?
        .execute(SLOW_REQUESTS, vec![json!(part)])
        .await?;
    Ok(())
}

#[then(expr = "the link check shows {string}")]
async fn link_check_shows(world: &mut BkdWorld, text: String) -> Result<()> {
    super::text_contains(world, ".check-progress", &text).await
}

#[when("I stop the link check")]
async fn stop_link_check(world: &mut BkdWorld) -> Result<()> {
    world.click(".check-progress button").await
}
