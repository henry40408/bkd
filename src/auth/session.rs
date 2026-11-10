//! Login sessions. The browser holds a random token; the database holds only its
//! SHA-256, so a leaked database cannot be replayed as cookies.

use anyhow::Result;
use sqlx::SqlitePool;

use super::{random_bytes, sha256_hex, to_hex};

pub const SESSION_LIFETIME_SECONDS: i64 = 30 * 24 * 60 * 60;

/// The account a valid session belongs to, read fresh from the database on every
/// request so that a role change or a disable takes effect at once.
#[derive(Debug, PartialEq, Eq)]
pub struct SessionUser {
    pub user_id: i64,
    pub username: String,
    pub is_admin: bool,
}

/// Starts a session and returns the raw token for the cookie.
pub async fn create(pool: &SqlitePool, user_id: i64, now: i64) -> Result<String> {
    let token = to_hex(&random_bytes::<32>());
    sqlx::query("INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES (?1, ?2, ?3, ?4)")
        .bind(sha256_hex(&token))
        .bind(user_id)
        .bind(now)
        .bind(now + SESSION_LIFETIME_SECONDS)
        .execute(pool)
        .await?;
    Ok(token)
}

/// Returns the user for a live session, or `None` when the token is unknown,
/// expired, or belongs to a disabled account.
pub async fn lookup(pool: &SqlitePool, token: &str, now: i64) -> Result<Option<SessionUser>> {
    let row: Option<(i64, String, bool)> = sqlx::query_as(
        "SELECT users.id, users.username, users.is_admin
         FROM sessions JOIN users ON users.id = sessions.user_id
         WHERE sessions.token_hash = ?1 AND sessions.expires_at > ?2 AND users.disabled_at IS NULL",
    )
    .bind(sha256_hex(token))
    .bind(now)
    .fetch_optional(pool)
    .await?;
    Ok(row.map(|(user_id, username, is_admin)| SessionUser {
        user_id,
        username,
        is_admin,
    }))
}

pub async fn revoke(pool: &SqlitePool, token: &str) -> Result<()> {
    sqlx::query("DELETE FROM sessions WHERE token_hash = ?1")
        .bind(sha256_hex(token))
        .execute(pool)
        .await?;
    Ok(())
}

/// Logs a user out everywhere. Use it when the account is disabled, deleted or its
/// password is reset.
#[cfg_attr(
    not(test),
    expect(
        dead_code,
        reason = "user management, which is not built yet, calls it"
    )
)]
pub async fn revoke_all_for_user(pool: &SqlitePool, user_id: i64) -> Result<()> {
    sqlx::query("DELETE FROM sessions WHERE user_id = ?1")
        .bind(user_id)
        .execute(pool)
        .await?;
    Ok(())
}

