//! The `users` table: login lookups and account management.
//!
//! Every change that could remove an admin runs in a `BEGIN IMMEDIATE` transaction
//! that first checks another active admin remains. Two admins demoting each other at
//! the same moment are serialized, so the second one is refused and the system never
//! ends up with nobody who can manage it. There is no command-line recovery.

use anyhow::Result;
use sqlx::{Sqlite, SqlitePool, Transaction};

use crate::auth::session;

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

/// What a trusted proxy said about the caller.
pub struct ForwardedAccount<'a> {
    /// Must already be normalized.
    pub username: &'a str,
    /// `Some` when the proxy decides the role (an admin group is configured).
    pub is_admin: Option<bool>,
    pub now: i64,
}

/// The stored hash of an account that has no password. It is not a valid Argon2 string, so
/// no password verifies against it.
const NO_PASSWORD: &str = "!";

/// Finds the account a trusted proxy named, creating it on first sight, and applies the
/// role the proxy decided. `None` means the caller gets no access: the account is disabled.
///
/// When no admin group decides the role, the first account of an empty database is the
/// admin, as in setup. The emptiness check and the insert are one statement inside
/// `BEGIN IMMEDIATE`, so two first requests at once cannot both become admin.
///
/// An account of the same name that has a password is the same person, so it is reused.
pub async fn adopt_forwarded(
    pool: &SqlitePool,
    account: &ForwardedAccount<'_>,
) -> Result<Option<(i64, bool)>> {
    if let Some(found) = find_forwarded(pool, account.username).await? {
        return settle_forwarded(pool, found, account.is_admin).await;
    }
    let mut transaction = pool.begin_with("BEGIN IMMEDIATE").await?;
    let created: Option<(i64, bool)> = sqlx::query_as(
        "INSERT INTO users (username, password_hash, is_admin, created_at)
         VALUES (?1, ?2, COALESCE(?3, NOT EXISTS (SELECT 1 FROM users)), ?4)
         ON CONFLICT (username) DO NOTHING RETURNING id, is_admin",
    )
    .bind(account.username)
    .bind(NO_PASSWORD)
    .bind(account.is_admin)
    .bind(account.now)
    .fetch_optional(&mut *transaction)
    .await?;
    transaction.commit().await?;
    if let Some((id, is_admin)) = created {
        tracing::info!(user_id = id, is_admin, "account created from forward auth");
    }
    let Some(found) = find_forwarded(pool, account.username).await? else {
        return Ok(None);
    };
    settle_forwarded(pool, found, account.is_admin).await
}

struct ForwardedRow {
    id: i64,
    is_admin: bool,
    is_disabled: bool,
}

async fn find_forwarded(pool: &SqlitePool, username: &str) -> Result<Option<ForwardedRow>> {
    let row: Option<(i64, bool, Option<i64>)> =
        sqlx::query_as("SELECT id, is_admin, disabled_at FROM users WHERE username = ?1")
            .bind(username)
            .fetch_optional(pool)
            .await?;
    Ok(row.map(|(id, is_admin, disabled_at)| ForwardedRow {
        id,
        is_admin,
        is_disabled: disabled_at.is_some(),
    }))
}

async fn settle_forwarded(
    pool: &SqlitePool,
    row: ForwardedRow,
    wanted_admin: Option<bool>,
) -> Result<Option<(i64, bool)>> {
    if row.is_disabled {
        return Ok(None);
    }
    let Some(wanted) = wanted_admin.filter(|wanted| *wanted != row.is_admin) else {
        return Ok(Some((row.id, row.is_admin)));
    };
    let is_admin = change_forwarded_role(pool, row.id, wanted).await?;
    Ok(Some((row.id, is_admin)))
}

async fn another_active_admin(pool: &SqlitePool, id: i64) -> Result<bool> {
    let found: bool = sqlx::query_scalar(
        "SELECT EXISTS (SELECT 1 FROM users WHERE id <> ?1 AND is_admin = 1 AND disabled_at IS NULL)",
    )
    .bind(id)
    .fetch_one(pool)
    .await?;
    Ok(found)
}

