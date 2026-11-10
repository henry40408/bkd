mod db;

use std::path::PathBuf;

use anyhow::Result;
use clap::Parser;

#[derive(Debug, Parser)]
#[command(version, about = "A single-binary bookmark manager")]
struct Args {
    /// Path of the SQLite database file.
    #[arg(long, env = "BKD_DB", default_value = "bkd.sqlite3")]
    db: PathBuf,
}

#[tokio::main]
async fn main() -> Result<()> {
    let args = Args::parse();
    let pool = db::open(&args.db).await?;
    // Closing checkpoints the WAL, so the -wal and -shm files do not outlive the process.
    pool.close().await;
    Ok(())
}
