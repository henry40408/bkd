//! Creating the very first account. Two browsers may submit the setup form at the
//! same moment; exactly one must win, and it must become an admin.

use anyhow::Result;
use sqlx::SqlitePool;

pub async fn users_exist(pool: &SqlitePool) -> Result<bool> {
    let exists: bool = sqlx::query_scalar("SELECT EXISTS (SELECT 1 FROM users)")
        .fetch_one(pool)
        .await?;
    Ok(exists)
}

/// Inserts the first admin and returns its id, or `None` when any user already exists.
///
/// The check and the insert are one statement, so two callers cannot both see an
/// empty table. `BEGIN IMMEDIATE` also takes the write lock up front, so a caller
/// waits for a competing writer instead of failing with `SQLITE_BUSY` halfway.
/// Do not split this into "check, then insert".
pub async fn create(
    pool: &SqlitePool,
    username: &str,
    password_hash: &str,
    now: i64,
) -> Result<Option<i64>> {
    let mut transaction = pool.begin_with("BEGIN IMMEDIATE").await?;
    let id: Option<i64> = sqlx::query_scalar(
        "INSERT INTO users (username, password_hash, is_admin, created_at)
         SELECT ?1, ?2, 1, ?3 WHERE NOT EXISTS (SELECT 1 FROM users)
         RETURNING id",
    )
    .bind(username)
    .bind(password_hash)
    .bind(now)
    .fetch_optional(&mut *transaction)
    .await?;
    transaction.commit().await?;
    Ok(id)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::test_support::{insert_user, test_pool};

    #[tokio::test]
    async fn the_first_account_is_an_admin() {
        let (_dir, pool) = test_pool().await;
        assert!(!users_exist(&pool).await.unwrap());

        let id = create(&pool, "henry", "hash", 0).await.unwrap().unwrap();
        assert!(users_exist(&pool).await.unwrap());
        let is_admin: bool = sqlx::query_scalar("SELECT is_admin FROM users WHERE id = ?1")
            .bind(id)
            .fetch_one(&pool)
            .await
            .unwrap();
        assert!(is_admin);
    }

    #[tokio::test]
    async fn nothing_is_created_once_a_user_exists() {
        let (_dir, pool) = test_pool().await;
        insert_user(&pool, "henry").await.unwrap();
        assert_eq!(create(&pool, "mei", "hash", 0).await.unwrap(), None);
        let count: i64 = sqlx::query_scalar("SELECT count(*) FROM users")
            .fetch_one(&pool)
            .await
            .unwrap();
        assert_eq!(count, 1);
    }

    #[tokio::test]
    async fn concurrent_attempts_create_exactly_one_admin() {
        let (_dir, pool) = test_pool().await;
        let attempts = (0..8).map(|index| {
            let pool = pool.clone();
            tokio::spawn(async move { create(&pool, &format!("user{index}"), "hash", 0).await })
        });
        let mut winners = 0;
        for attempt in attempts {
            if attempt.await.unwrap().unwrap().is_some() {
                winners += 1;
            }
        }
        assert_eq!(winners, 1);
    }
}