/// Returns the role the account has afterwards. Like every demotion, this keeps the last
/// active admin: the proxy cannot lock everyone out of user management.
async fn change_forwarded_role(pool: &SqlitePool, id: i64, is_admin: bool) -> Result<bool> {
    // The last admin asks for this on every request while outside the group, so the common
    // refusal is decided without the write lock.
    if !is_admin && !another_active_admin(pool, id).await? {
        return Ok(true);
    }
    let mut transaction = pool.begin_with("BEGIN IMMEDIATE").await?;
    if !is_admin {
        let another_admin: bool = sqlx::query_scalar(
            "SELECT EXISTS (SELECT 1 FROM users WHERE id <> ?1 AND is_admin = 1 AND disabled_at IS NULL)",
        )
        .bind(id)
        .fetch_one(&mut *transaction)
        .await?;
        if !another_admin {
            return Ok(true);
        }
    }
    sqlx::query("UPDATE users SET is_admin = ?1 WHERE id = ?2")
        .bind(is_admin)
        .bind(id)
        .execute(&mut *transaction)
        .await?;
    transaction.commit().await?;
    tracing::info!(user_id = id, is_admin, "role set by forward auth");
    Ok(is_admin)
}

pub async fn username_of(pool: &SqlitePool, id: i64) -> Result<Option<String>> {
    let row: Option<(String,)> = sqlx::query_as("SELECT username FROM users WHERE id = ?1")
        .bind(id)
        .fetch_optional(pool)
        .await?;
    Ok(row.map(|(username,)| username))
}

#[derive(Debug)]
pub struct UserRecord {
    pub id: i64,
    pub username: String,
    pub is_admin: bool,
    pub is_disabled: bool,
    pub created_at: i64,
}

/// What happened to an account change.
#[derive(Debug, PartialEq, Eq)]
pub enum Change {
    Done,
    NotFound,
    /// Refused: it would leave no active admin.
    LastAdmin,
    /// Refused: the caller is no longer an active admin. The route checked this before
    /// a slow password hash; a demotion may have landed since, so it is checked again
    /// inside the transaction.
    CallerNotAdmin,
}

pub struct NewAccount<'a> {
    /// Must already be normalized.
    pub username: &'a str,
    pub password_hash: &'a str,
    pub is_admin: bool,
    pub now: i64,
}

pub async fn list(pool: &SqlitePool) -> Result<Vec<UserRecord>> {
    let rows: Vec<(i64, String, bool, Option<i64>, i64)> = sqlx::query_as(
        "SELECT id, username, is_admin, disabled_at, created_at FROM users ORDER BY username",
    )
    .fetch_all(pool)
    .await?;
    Ok(rows
        .into_iter()
        .map(
            |(id, username, is_admin, disabled_at, created_at)| UserRecord {
                id,
                username,
                is_admin,
                is_disabled: disabled_at.is_some(),
                created_at,
            },
        )
        .collect())
}

/// `Ok(Err(Change::CallerNotAdmin))` when `caller` lost admin power meanwhile;
/// `Ok(Ok(None))` when the username is taken.
pub async fn create(
    pool: &SqlitePool,
    caller: i64,
    account: &NewAccount<'_>,
) -> Result<Result<Option<UserRecord>, Change>> {
    let mut transaction = pool.begin_with("BEGIN IMMEDIATE").await?;
    if !caller_is_active_admin(&mut transaction, caller).await? {
        return Ok(Err(Change::CallerNotAdmin));
    }
    let id: Option<i64> = sqlx::query_scalar(
        "INSERT INTO users (username, password_hash, is_admin, created_at)
         VALUES (?1, ?2, ?3, ?4) ON CONFLICT (username) DO NOTHING RETURNING id",
    )
    .bind(account.username)
    .bind(account.password_hash)
    .bind(account.is_admin)
    .bind(account.now)
    .fetch_optional(&mut *transaction)
    .await?;
    transaction.commit().await?;
    Ok(Ok(id.map(|id| UserRecord {
        id,
        username: account.username.to_owned(),
        is_admin: account.is_admin,
        is_disabled: false,
        created_at: account.now,
    })))
}

pub async fn set_admin(pool: &SqlitePool, caller: i64, id: i64, is_admin: bool) -> Result<Change> {
    let mut transaction = pool.begin_with("BEGIN IMMEDIATE").await?;
    if let Some(refusal) = check(&mut transaction, caller, id, !is_admin).await? {
        return Ok(refusal);
    }
    sqlx::query("UPDATE users SET is_admin = ?1 WHERE id = ?2")
        .bind(is_admin)
        .bind(id)
        .execute(&mut *transaction)
        .await?;
    transaction.commit().await?;
    Ok(Change::Done)
}

