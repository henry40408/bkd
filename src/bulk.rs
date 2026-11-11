//! One action applied to several of a user's bookmarks. The whole batch runs in one
//! transaction: either every bookmark changes or none does.

use std::collections::HashSet;

use anyhow::Result;
use serde::Deserialize;
use sqlx::{QueryBuilder, Sqlite, SqliteConnection, SqlitePool};

use crate::bookmarks::{MAX_TAGS_PER_BOOKMARK, ValidationError, normalize_tags};

pub const MAX_BULK_IDS: usize = 200;

/// What a client sends. It becomes a [`ValidBulk`] only through `validate`.
#[derive(Debug, Deserialize)]
pub struct BulkInput {
    pub ids: Vec<i64>,
    #[serde(flatten)]
    pub action: BulkActionInput,
}

#[derive(Debug, Deserialize)]
#[serde(tag = "action", rename_all = "snake_case")]
pub enum BulkActionInput {
    Delete,
    AddTags { tags: Vec<String> },
    RemoveTags { tags: Vec<String> },
}

#[derive(Debug, PartialEq, Eq)]
pub enum BulkAction {
    Delete,
    AddTags(Vec<String>),
    RemoveTags(Vec<String>),
}

#[derive(Debug, PartialEq, Eq)]
pub struct ValidBulk {
    ids: Vec<i64>,
    action: BulkAction,
}

#[derive(Debug, PartialEq, Eq)]
pub enum BulkOutcome {
    /// How many bookmarks the action changed. A bookmark that already had the tag
    /// (or never had it) is not counted.
    Changed(usize),
    /// An id is missing or belongs to someone else. Nothing was changed.
    NotFound,
    /// Adding the tags would give a bookmark more tags than it may have. Nothing was changed.
    TooManyTags,
}

impl BulkInput {
    pub fn validate(self) -> Result<ValidBulk, ValidationError> {
        let mut seen = HashSet::new();
        let ids: Vec<i64> = self.ids.into_iter().filter(|id| seen.insert(*id)).collect();
        if ids.is_empty() {
            return Err(ValidationError("ids must not be empty"));
        }
        if ids.len() > MAX_BULK_IDS {
            return Err(ValidationError("too many ids"));
        }
        let action = match self.action {
            BulkActionInput::Delete => BulkAction::Delete,
            BulkActionInput::AddTags { tags } => BulkAction::AddTags(non_empty_tags(&tags)?),
            BulkActionInput::RemoveTags { tags } => BulkAction::RemoveTags(non_empty_tags(&tags)?),
        };
        Ok(ValidBulk { ids, action })
    }
}

fn non_empty_tags(raw: &[String]) -> Result<Vec<String>, ValidationError> {
    let tags = normalize_tags(raw)?;
    if tags.is_empty() {
        return Err(ValidationError("tags must not be empty"));
    }
    Ok(tags)
}

pub async fn apply(
    pool: &SqlitePool,
    user_id: i64,
    bulk: &ValidBulk,
    now: i64,
) -> Result<BulkOutcome> {
    // IMMEDIATE takes the write lock up front. A deferred transaction that reads first and writes later fails with SQLITE_BUSY_SNAPSHOT when another connection commits in between.
    let mut transaction = pool.begin_with("BEGIN IMMEDIATE").await?;
    if count_owned(&mut transaction, user_id, &bulk.ids).await? != bulk.ids.len() {
        return Ok(BulkOutcome::NotFound);
    }
    let changed = match &bulk.action {
        BulkAction::Delete => delete(&mut transaction, &bulk.ids).await?,
        BulkAction::AddTags(tags) => add_tags(&mut transaction, &bulk.ids, tags, now).await?,
        BulkAction::RemoveTags(tags) => remove_tags(&mut transaction, &bulk.ids, tags, now).await?,
    };
    if has_too_many_tags(&mut transaction, &bulk.ids).await? {
        return Ok(BulkOutcome::TooManyTags);
    }
    transaction.commit().await?;
    Ok(BulkOutcome::Changed(changed))
}