pub async fn delete_expired(pool: &SqlitePool, now: i64) -> Result<u64> {
    let result = sqlx::query("DELETE FROM sessions WHERE expires_at <= ?1")
        .bind(now)
        .execute(pool)
        .await?;
    Ok(result.rows_affected())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::test_support::{insert_user, test_pool};

    const NOW: i64 = 1_000_000;

    #[tokio::test]
    async fn a_new_session_finds_its_user() {
        let (_dir, pool) = test_pool().await;
        let user_id = insert_user(&pool, "henry").await.unwrap();
        let token = create(&pool, user_id, NOW).await.unwrap();

        let user = lookup(&pool, &token, NOW).await.unwrap().unwrap();
        assert_eq!(
            user,
            SessionUser {
                user_id,
                username: "henry".into(),
                is_admin: false
            }
        );
    }

    #[tokio::test]
    async fn the_database_never_holds_the_raw_token() {
        let (_dir, pool) = test_pool().await;
        let user_id = insert_user(&pool, "henry").await.unwrap();
        let token = create(&pool, user_id, NOW).await.unwrap();

        let stored: String = sqlx::query_scalar("SELECT token_hash FROM sessions")
            .fetch_one(&pool)
            .await
            .unwrap();
        assert_ne!(stored, token);
        assert_eq!(stored, sha256_hex(&token));
    }

    #[tokio::test]
    async fn tokens_are_unique_and_unknown_tokens_fail() {
        let (_dir, pool) = test_pool().await;
        let user_id = insert_user(&pool, "henry").await.unwrap();
        let first = create(&pool, user_id, NOW).await.unwrap();
        let second = create(&pool, user_id, NOW).await.unwrap();
        assert_ne!(first, second);
        assert!(lookup(&pool, "not-a-token", NOW).await.unwrap().is_none());
    }

    #[tokio::test]
    async fn an_expired_session_is_rejected() {
        let (_dir, pool) = test_pool().await;
        let user_id = insert_user(&pool, "henry").await.unwrap();
        let token = create(&pool, user_id, NOW).await.unwrap();

        let last_valid = NOW + SESSION_LIFETIME_SECONDS - 1;
        assert!(lookup(&pool, &token, last_valid).await.unwrap().is_some());
        let expired = NOW + SESSION_LIFETIME_SECONDS;
        assert!(lookup(&pool, &token, expired).await.unwrap().is_none());
    }

    #[tokio::test]
    async fn a_disabled_user_loses_access_immediately() {
        let (_dir, pool) = test_pool().await;
        let user_id = insert_user(&pool, "henry").await.unwrap();
        let token = create(&pool, user_id, NOW).await.unwrap();

        sqlx::query("UPDATE users SET disabled_at = ?1 WHERE id = ?2")
            .bind(NOW)
            .bind(user_id)
            .execute(&pool)
            .await
            .unwrap();
        assert!(lookup(&pool, &token, NOW).await.unwrap().is_none());
    }

    #[tokio::test]
    async fn the_admin_flag_is_read_on_every_lookup() {
        let (_dir, pool) = test_pool().await;
        let user_id = insert_user(&pool, "henry").await.unwrap();
        let token = create(&pool, user_id, NOW).await.unwrap();

        sqlx::query("UPDATE users SET is_admin = 1 WHERE id = ?1")
            .bind(user_id)
            .execute(&pool)
            .await
            .unwrap();
        let user = lookup(&pool, &token, NOW).await.unwrap().unwrap();
        assert!(user.is_admin);
    }

    #[tokio::test]
    async fn revoke_removes_only_that_session() {
        let (_dir, pool) = test_pool().await;
        let user_id = insert_user(&pool, "henry").await.unwrap();
        let kept = create(&pool, user_id, NOW).await.unwrap();
        let revoked = create(&pool, user_id, NOW).await.unwrap();

        revoke(&pool, &revoked).await.unwrap();
        assert!(lookup(&pool, &revoked, NOW).await.unwrap().is_none());
        assert!(lookup(&pool, &kept, NOW).await.unwrap().is_some());
    }

    #[tokio::test]
    async fn revoke_all_removes_every_session_of_one_user() {
        let (_dir, pool) = test_pool().await;
        let henry = insert_user(&pool, "henry").await.unwrap();
        let mei = insert_user(&pool, "mei").await.unwrap();
        let henry_token = create(&pool, henry, NOW).await.unwrap();
        let mei_token = create(&pool, mei, NOW).await.unwrap();

        revoke_all_for_user(&pool, henry).await.unwrap();
        assert!(lookup(&pool, &henry_token, NOW).await.unwrap().is_none());
        assert!(lookup(&pool, &mei_token, NOW).await.unwrap().is_some());
    }

    #[tokio::test]
    async fn delete_expired_keeps_live_sessions() {
        let (_dir, pool) = test_pool().await;
        let user_id = insert_user(&pool, "henry").await.unwrap();
        let old = create(&pool, user_id, NOW).await.unwrap();
        let later = NOW + SESSION_LIFETIME_SECONDS;
        let fresh = create(&pool, user_id, later).await.unwrap();

        assert_eq!(delete_expired(&pool, later).await.unwrap(), 1);
        assert!(lookup(&pool, &old, NOW).await.unwrap().is_none());
        assert!(lookup(&pool, &fresh, later).await.unwrap().is_some());
    }
}
