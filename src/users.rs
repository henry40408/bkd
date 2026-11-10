//! Reads of the `users` table that the web layer needs. Account management (create,
//! promote, disable, delete) joins this module in a later step.

use anyhow::Result;
use sqlx::SqlitePool;

pub struct LoginRecord {
    pub id: i64,
    pub password_hash: String,
    pub is_disabled: bool,
}

/// `username` must already be normalized (lowercase), see `auth::username::normalize`.
pub async fn find_for_login(pool: &SqlitePool, username: &str) -> Result<Option<LoginRecord>> {
    let row: Option<(i64, String, Option<i64>)> =
        sqlx::query_as("SELECT id, password_hash, disabled_at FROM users WHERE username = ?1")
            .bind(username)
            .fetch_optional(pool)
            .await?;
    Ok(row.map(|(id, password_hash, disabled_at)| LoginRecord {
        id,
        password_hash,
        is_disabled: disabled_at.is_some(),
    }))
}
