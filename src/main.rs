mod auth;
mod background_check;
mod bookmarks;
mod bulk;
mod db;
mod import;
mod linkcheck;
mod netscape;
mod search;
mod users;
mod web;

use std::net::SocketAddr;
use std::path::PathBuf;
use std::time::Duration;

use anyhow::{Context, Result};
use clap::Parser;
use tokio::net::TcpListener;

use auth::forward_auth::{ForwardAuth, ForwardAuthConfig};
use auth::setup_token::SetupToken;
use auth::{first_admin, password};
use background_check::Schedule;
use linkcheck::Checker;
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

    /// Reverse proxies (addresses or CIDR ranges, comma separated) whose user header is
    /// believed, such as Authelia's. Leave empty to turn forward auth off.
    #[arg(long, env = "BKD_FORWARD_AUTH_TRUSTED_PROXIES", value_delimiter = ',')]
    forward_auth_trusted_proxies: Vec<String>,

    /// Header that carries the signed-in user name.
    #[arg(
        long,
        env = "BKD_FORWARD_AUTH_USER_HEADER",
        default_value = "Remote-User"
    )]
    forward_auth_user_header: String,

    /// Header that carries the user's groups, comma separated.
    #[arg(
        long,
        env = "BKD_FORWARD_AUTH_GROUPS_HEADER",
        default_value = "Remote-Groups"
    )]
    forward_auth_groups_header: String,

    /// Members of this group are admins; everyone else is a member. Without it the
    /// roles stay as an admin sets them in bkd.
    #[arg(long, env = "BKD_FORWARD_AUTH_ADMIN_GROUP")]
    forward_auth_admin_group: Option<String>,

    /// Seconds between rounds of the background link check. 0 turns it off. Even when it
    /// is on, it only checks the bookmarks of users who chose that in their settings.
    #[arg(
        long,
        env = "BKD_LINK_CHECK_INTERVAL",
        default_value_t = 0,
        value_parser = clap::value_parser!(u64).range(0..=30 * 24 * 60 * 60)
    )]
    link_check_interval: u64,

    /// Bookmarks the background link check visits per round, all users together.
    #[arg(
        long,
        env = "BKD_LINK_CHECK_BATCH",
        default_value_t = 20,
        value_parser = clap::value_parser!(u32).range(1..=200)
    )]
    link_check_batch: u32,
}

impl Args {
    fn link_check_schedule(&self) -> Option<Schedule> {
        (self.link_check_interval > 0).then(|| Schedule {
            interval: Duration::from_secs(self.link_check_interval),
            batch: self.link_check_batch as usize,
        })
    }

    fn forward_auth(&self) -> Result<Option<ForwardAuth>> {
        if self.forward_auth_trusted_proxies.is_empty() {
            return Ok(None);
        }
        let forward_auth = ForwardAuth::new(ForwardAuthConfig {
            trusted_proxies: self.forward_auth_trusted_proxies.clone(),
            user_header: self.forward_auth_user_header.clone(),
            groups_header: self.forward_auth_groups_header.clone(),
            admin_group: self.forward_auth_admin_group.clone(),
        })?;
        Ok(Some(forward_auth))
    }
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
    let link_checker = Checker::public_only()?;
    let schedule = args.link_check_schedule();
    let state = AppState::new(Settings {
        pool: pool.clone(),
        setup_token,
        secure_cookies: args.secure_cookies,
        forward_auth: args
            .forward_auth()
            .context("invalid forward auth setting")?,
        link_checker: link_checker.clone(),
        background_link_check: schedule.is_some(),
    });
    let background_check = schedule
        .map(|schedule| tokio::spawn(background_check::run(pool.clone(), link_checker, schedule)));

    let listener = TcpListener::bind(args.addr)
        .await
        .with_context(|| format!("cannot listen on {}", args.addr))?;
    tracing::info!("listening on http://{}", listener.local_addr()?);
    web::serve(state, listener).await?;
    if let Some(task) = background_check {
        task.abort();
    }

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
