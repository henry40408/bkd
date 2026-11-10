//! Connection pool and schema migrations. Every SQL statement lives in this module
//! or in a data-access module, never in a route handler.

use std::path::Path;
use std::time::Duration;

use anyhow::{Context, Result};
use sqlx::SqlitePool;
use sqlx::sqlite::{SqliteConnectOptions, SqliteJournalMode, SqlitePoolOptions, SqliteSynchronous};

/// Opens the database file, creating it if needed, and applies pending migrations.
pub async fn open(path: &Path) -> Result<SqlitePool> {
    let options = SqliteConnectOptions::new()
        .filename(path)
        .create_if_missing(true)
        // WAL lets readers run while one writer commits.
        .journal_mode(SqliteJournalMode::Wal)
        // Safe with WAL: a power cut can lose the last commit but never corrupts the file.
        .synchronous(SqliteSynchronous::Normal)
        // SQLite ignores foreign keys unless every connection asks for them.
        .foreign_keys(true)
        // Wait for a competing writer instead of failing with "database is locked".
        .busy_timeout(Duration::from_secs(5));
    connect(options)
        .await
        .with_context(|| format!("cannot open database {}", path.display()))
}

async fn connect(options: SqliteConnectOptions) -> Result<SqlitePool> {
    let pool = SqlitePoolOptions::new().connect_with(options).await?;
    sqlx::migrate!().run(&pool).await?;
    Ok(pool)
}

#[cfg(test)]
mod tests {
    use super::test_support::{insert_user, tempdir, test_pool};
    use super::*;

    #[tokio::test]
    async fn migrations_create_every_table() {
        let (_dir, pool) = test_pool().await;
        let tables: Vec<String> = sqlx::query_scalar(
            "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE '\\_%' ESCAPE '\\' AND name NOT LIKE 'sqlite\\_%' ESCAPE '\\' ORDER BY name",
        )
        .fetch_all(&pool)
        .await
        .unwrap();
        assert_eq!(tables, ["bookmark_tags", "bookmarks", "sessions", "users"]);
    }

    #[tokio::test]
    async fn open_is_idempotent() {
        let dir = tempdir::TempDir::new();
        let path = dir.path().join("test.sqlite3");
        open(&path).await.unwrap();
        open(&path).await.unwrap();
    }

    #[tokio::test]
    async fn every_connection_enforces_foreign_keys() {
        let (_dir, pool) = test_pool().await;
        // Held at the same time, so the pool has to open more than one connection.
        let mut connections = Vec::new();
        for _ in 0..3 {
            connections.push(pool.acquire().await.unwrap());
        }
        for connection in &mut connections {
            let enabled: i64 = sqlx::query_scalar("PRAGMA foreign_keys")
                .fetch_one(&mut **connection)
                .await
                .unwrap();
            assert_eq!(enabled, 1);
        }
    }

    #[tokio::test]
    async fn bookmark_of_a_missing_user_is_rejected() {
        let (_dir, pool) = test_pool().await;
        let orphan = sqlx::query(
            "INSERT INTO bookmarks (user_id, url, title, created_at, updated_at) VALUES (999, 'u', 't', 0, 0)",
        )
        .execute(&pool)
        .await;
        assert!(orphan.is_err());
    }

    #[tokio::test]
    async fn usernames_are_unique() {
        let (_dir, pool) = test_pool().await;
        insert_user(&pool, "henry").await.unwrap();
        assert!(insert_user(&pool, "henry").await.is_err());
    }

    #[tokio::test]
    async fn usernames_follow_the_allowed_pattern() {
        let (_dir, pool) = test_pool().await;
        for valid in ["abc", "a.b-c_d", "user01", &"a".repeat(32)] {
            insert_user(&pool, valid)
                .await
                .unwrap_or_else(|e| panic!("{valid}: {e}"));
        }
        let too_long = "a".repeat(33);
        for invalid in [
            "",
            "ab",
            "Mei",
            "mé",
            "É",
            "日本語",
            "a b",
            "-abc",
            "abc.",
            "ab/c",
            &too_long,
        ] {
            assert!(
                insert_user(&pool, invalid).await.is_err(),
                "{invalid:?} should be rejected"
            );
        }
    }