/// `disabled_at` is the time to record, or `None` to enable. Disabling also ends every
/// session of the account; enabling does not restore them.
pub async fn set_disabled(
    pool: &SqlitePool,
    caller: i64,
    id: i64,
    disabled_at: Option<i64>,
) -> Result<Change> {
    let mut transaction = pool.begin_with("BEGIN IMMEDIATE").await?;
    if let Some(refusal) = check(&mut transaction, caller, id, disabled_at.is_some()).await? {
        return Ok(refusal);
    }
    sqlx::query("UPDATE users SET disabled_at = ?1 WHERE id = ?2")
        .bind(disabled_at)
        .bind(id)
        .execute(&mut *transaction)
        .await?;
    if disabled_at.is_some() {
        session::revoke_all_for_user(&mut *transaction, id).await?;
    }
    transaction.commit().await?;
    Ok(Change::Done)
}

/// Replaces the password and ends every session, so a stolen session dies with the old password.
pub async fn reset_password(
    pool: &SqlitePool,
    caller: i64,
    id: i64,
    password_hash: &str,
) -> Result<Change> {
    let mut transaction = pool.begin_with("BEGIN IMMEDIATE").await?;
    if let Some(refusal) = check(&mut transaction, caller, id, false).await? {
        return Ok(refusal);
    }
    sqlx::query("UPDATE users SET password_hash = ?1 WHERE id = ?2")
        .bind(password_hash)
        .bind(id)
        .execute(&mut *transaction)
        .await?;
    session::revoke_all_for_user(&mut *transaction, id).await?;
    transaction.commit().await?;
    Ok(Change::Done)
}

/// Replaces a user's own password and ends every session, but only if the stored hash
/// is still `verified_hash`. The caller spent tens of milliseconds in Argon2 checking
/// the current password; a reset or another change in that gap must win. Returns false
/// when the hash changed or the account is gone.
pub async fn change_own_password(
    pool: &SqlitePool,
    id: i64,
    verified_hash: &str,
    new_hash: &str,
) -> Result<bool> {
    let mut transaction = pool.begin_with("BEGIN IMMEDIATE").await?;
    let result = sqlx::query(
        "UPDATE users SET password_hash = ?3
         WHERE id = ?1 AND password_hash = ?2 AND disabled_at IS NULL",
    )
    .bind(id)
    .bind(verified_hash)
    .bind(new_hash)
    .execute(&mut *transaction)
    .await?;
    if result.rows_affected() != 1 {
        return Ok(false);
    }
    session::revoke_all_for_user(&mut *transaction, id).await?;
    transaction.commit().await?;
    Ok(true)
}

/// Deletes the account with its sessions, bookmarks and tags (foreign keys cascade).
pub async fn delete(pool: &SqlitePool, caller: i64, id: i64) -> Result<Change> {
    let mut transaction = pool.begin_with("BEGIN IMMEDIATE").await?;
    if let Some(refusal) = check(&mut transaction, caller, id, true).await? {
        return Ok(refusal);
    }
    sqlx::query("DELETE FROM users WHERE id = ?1")
        .bind(id)
        .execute(&mut *transaction)
        .await?;
    transaction.commit().await?;
    Ok(Change::Done)
}

async fn caller_is_active_admin(
    transaction: &mut Transaction<'_, Sqlite>,
    caller: i64,
) -> Result<bool> {
    let allowed: bool = sqlx::query_scalar(
        "SELECT EXISTS (SELECT 1 FROM users WHERE id = ?1 AND is_admin = 1 AND disabled_at IS NULL)",
    )
    .bind(caller)
    .fetch_one(&mut **transaction)
    .await?;
    Ok(allowed)
}

