//! A bkd server under test: the real binary, a throwaway database, a free port.
//!
//! The binary is spawned directly, not through `cargo run`, so killing our child
//! kills the server rather than leaving a wrapper process holding the port.

use std::io::{BufRead, BufReader};
use std::net::TcpListener;
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::mpsc;
use std::time::{Duration, Instant};

use anyhow::{Context, Result, bail};

const STARTUP_TIMEOUT: Duration = Duration::from_secs(30);

/// What a scenario needs to reach its server.
#[derive(Debug, Clone)]
pub struct Endpoints {
    pub base_url: String,
    /// The first-run setup token the server printed to its log.
    pub setup_token: String,
    /// The server's database file, for a scenario that must age a row (a confirmation
    /// older than five minutes) instead of waiting.
    pub db_path: PathBuf,
}

/// A running server, stopped (and its database deleted) on drop.
#[derive(Debug)]
pub struct Harness {
    endpoints: Endpoints,
    child: Child,
    // Held for its Drop: removes the directory containing the database.
    _temp: tempfile::TempDir,
}

impl Harness {
    /// Starts a server on an empty database and waits until it answers.
    pub async fn start(binary: &Path) -> Result<Self> {
        let temp = tempfile::Builder::new()
            .prefix("bkd-e2e-")
            .tempdir()
            .context("creating the directory for the test database")?;
        let port = free_port()?;
        let db_path = temp.path().join("test.sqlite3");
        let base_url = format!("http://127.0.0.1:{port}");

        let mut child = Command::new(binary)
            .arg("--db")
            .arg(&db_path)
            .arg("--addr")
            .arg(format!("127.0.0.1:{port}"))
            // No ANSI codes, so the setup token parses cleanly out of the log.
            .env("NO_COLOR", "1")
            .stderr(Stdio::piped())
            .spawn()
            .with_context(|| format!("starting {}", binary.display()))?;
        let token_rx = forward_log_and_find_setup_token(&mut child);

        // Built before the wait, so a server that never answers is still killed.
        let mut harness = Self {
            endpoints: Endpoints {
                base_url: base_url.clone(),
                setup_token: String::new(),
                db_path,
            },
            child,
            _temp: temp,
        };
        wait_until_answering(&base_url, &mut harness.child).await?;
        // The token is logged before the listener binds, so it has arrived by now.
        harness.endpoints.setup_token =
            tokio::task::spawn_blocking(move || token_rx.recv_timeout(Duration::from_secs(5)))
                .await?
                .context("the server did not log a setup token")?;
        Ok(harness)
    }

    pub fn endpoints(&self) -> &Endpoints {
        &self.endpoints
    }
}

impl Drop for Harness {
    fn drop(&mut self) {
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}

/// Echoes the server's log to ours, so startup failures show in the test
/// output, and delivers the setup token it logs.
fn forward_log_and_find_setup_token(child: &mut Child) -> mpsc::Receiver<String> {
    let stderr = child.stderr.take().expect("stderr is piped");
    let (tx, rx) = mpsc::channel();
    std::thread::spawn(move || {
        for line in BufReader::new(stderr).lines().map_while(Result::ok) {
            eprintln!("{line}");
            if let Some((_, token)) = line.split_once("setup token ") {
                let _ = tx.send(token.trim().to_owned());
            }
        }
    });
    rx
}

/// Builds the server binary, so a stale one never hides a regression.
/// A dev build: release is slow to compile and nothing here measures speed.
pub fn build_binary() -> Result<PathBuf> {
    let cargo = std::env::var_os("CARGO").unwrap_or_else(|| "cargo".into());
    let status = Command::new(cargo)
        .current_dir(repo_root())
        .arg("build")
        .status()
        .context("running `cargo build`")?;
    if !status.success() {
        bail!("`cargo build` failed with {status}");
    }
    let target = std::env::var_os("CARGO_TARGET_DIR")
        .map_or_else(|| repo_root().join("target"), PathBuf::from);
    let binary = target.join("debug/bkd");
    if !binary.is_file() {
        bail!("`cargo build` did not produce {}", binary.display());
    }
    Ok(binary)
}

/// An unused TCP port. Racy (released before the server claims it); a clash makes
/// the new server exit, which `wait_until_answering` reports.
fn free_port() -> Result<u16> {
    let listener = TcpListener::bind("127.0.0.1:0").context("probing for a free port")?;
    Ok(listener.local_addr()?.port())
}

/// Waits for the server to answer. Fails at once if our child has exited, so a
/// port clash cannot pass by reaching another scenario's server.
async fn wait_until_answering(base_url: &str, child: &mut Child) -> Result<()> {
    let client = reqwest::Client::new();
    let url = format!("{base_url}/api/state");
    let deadline = Instant::now() + STARTUP_TIMEOUT;
    while Instant::now() < deadline {
        if let Some(status) = child.try_wait()? {
            bail!("the server exited with {status} before it answered");
        }
        if let Ok(response) = client.get(&url).send().await
            && response.status().is_success()
        {
            // The port may belong to another server; ours must still be alive.
            if let Some(status) = child.try_wait()? {
                bail!("the server exited with {status} before it answered");
            }
            return Ok(());
        }
        tokio::time::sleep(Duration::from_millis(100)).await;
    }
    bail!("the server did not answer {url} within {STARTUP_TIMEOUT:?}")
}

/// The repository root: the parent of this crate's directory.
fn repo_root() -> &'static Path {
    Path::new(env!("CARGO_MANIFEST_DIR"))
        .parent()
        .expect("e2e/ always has a parent")
}