/// Appends `(?, ?, ...)` with one bound value per id.
fn push_ids(query: &mut QueryBuilder<Sqlite>, ids: &[i64]) {
    query.push("(");
    let mut separated = query.separated(", ");
    for id in ids {
        separated.push_bind(*id);
    }
    query.push(")");
}

async fn count_owned(
    connection: &mut SqliteConnection,
    user_id: i64,
    ids: &[i64],
) -> Result<usize> {
    let mut query = QueryBuilder::<Sqlite>::new("SELECT count(*) FROM bookmarks WHERE user_id = ");
    query.push_bind(user_id);
    query.push(" AND id IN ");
    push_ids(&mut query, ids);
    let count: i64 = query.build_query_scalar().fetch_one(connection).await?;
    Ok(usize::try_from(count)?)
}

async fn delete(connection: &mut SqliteConnection, ids: &[i64]) -> Result<usize> {
    let mut query = QueryBuilder::<Sqlite>::new("DELETE FROM bookmarks WHERE id IN ");
    push_ids(&mut query, ids);
    let result = query.build().execute(connection).await?;
    Ok(usize::try_from(result.rows_affected())?)
}

async fn add_tags(
    connection: &mut SqliteConnection,
    ids: &[i64],
    tags: &[String],
    now: i64,
) -> Result<usize> {
    let mut changed = HashSet::new();
    for tag in tags {
        let mut query = QueryBuilder::<Sqlite>::new(
            "INSERT OR IGNORE INTO bookmark_tags (bookmark_id, tag) SELECT id, ",
        );
        query.push_bind(tag);
        query.push(" FROM bookmarks WHERE id IN ");
        push_ids(&mut query, ids);
        query.push(" RETURNING bookmark_id");
        changed.extend(fetch_ids(connection, query).await?);
    }
    touch(connection, &changed, now).await?;
    Ok(changed.len())
}

async fn remove_tags(
    connection: &mut SqliteConnection,
    ids: &[i64],
    tags: &[String],
    now: i64,
) -> Result<usize> {
    let mut changed = HashSet::new();
    for tag in tags {
        let mut query = QueryBuilder::<Sqlite>::new("DELETE FROM bookmark_tags WHERE tag = ");
        query.push_bind(tag);
        query.push(" AND bookmark_id IN ");
        push_ids(&mut query, ids);
        query.push(" RETURNING bookmark_id");
        changed.extend(fetch_ids(connection, query).await?);
    }
    touch(connection, &changed, now).await?;
    Ok(changed.len())
}

async fn fetch_ids(
    connection: &mut SqliteConnection,
    mut query: QueryBuilder<Sqlite>,
) -> Result<Vec<i64>> {
    Ok(query.build_query_scalar().fetch_all(connection).await?)
}

async fn touch(connection: &mut SqliteConnection, ids: &HashSet<i64>, now: i64) -> Result<()> {
    if ids.is_empty() {
        return Ok(());
    }
    let ids: Vec<i64> = ids.iter().copied().collect();
    let mut query = QueryBuilder::<Sqlite>::new("UPDATE bookmarks SET updated_at = ");
    query.push_bind(now);
    query.push(" WHERE id IN ");
    push_ids(&mut query, &ids);
    query.build().execute(connection).await?;
    Ok(())
}

async fn has_too_many_tags(connection: &mut SqliteConnection, ids: &[i64]) -> Result<bool> {
    let mut query = QueryBuilder::<Sqlite>::new(
        "SELECT EXISTS (SELECT 1 FROM bookmark_tags WHERE bookmark_id IN ",
    );
    push_ids(&mut query, ids);
    query.push(" GROUP BY bookmark_id HAVING count(*) > ");
    query.push_bind(i64::try_from(MAX_TAGS_PER_BOOKMARK)?);
    query.push(")");
    Ok(query.build_query_scalar().fetch_one(connection).await?)
}