/// Returns the reason to stop, or `None` to go ahead. `removes_admin` says the change
/// would take away the target's power to manage users. A disabled account does not
/// count as an admin, so only an active admin can be the last one.
async fn check(
    transaction: &mut Transaction<'_, Sqlite>,
    caller: i64,
    id: i64,
    removes_admin: bool,
) -> Result<Option<Change>> {
    if !caller_is_active_admin(transaction, caller).await? {
        return Ok(Some(Change::CallerNotAdmin));
    }
    let row: Option<(bool, Option<i64>)> =
        sqlx::query_as("SELECT is_admin, disabled_at FROM users WHERE id = ?1")
            .bind(id)
            .fetch_optional(&mut **transaction)
            .await?;
    let Some((is_admin, disabled_at)) = row else {
        return Ok(Some(Change::NotFound));
    };
    let is_active_admin = is_admin && disabled_at.is_none();
    if !(removes_admin && is_active_admin) {
        return Ok(None);
    }
    let another_admin: bool = sqlx::query_scalar(
        "SELECT EXISTS (SELECT 1 FROM users WHERE id <> ?1 AND is_admin = 1 AND disabled_at IS NULL)",
    )
    .bind(id)
    .fetch_one(&mut **transaction)
    .await?;
    Ok((!another_admin).then_some(Change::LastAdmin))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::test_support::test_pool;

    async fn add(pool: &SqlitePool, name: &str, is_admin: bool) -> i64 {
        sqlx::query_scalar(
            "INSERT INTO users (username, password_hash, is_admin, created_at)
             VALUES (?1, 'hash', ?2, 0) RETURNING id",
        )
        .bind(name)
        .bind(is_admin)
        .fetch_one(pool)
        .await
        .unwrap()
    }

    fn account(username: &str) -> NewAccount<'_> {
        NewAccount {
            username,
            password_hash: "hash",
            is_admin: false,
            now: 0,
        }
    }

    async fn sessions_of(pool: &SqlitePool, id: i64) -> i64 {
        sqlx::query_scalar("SELECT count(*) FROM sessions WHERE user_id = ?1")
            .bind(id)
            .fetch_one(pool)
            .await
            .unwrap()
    }

    #[tokio::test]
    async fn a_taken_username_is_reported_not_overwritten() {
        let (_dir, pool) = test_pool().await;
        let admin = add(&pool, "henry", true).await;
        let created = create(&pool, admin, &account("henry")).await.unwrap();
        assert!(created.unwrap().is_none());
        assert_eq!(list(&pool).await.unwrap().len(), 1);
    }

    #[tokio::test]
    async fn the_last_admin_cannot_be_demoted_disabled_or_deleted() {
        let (_dir, pool) = test_pool().await;
        let admin = add(&pool, "henry", true).await;
        add(&pool, "mei", false).await;

        assert_eq!(
            set_admin(&pool, admin, admin, false).await.unwrap(),
            Change::LastAdmin
        );
        assert_eq!(
            set_disabled(&pool, admin, admin, Some(1)).await.unwrap(),
            Change::LastAdmin
        );
        assert_eq!(
            delete(&pool, admin, admin).await.unwrap(),
            Change::LastAdmin
        );
        let users = list(&pool).await.unwrap();
        assert!(
            users
                .iter()
                .any(|user| user.id == admin && user.is_admin && !user.is_disabled)
        );
    }

    #[tokio::test]
    async fn a_disabled_admin_does_not_count_as_another_admin() {
        let (_dir, pool) = test_pool().await;
        let first = add(&pool, "henry", true).await;
        let second = add(&pool, "mei", true).await;
        assert_eq!(
            set_disabled(&pool, first, second, Some(1)).await.unwrap(),
            Change::Done
        );

        assert_eq!(
            set_admin(&pool, first, first, false).await.unwrap(),
            Change::LastAdmin
        );
        // Enabling the second admin makes the first one removable again.
        assert_eq!(
            set_disabled(&pool, first, second, None).await.unwrap(),
            Change::Done
        );
        assert_eq!(
            set_admin(&pool, first, first, false).await.unwrap(),
            Change::Done
        );
    }

    #[tokio::test]
    async fn with_two_admins_one_may_go() {
        let (_dir, pool) = test_pool().await;
        let first = add(&pool, "henry", true).await;
        add(&pool, "mei", true).await;
        assert_eq!(delete(&pool, first, first).await.unwrap(), Change::Done);
    }

    #[tokio::test]
    async fn two_admins_removing_each_other_at_once_leave_one() {
        let (_dir, pool) = test_pool().await;
        let first = add(&pool, "henry", true).await;
        let second = add(&pool, "mei", true).await;
        let attempts = [(first, second), (second, first)].map(|(caller, target)| {
            let pool = pool.clone();
            tokio::spawn(async move { set_admin(&pool, caller, target, false).await.unwrap() })
        });
        let mut outcomes = Vec::new();
        for attempt in attempts {
            outcomes.push(attempt.await.unwrap());
        }
        // The loser is refused either as the last admin or because it was just demoted.
        assert_eq!(outcomes.iter().filter(|c| **c == Change::Done).count(), 1);
        let admins = list(&pool)
            .await
            .unwrap()
            .iter()
            .filter(|user| user.is_admin)
            .count();
        assert_eq!(admins, 1);
    }

    #[tokio::test]
    async fn a_caller_who_lost_admin_power_changes_nothing() {
        let (_dir, pool) = test_pool().await;
        let admin = add(&pool, "henry", true).await;
        let demoted = add(&pool, "mei", false).await;
        let disabled = add(&pool, "bob", true).await;
        set_disabled(&pool, admin, disabled, Some(1)).await.unwrap();
        let victim = add(&pool, "sam", false).await;

        for caller in [demoted, disabled] {
            let created = create(&pool, caller, &account("new")).await.unwrap();
            assert_eq!(created.unwrap_err(), Change::CallerNotAdmin);
            assert_eq!(
                reset_password(&pool, caller, victim, "x").await.unwrap(),
                Change::CallerNotAdmin
            );
            assert_eq!(
                set_admin(&pool, caller, victim, true).await.unwrap(),
                Change::CallerNotAdmin
            );
            assert_eq!(
                delete(&pool, caller, victim).await.unwrap(),
                Change::CallerNotAdmin
            );
        }
        assert_eq!(list(&pool).await.unwrap().len(), 4);
    }

    #[tokio::test]
    async fn disabling_and_password_reset_end_every_session() {
        let (_dir, pool) = test_pool().await;
        let admin = add(&pool, "henry", true).await;
        let user = add(&pool, "mei", false).await;
        let other = add(&pool, "bob", false).await;
        for id in [user, user, other] {
            session::create(&pool, &session::SessionStart::new(id, 0))
                .await
                .unwrap();
        }

        assert_eq!(
            reset_password(&pool, admin, user, "new").await.unwrap(),
            Change::Done
        );
        assert_eq!(sessions_of(&pool, user).await, 0);
        assert_eq!(sessions_of(&pool, other).await, 1);

        session::create(&pool, &session::SessionStart::new(user, 0))
            .await
            .unwrap();
        assert_eq!(
            set_disabled(&pool, admin, user, Some(1)).await.unwrap(),
            Change::Done
        );
        assert_eq!(sessions_of(&pool, user).await, 0);
        let hash: String = sqlx::query_scalar("SELECT password_hash FROM users WHERE id = ?1")
            .bind(user)
            .fetch_one(&pool)
            .await
            .unwrap();
        assert_eq!(hash, "new");
    }

    #[tokio::test]
    async fn deleting_a_user_removes_their_bookmarks_and_sessions() {
        let (_dir, pool) = test_pool().await;
        let admin = add(&pool, "henry", true).await;
        let user = add(&pool, "mei", false).await;
        session::create(&pool, &session::SessionStart::new(user, 0))
            .await
            .unwrap();
        sqlx::query(
            "INSERT INTO bookmarks (user_id, url, title, created_at, updated_at)
             VALUES (?1, 'https://a.b', 't', 0, 0)",
        )
        .bind(user)
        .execute(&pool)
        .await
        .unwrap();

        assert_eq!(delete(&pool, admin, user).await.unwrap(), Change::Done);
        let bookmarks: i64 = sqlx::query_scalar("SELECT count(*) FROM bookmarks")
            .fetch_one(&pool)
            .await
            .unwrap();
        assert_eq!(bookmarks, 0);
        assert_eq!(sessions_of(&pool, user).await, 0);
    }

    #[tokio::test]
    async fn changing_an_unknown_user_reports_not_found() {
        let (_dir, pool) = test_pool().await;
        let admin = add(&pool, "henry", true).await;
        assert_eq!(
            set_admin(&pool, admin, 99, true).await.unwrap(),
            Change::NotFound
        );
        assert_eq!(
            set_disabled(&pool, admin, 99, Some(0)).await.unwrap(),
            Change::NotFound
        );
        assert_eq!(
            reset_password(&pool, admin, 99, "x").await.unwrap(),
            Change::NotFound
        );
        assert_eq!(delete(&pool, admin, 99).await.unwrap(), Change::NotFound);
    }
}
