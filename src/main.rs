mod auth;
mod bookmarks;
mod bulk;
mod db;
mod import;
mod netscape;
mod search;
mod users;
mod web;

use std::net::SocketAddr;
use std::path::PathBuf;

use anyhow::{Context, Result};
use clap::Parser;
use tokio::net::TcpListener;

use auth::setup_token::SetupToken;
use auth::{first_admin, password};
use web::{AppState, Settings};

#[derive(Debug, Parser)]
#[command(version, about = "A single-binary bookmark manager")]
struct Args {
    /// Path of the SQLite database file.
    #[arg(long, env = "BKD_DB", default_value = "bkd.sqlite3")]
    db: PathBuf,

    /// Address to listen on.
    #[arg(long, env = "BKD_ADDR", default_value = "127.0.0.1:8080")]
    addr: SocketAddr,

    /// Mark the session cookie Secure. Turn it on when the site is served over HTTPS.
    #[arg(long, env = "BKD_SECURE_COOKIES")]
    secure_cookies: bool,
}

#[tokio::main]
async fn main() -> Result<()> {
    tracing_subscriber::fmt()
        .with_writer(std::io::stderr)
        .init();
    let args = Args::parse();

    let pool = db::open(&args.db).await?;
    password::warm_up();
    let setup_token = announce_setup_token(&pool).await?;
    let state = AppState::new(Settings {
        pool: pool.clone(),
        setup_token,
        secure_cookies: args.secure_cookies,
    });

    let listener = TcpListener::bind(args.addr)
        .await
        .with_context(|| format!("cannot listen on {}", args.addr))?;
    tracing::info!("listening on http://{}", listener.local_addr()?);
    web::serve(state, listener).await?;

    // Closing checkpoints the WAL, so the -wal and -shm files do not outlive the process.
    pool.close().await;
    Ok(())
}

/// While there is no user, creates the setup token and writes it to the log, where only
/// someone with access to the server can read it.
async fn announce_setup_token(pool: &sqlx::SqlitePool) -> Result<Option<SetupToken>> {
    if first_admin::users_exist(pool).await? {
        return Ok(None);
    }
    let (token, token_text) = SetupToken::generate();
    tracing::warn!(
        "no users yet: open the site and create the first admin with setup token {token_text}"
    );
    Ok(Some(token))
}
