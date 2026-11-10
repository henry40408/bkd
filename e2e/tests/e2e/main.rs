//! The Cucumber runner (`harness = false`); run `cargo test --test e2e` from `e2e/`.

mod steps;

use bkd_e2e::browser::Browser;
use bkd_e2e::server::build_binary;
use bkd_e2e::world::{BkdWorld, set_binary};
use cucumber::World as _;
use cucumber::writer::Stats as _;

/// Default features path.
const FEATURES: &str = "features";

/// Overrides [`FEATURES`] with one file or directory; cucumber's own CLI
/// is also available (`-- --input`, `--name`, `--tags`), which this variable predates.
const FEATURES_VAR: &str = "BKD_E2E_FEATURES";

/// Upper bound on concurrent scenarios, each with its own server and browser.
const CONCURRENCY_CEILING: usize = 4;

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    set_binary(build_binary()?);
    // Before anything runs in parallel: see `Browser::prepare`.
    Browser::prepare().await?;

    let concurrency = std::thread::available_parallelism()
        .map_or(1, std::num::NonZeroUsize::get)
        .min(CONCURRENCY_CEILING);
    let writer = BkdWorld::cucumber()
        .max_concurrent_scenarios(concurrency)
        .fail_on_skipped()
        .before(|_feature, _rule, _scenario, world| {
            Box::pin(async move {
                world
                    .open_browser()
                    .await
                    .expect("could not open a browser session");
            })
        })
        .after(|_feature, _rule, _scenario, _finished, world| {
            Box::pin(async move {
                if let Some(world) = world {
                    world
                        .close_browser()
                        .await
                        .expect("could not close the session");
                }
            })
        })
        .run(std::env::var(FEATURES_VAR).unwrap_or_else(|_| FEATURES.to_owned()))
        .await;

    let failures = writer.failed_steps() + writer.parsing_errors() + writer.hook_errors();
    anyhow::ensure!(failures == 0, "{failures} cucumber failure(s)");
    Ok(())
}