    #[tokio::test]
    async fn deleting_a_user_deletes_their_data() {
        let (_dir, pool) = test_pool().await;
        let user_id = insert_user(&pool, "henry").await.unwrap();
        sqlx::query("INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES ('h', ?1, 0, 1)")
            .bind(user_id)
            .execute(&pool)
            .await
            .unwrap();
        let bookmark_id: i64 = sqlx::query_scalar(
            "INSERT INTO bookmarks (user_id, url, title, created_at, updated_at) VALUES (?1, 'u', 't', 0, 0) RETURNING id",
        )
        .bind(user_id)
        .fetch_one(&pool)
        .await
        .unwrap();
        sqlx::query("INSERT INTO bookmark_tags (bookmark_id, tag) VALUES (?1, 'rust')")
            .bind(bookmark_id)
            .execute(&pool)
            .await
            .unwrap();

        sqlx::query("DELETE FROM users WHERE id = ?1")
            .bind(user_id)
            .execute(&pool)
            .await
            .unwrap();

        let remaining: i64 = sqlx::query_scalar(
            "SELECT (SELECT count(*) FROM sessions) + (SELECT count(*) FROM bookmarks) + (SELECT count(*) FROM bookmark_tags)",
        )
        .fetch_one(&pool)
        .await
        .unwrap();
        assert_eq!(remaining, 0);
    }

    #[tokio::test]
    async fn empty_tags_and_bookmark_fields_are_rejected() {
        let (_dir, pool) = test_pool().await;
        let user_id = insert_user(&pool, "henry").await.unwrap();
        let blank_title = sqlx::query(
            "INSERT INTO bookmarks (user_id, url, title, created_at, updated_at) VALUES (?1, 'u', '', 0, 0)",
        )
        .bind(user_id)
        .execute(&pool)
        .await;
        assert!(blank_title.is_err());

        let bookmark_id: i64 = sqlx::query_scalar(
            "INSERT INTO bookmarks (user_id, url, title, created_at, updated_at) VALUES (?1, 'u', 't', 0, 0) RETURNING id",
        )
        .bind(user_id)
        .fetch_one(&pool)
        .await
        .unwrap();
        let blank_tag = sqlx::query("INSERT INTO bookmark_tags (bookmark_id, tag) VALUES (?1, '')")
            .bind(bookmark_id)
            .execute(&pool)
            .await;
        assert!(blank_tag.is_err());
    }

    #[tokio::test]
    async fn columns_reject_values_of_the_wrong_type() {
        let (_dir, pool) = test_pool().await;
        let result = sqlx::query(
            "INSERT INTO users (username, password_hash, created_at) VALUES ('henry', 'x', 'yesterday')",
        )
        .execute(&pool)
        .await;
        assert!(result.is_err());
    }
}

/// Helpers shared by the tests of every module that touches the database.
#[cfg(test)]
pub mod test_support {
    use super::*;

    pub async fn test_pool() -> (tempdir::TempDir, SqlitePool) {
        let dir = tempdir::TempDir::new();
        let pool = open(&dir.path().join("test.sqlite3")).await.unwrap();
        (dir, pool)
    }

    pub async fn insert_user(pool: &SqlitePool, username: &str) -> Result<i64, sqlx::Error> {
        sqlx::query_scalar(
            "INSERT INTO users (username, password_hash, created_at) VALUES (?1, 'x', 0) RETURNING id",
        )
        .bind(username)
        .fetch_one(pool)
        .await
    }

    /// A throwaway directory removed on drop, so the tests need no extra dependency.
    pub mod tempdir {
        use std::path::{Path, PathBuf};
        use std::sync::atomic::{AtomicU32, Ordering};

        pub struct TempDir(PathBuf);

        impl TempDir {
            pub fn new() -> Self {
                static NEXT: AtomicU32 = AtomicU32::new(0);
                let path = std::env::temp_dir().join(format!(
                    "bkd-test-{}-{}",
                    std::process::id(),
                    NEXT.fetch_add(1, Ordering::Relaxed)
                ));
                std::fs::create_dir_all(&path).unwrap();
                Self(path)
            }

            pub fn path(&self) -> &Path {
                &self.0
            }
        }

        impl Drop for TempDir {
            fn drop(&mut self) {
                let _ = std::fs::remove_dir_all(&self.0);
            }
        }
    }
}
