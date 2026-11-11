//! Bookmarks and their tags. Every function takes the owner's `user_id` and puts it in
//! the SQL, so a bookmark id that belongs to someone else behaves as if it did not exist.

use std::collections::HashSet;
use std::fmt;

use anyhow::Result;
use serde::{Deserialize, Serialize};
use sqlx::{QueryBuilder, Sqlite, SqlitePool};

use crate::search::{self, Filter};

const MAX_URL_CHARS: usize = 2048;
const MAX_TITLE_CHARS: usize = 300;
const MAX_NOTE_CHARS: usize = 5000;
const MAX_TAG_CHARS: usize = 50;
pub const MAX_TAGS_PER_BOOKMARK: usize = 20;

// Joins tags inside SQL. Tags are rejected if they contain control characters, so
// this byte can never appear in one.
const TAG_SEPARATOR: char = '\u{1f}';

pub const DEFAULT_PAGE_SIZE: u32 = 50;
pub const MAX_PAGE_SIZE: u32 = 200;

#[derive(Debug, PartialEq, Eq, Serialize)]
pub struct Bookmark {
    pub id: i64,
    pub url: String,
    pub title: String,
    pub note: String,
    pub tags: Vec<String>,
    pub created_at: i64,
    pub updated_at: i64,
    /// `None` until the link is checked, and again after the URL changes.
    pub link: Option<LinkCheck>,
}

/// The result of the last link check.
#[derive(Debug, PartialEq, Eq, Serialize)]
pub struct LinkCheck {
    /// `ok`, `redirected`, `broken` or `unknown`.
    pub status: String,
    pub checked_at: i64,
    pub detail: String,
}

#[derive(Debug, PartialEq, Eq, Serialize, sqlx::FromRow)]
pub struct TagCount {
    pub tag: String,
    pub count: i64,
}

#[derive(Debug, Clone, Copy)]
pub struct Page {
    pub limit: u32,
    pub offset: u32,
}

impl Page {
    /// Applies the defaults and caps the size, so a client cannot ask for everything.
    pub fn new(limit: Option<u32>, offset: Option<u32>) -> Self {
        Self {
            limit: limit.unwrap_or(DEFAULT_PAGE_SIZE).clamp(1, MAX_PAGE_SIZE),
            offset: offset.unwrap_or(0),
        }
    }
}

/// What a client sends. It becomes a [`ValidBookmark`] only through `validate`.
#[derive(Debug, Deserialize)]
pub struct BookmarkInput {
    pub url: String,
    pub title: String,
    #[serde(default)]
    pub note: String,
    #[serde(default)]
    pub tags: Vec<String>,
}

/// Input that passed every rule. Only this type can be stored.
#[derive(Debug, PartialEq, Eq)]
pub struct ValidBookmark {
    url: String,
    title: String,
    note: String,
    tags: Vec<String>,
}

#[derive(Debug, PartialEq, Eq)]
pub struct ValidationError(pub &'static str);

impl fmt::Display for ValidationError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(self.0)
    }
}

impl std::error::Error for ValidationError {}

impl BookmarkInput {
    pub fn validate(self) -> Result<ValidBookmark, ValidationError> {
        Ok(ValidBookmark {
            url: validate_url(&self.url)?,
            title: validate_title(&self.title)?,
            note: validate_note(&self.note)?,
            tags: normalize_tags(&self.tags)?,
        })
    }
}

impl BookmarkInput {
    /// For an imported file, where one long title must not cost the whole bookmark:
    /// shortens what is too long, falls back to the URL for a missing title and drops
    /// tags that cannot be kept. The URL is left alone, because a changed URL would
    /// point somewhere else.
    pub fn fitted(url: &str, title: &str, note: &str, tags: &[String]) -> Self {
        let title = title.trim();
        let title = if title.is_empty() { url.trim() } else { title };
        let mut kept = Vec::new();
        for tag in tags {
            if let Ok(Some(tag)) = normalize_tag(tag)
                && !kept.contains(&tag)
            {
                kept.push(tag);
            }
        }
        kept.truncate(MAX_TAGS_PER_BOOKMARK);
        Self {
            url: url.to_owned(),
            title: title.chars().take(MAX_TITLE_CHARS).collect(),
            note: note.chars().take(MAX_NOTE_CHARS).collect(),
            tags: kept,
        }
    }
}

fn validate_url(raw: &str) -> Result<String, ValidationError> {
    let url = raw.trim();
    if url.chars().count() > MAX_URL_CHARS {
        return Err(ValidationError("url is too long"));
    }
    // Only web links: the front end turns this into an <a href>, and a `javascript:`
    // URL there would run script.
    let lowered = url.to_ascii_lowercase();
    let rest = lowered
        .strip_prefix("https://")
        .or_else(|| lowered.strip_prefix("http://"));
    let Some(rest) = rest else {
        return Err(ValidationError("url must start with http:// or https://"));
    };
    if rest.is_empty() || url.chars().any(|c| c.is_whitespace() || c.is_control()) {
        return Err(ValidationError("url is not valid"));
    }
    Ok(url.to_owned())
}

fn validate_title(raw: &str) -> Result<String, ValidationError> {
    let title = raw.trim();
    if title.is_empty() {
        return Err(ValidationError("title is required"));
    }
    if title.chars().count() > MAX_TITLE_CHARS {
        return Err(ValidationError("title is too long"));
    }
    Ok(title.to_owned())
}

fn validate_note(raw: &str) -> Result<String, ValidationError> {
    let note = raw.trim_end();
    if note.chars().count() > MAX_NOTE_CHARS {
        return Err(ValidationError("note is too long"));
    }
    Ok(note.to_owned())
}

/// Trims and lowercases tags, drops empty ones and duplicates, keeps the first order.
/// Lowercasing here, not in SQL, because SQLite folds only ASCII.
pub fn normalize_tag(raw: &str) -> Result<Option<String>, ValidationError> {
    let tag = raw.trim().to_lowercase();
    if tag.is_empty() {
        return Ok(None);
    }
    if tag.chars().count() > MAX_TAG_CHARS {
        return Err(ValidationError("tag is too long"));
    }
    if tag.chars().any(char::is_control) {
        return Err(ValidationError("tag contains a control character"));
    }
    Ok(Some(tag))
}

pub fn normalize_tags(raw_tags: &[String]) -> Result<Vec<String>, ValidationError> {
    let mut seen = HashSet::new();
    let mut tags = Vec::new();
    for raw in raw_tags {
        let Some(tag) = normalize_tag(raw)? else {
            continue;
        };
        if seen.insert(tag.clone()) {
            tags.push(tag);
        }
    }
    if tags.len() > MAX_TAGS_PER_BOOKMARK {
        return Err(ValidationError("too many tags"));
    }
    Ok(tags)
}

#[derive(sqlx::FromRow)]
struct BookmarkRow {
    id: i64,
    url: String,
    title: String,
    note: String,
    created_at: i64,
    updated_at: i64,
    link_status: Option<String>,
    link_checked_at: Option<i64>,
    link_detail: Option<String>,
    tags: Option<String>,
}

impl From<BookmarkRow> for Bookmark {
    fn from(row: BookmarkRow) -> Self {
        Self {
            id: row.id,
            url: row.url,
            title: row.title,
            note: row.note,
            tags: row
                .tags
                .map(|joined| joined.split(TAG_SEPARATOR).map(str::to_owned).collect())
                .unwrap_or_default(),
            created_at: row.created_at,
            updated_at: row.updated_at,
            link: row.link_status.map(|status| LinkCheck {
                status,
                checked_at: row.link_checked_at.unwrap_or_default(),
                detail: row.link_detail.unwrap_or_default(),
            }),
        }
    }
}

// The tags come from a sub-select so one query returns everything the list needs.
const SELECT_BOOKMARK: &str = "SELECT bookmarks.id, bookmarks.url, bookmarks.title, bookmarks.note,
    bookmarks.created_at, bookmarks.updated_at,
    bookmarks.link_status, bookmarks.link_checked_at, bookmarks.link_detail,
    (SELECT group_concat(tag, char(31))
     FROM (SELECT tag FROM bookmark_tags WHERE bookmark_id = bookmarks.id ORDER BY tag)) AS tags
    FROM bookmarks";

pub async fn create(
    pool: &SqlitePool,
    user_id: i64,
    bookmark: &ValidBookmark,
    now: i64,
) -> Result<Bookmark> {
    let mut transaction = pool.begin().await?;
    let id: i64 = sqlx::query_scalar(
        "INSERT INTO bookmarks (user_id, url, title, note, created_at, updated_at)
         VALUES (?1, ?2, ?3, ?4, ?5, ?5) RETURNING id",
    )
    .bind(user_id)
    .bind(&bookmark.url)
    .bind(&bookmark.title)
    .bind(&bookmark.note)
    .bind(now)
    .fetch_one(&mut *transaction)
    .await?;
    insert_tags(&mut transaction, id, &bookmark.tags).await?;
    transaction.commit().await?;
    // The owner could delete it in this gap, so a missing row is an error, not a bug.
    get(pool, user_id, id)
        .await?
        .ok_or_else(|| anyhow::anyhow!("bookmark {id} was deleted right after it was created"))
}

/// Replaces every field and the whole tag set. Returns `None` when the bookmark does
/// not exist or belongs to someone else.
pub async fn update(
    pool: &SqlitePool,
    user_id: i64,
    id: i64,
    bookmark: &ValidBookmark,
    now: i64,
) -> Result<Option<Bookmark>> {
    let mut transaction = pool.begin().await?;
    let changed = sqlx::query(
        // A check describes one address, so it is dropped when the URL changes. In SET,
        // `url` is still the old value.
        "UPDATE bookmarks SET
             link_status     = CASE WHEN url = ?1 THEN link_status     END,
             link_checked_at = CASE WHEN url = ?1 THEN link_checked_at END,
             link_detail     = CASE WHEN url = ?1 THEN link_detail     END,
             url = ?1, title = ?2, note = ?3, updated_at = ?4
         WHERE id = ?5 AND user_id = ?6",
    )
    .bind(&bookmark.url)
    .bind(&bookmark.title)
    .bind(&bookmark.note)
    .bind(now)
    .bind(id)
    .bind(user_id)
    .execute(&mut *transaction)
    .await?
    .rows_affected();
    if changed == 0 {
        return Ok(None);
    }
    sqlx::query("DELETE FROM bookmark_tags WHERE bookmark_id = ?1")
        .bind(id)
        .execute(&mut *transaction)
        .await?;
    insert_tags(&mut transaction, id, &bookmark.tags).await?;
    transaction.commit().await?;
    get(pool, user_id, id).await
}

async fn insert_tags(
    transaction: &mut sqlx::SqliteConnection,
    bookmark_id: i64,
    tags: &[String],
) -> Result<()> {
    for tag in tags {
        sqlx::query("INSERT INTO bookmark_tags (bookmark_id, tag) VALUES (?1, ?2)")
            .bind(bookmark_id)
            .bind(tag)
            .execute(&mut *transaction)
            .await?;
    }
    Ok(())
}

/// Returns false when the bookmark does not exist or belongs to someone else.
pub async fn delete(pool: &SqlitePool, user_id: i64, id: i64) -> Result<bool> {
    let result = sqlx::query("DELETE FROM bookmarks WHERE id = ?1 AND user_id = ?2")
        .bind(id)
        .bind(user_id)
        .execute(pool)
        .await?;
    Ok(result.rows_affected() > 0)
}

pub async fn get(pool: &SqlitePool, user_id: i64, id: i64) -> Result<Option<Bookmark>> {
    let mut query = QueryBuilder::<Sqlite>::new(SELECT_BOOKMARK);
    query.push(" WHERE bookmarks.user_id = ");
    query.push_bind(user_id);
    query.push(" AND bookmarks.id = ");
    query.push_bind(id);
    let row: Option<BookmarkRow> = query.build_query_as().fetch_optional(pool).await?;
    Ok(row.map(Bookmark::from))
}

/// Newest first. `filter` narrows by search terms and tag.
pub async fn list(
    pool: &SqlitePool,
    user_id: i64,
    filter: &Filter,
    page: Page,
) -> Result<Vec<Bookmark>> {
    let mut query = QueryBuilder::<Sqlite>::new(SELECT_BOOKMARK);
    query.push(" WHERE bookmarks.user_id = ");
    query.push_bind(user_id);
    search::push_conditions(&mut query, filter);
    query.push(" ORDER BY bookmarks.created_at DESC, bookmarks.id DESC LIMIT ");
    query.push_bind(i64::from(page.limit));
    query.push(" OFFSET ");
    query.push_bind(i64::from(page.offset));
    let rows: Vec<BookmarkRow> = query.build_query_as().fetch_all(pool).await?;
    Ok(rows.into_iter().map(Bookmark::from).collect())
}

/// One bookmark chosen at random from those `filter` lets through.
pub async fn random(pool: &SqlitePool, user_id: i64, filter: &Filter) -> Result<Option<Bookmark>> {
    let mut query = QueryBuilder::<Sqlite>::new(SELECT_BOOKMARK);
    query.push(" WHERE bookmarks.user_id = ");
    query.push_bind(user_id);
    search::push_conditions(&mut query, filter);
    query.push(" ORDER BY RANDOM() LIMIT 1");
    let row: Option<BookmarkRow> = query.build_query_as().fetch_optional(pool).await?;
    Ok(row.map(Bookmark::from))
}

/// Every tag the user has used, with how many bookmarks carry it.
pub async fn tag_counts(pool: &SqlitePool, user_id: i64) -> Result<Vec<TagCount>> {
    let counts = sqlx::query_as(
        "SELECT bookmark_tags.tag AS tag, count(*) AS count
         FROM bookmark_tags JOIN bookmarks ON bookmarks.id = bookmark_tags.bookmark_id
         WHERE bookmarks.user_id = ?1
         GROUP BY bookmark_tags.tag ORDER BY bookmark_tags.tag",
    )
    .bind(user_id)
    .fetch_all(pool)
    .await?;
    Ok(counts)
}

/// A bookmark from an import file, with the time it was first saved.
pub struct ImportEntry {
    pub bookmark: ValidBookmark,
    pub created_at: i64,
}

/// Adds the entries in one transaction, so a failure leaves nothing behind. A URL the
/// user already has, or that appears twice in the file, is skipped. Returns how many
/// were added.
pub async fn import(pool: &SqlitePool, user_id: i64, entries: &[ImportEntry]) -> Result<usize> {
    // Immediate, so the read below cannot go stale and fail to upgrade to a writer.
    let mut transaction = pool.begin_with("BEGIN IMMEDIATE").await?;
    let existing: Vec<String> = sqlx::query_scalar("SELECT url FROM bookmarks WHERE user_id = ?1")
        .bind(user_id)
        .fetch_all(&mut *transaction)
        .await?;
    let mut seen: HashSet<String> = existing.into_iter().collect();
    let mut added = 0;
    for entry in entries {
        if !seen.insert(entry.bookmark.url.clone()) {
            continue;
        }
        let id: i64 = sqlx::query_scalar(
            "INSERT INTO bookmarks (user_id, url, title, note, created_at, updated_at)
             VALUES (?1, ?2, ?3, ?4, ?5, ?5) RETURNING id",
        )
        .bind(user_id)
        .bind(&entry.bookmark.url)
        .bind(&entry.bookmark.title)
        .bind(&entry.bookmark.note)
        .bind(entry.created_at)
        .fetch_one(&mut *transaction)
        .await?;
        insert_tags(&mut transaction, id, &entry.bookmark.tags).await?;
        added += 1;
    }
    transaction.commit().await?;
    Ok(added)
}

/// Every bookmark the user has, oldest first, so a file read back in keeps the order.
pub async fn all(pool: &SqlitePool, user_id: i64) -> Result<Vec<Bookmark>> {
    let mut query = QueryBuilder::<Sqlite>::new(SELECT_BOOKMARK);
    query.push(" WHERE bookmarks.user_id = ");
    query.push_bind(user_id);
    query.push(" ORDER BY bookmarks.created_at, bookmarks.id");
    let rows: Vec<BookmarkRow> = query.build_query_as().fetch_all(pool).await?;
    Ok(rows.into_iter().map(Bookmark::from).collect())
}

#[cfg(test)]
pub mod test_support {
    use super::*;

    /// A bookmark with the given title and tags and a URL built from the title.
    pub fn valid(title: &str, tags: &[&str]) -> ValidBookmark {
        BookmarkInput {
            url: format!("https://example.com/{}", title.replace(' ', "-")),
            title: title.to_owned(),
            note: String::new(),
            tags: tags.iter().map(|tag| (*tag).to_owned()).collect(),
        }
        .validate()
        .unwrap()
    }
}

#[cfg(test)]
mod tests {
    use super::test_support::valid;
    use super::*;
    use crate::db::test_support::{insert_user, test_pool};

    fn input(url: &str, title: &str) -> BookmarkInput {
        BookmarkInput {
            url: url.to_owned(),
            title: title.to_owned(),
            note: String::new(),
            tags: Vec::new(),
        }
    }

    fn everything() -> Filter {
        Filter::default()
    }

    fn page() -> Page {
        Page::new(None, None)
    }

    #[test]
    fn only_web_urls_are_accepted() {
        for good in [
            "https://example.com",
            "HTTP://Example.com/a?b=c#d",
            "  https://a.b  ",
        ] {
            assert!(input(good, "t").validate().is_ok(), "{good:?}");
        }
        for bad in [
            "",
            "example.com",
            "javascript:alert(1)",
            "data:text/html,x",
            "ftp://example.com",
            "https://",
            "https://exa mple.com",
            "https://a.com/\nb",
        ] {
            assert!(input(bad, "t").validate().is_err(), "{bad:?}");
        }
    }

    #[test]
    fn the_url_is_trimmed_and_the_title_is_required() {
        let ok = input("  https://a.b  ", "  Title  ").validate().unwrap();
        assert_eq!(
            (ok.url.as_str(), ok.title.as_str()),
            ("https://a.b", "Title")
        );
        assert!(input("https://a.b", "   ").validate().is_err());
    }

    #[test]
    fn lengths_are_limited() {
        let long_url = format!("https://a.b/{}", "x".repeat(MAX_URL_CHARS));
        assert!(input(&long_url, "t").validate().is_err());
        assert!(
            input("https://a.b", &"t".repeat(MAX_TITLE_CHARS + 1))
                .validate()
                .is_err()
        );
        let mut long_note = input("https://a.b", "t");
        long_note.note = "n".repeat(MAX_NOTE_CHARS + 1);
        assert!(long_note.validate().is_err());
    }

    #[test]
    fn tags_are_lowercased_trimmed_and_deduplicated() {
        let mut bookmark = input("https://a.b", "t");
        bookmark.tags = ["Rust", " rust ", "ÉCOLE", "", "  ", "日本語", "Go"]
            .map(String::from)
            .to_vec();
        assert_eq!(
            bookmark.validate().unwrap().tags,
            ["rust", "école", "日本語", "go"]
        );
    }

    #[test]
    fn bad_tags_are_rejected() {
        let mut too_long = input("https://a.b", "t");
        too_long.tags = vec!["x".repeat(MAX_TAG_CHARS + 1)];
        assert!(too_long.validate().is_err());

        let mut control = input("https://a.b", "t");
        control.tags = vec!["a\u{1f}b".to_owned()];
        assert!(control.validate().is_err());

        let mut many = input("https://a.b", "t");
        many.tags = (0..=MAX_TAGS_PER_BOOKMARK)
            .map(|n| format!("tag{n}"))
            .collect();
        assert!(many.validate().is_err());
    }

    #[test]
    fn page_size_is_capped() {
        assert_eq!(Page::new(None, None).limit, DEFAULT_PAGE_SIZE);
        assert_eq!(Page::new(Some(0), None).limit, 1);
        assert_eq!(Page::new(Some(10_000), None).limit, MAX_PAGE_SIZE);
    }

    #[tokio::test]
    async fn a_created_bookmark_reads_back_with_sorted_tags() {
        let (_dir, pool) = test_pool().await;
        let user = insert_user(&pool, "henry").await.unwrap();
        let created = create(&pool, user, &valid("Rust book", &["rust", "book"]), 100)
            .await
            .unwrap();

        assert_eq!(created.tags, ["book", "rust"]);
        assert_eq!((created.created_at, created.updated_at), (100, 100));
        assert_eq!(get(&pool, user, created.id).await.unwrap(), Some(created));
    }

    #[tokio::test]
    async fn a_bookmark_without_tags_has_an_empty_list() {
        let (_dir, pool) = test_pool().await;
        let user = insert_user(&pool, "henry").await.unwrap();
        let created = create(&pool, user, &valid("Plain", &[]), 100)
            .await
            .unwrap();
        assert!(created.tags.is_empty());
    }

    #[tokio::test]
    async fn update_replaces_fields_and_tags_and_keeps_created_at() {
        let (_dir, pool) = test_pool().await;
        let user = insert_user(&pool, "henry").await.unwrap();
        let created = create(&pool, user, &valid("Old", &["a", "b"]), 100)
            .await
            .unwrap();

        let updated = update(&pool, user, created.id, &valid("New", &["c"]), 200)
            .await
            .unwrap()
            .unwrap();
        assert_eq!(updated.title, "New");
        assert_eq!(updated.tags, ["c"]);
        assert_eq!((updated.created_at, updated.updated_at), (100, 200));
    }

    #[tokio::test]
    async fn another_users_bookmark_cannot_be_read_changed_or_deleted() {
        let (_dir, pool) = test_pool().await;
        let owner = insert_user(&pool, "henry").await.unwrap();
        let other = insert_user(&pool, "mei").await.unwrap();
        let created = create(&pool, owner, &valid("Mine", &["x"]), 100)
            .await
            .unwrap();

        assert_eq!(get(&pool, other, created.id).await.unwrap(), None);
        let stolen = update(&pool, other, created.id, &valid("Hijacked", &[]), 200).await;
        assert_eq!(stolen.unwrap(), None);
        assert!(!delete(&pool, other, created.id).await.unwrap());

        let untouched = get(&pool, owner, created.id).await.unwrap().unwrap();
        assert_eq!(
            (untouched.title.as_str(), untouched.tags.as_slice()),
            ("Mine", ["x".to_owned()].as_slice())
        );
    }

    #[tokio::test]
    async fn delete_removes_the_bookmark_and_its_tags() {
        let (_dir, pool) = test_pool().await;
        let user = insert_user(&pool, "henry").await.unwrap();
        let created = create(&pool, user, &valid("Gone", &["t"]), 100)
            .await
            .unwrap();

        assert!(delete(&pool, user, created.id).await.unwrap());
        assert!(!delete(&pool, user, created.id).await.unwrap());
        assert!(tag_counts(&pool, user).await.unwrap().is_empty());
    }

    #[tokio::test]
    async fn list_is_newest_first_and_private_to_the_owner() {
        let (_dir, pool) = test_pool().await;
        let henry = insert_user(&pool, "henry").await.unwrap();
        let mei = insert_user(&pool, "mei").await.unwrap();
        create(&pool, henry, &valid("first", &[]), 100)
            .await
            .unwrap();
        create(&pool, henry, &valid("second", &[]), 200)
            .await
            .unwrap();
        create(&pool, mei, &valid("hers", &[]), 150).await.unwrap();

        let titles: Vec<_> = list(&pool, henry, &everything(), page())
            .await
            .unwrap()
            .into_iter()
            .map(|bookmark| bookmark.title)
            .collect();
        assert_eq!(titles, ["second", "first"]);
    }

    #[tokio::test]
    async fn bookmarks_made_in_the_same_second_keep_a_stable_order() {
        let (_dir, pool) = test_pool().await;
        let user = insert_user(&pool, "henry").await.unwrap();
        for title in ["a", "b", "c"] {
            create(&pool, user, &valid(title, &[]), 100).await.unwrap();
        }
        let titles: Vec<_> = list(&pool, user, &everything(), page())
            .await
            .unwrap()
            .into_iter()
            .map(|bookmark| bookmark.title)
            .collect();
        assert_eq!(titles, ["c", "b", "a"]);
    }

    #[tokio::test]
    async fn pages_do_not_overlap() {
        let (_dir, pool) = test_pool().await;
        let user = insert_user(&pool, "henry").await.unwrap();
        for n in 0..5 {
            create(&pool, user, &valid(&format!("bookmark {n}"), &[]), n)
                .await
                .unwrap();
        }
        let first = list(&pool, user, &everything(), Page::new(Some(2), Some(0)))
            .await
            .unwrap();
        let second = list(&pool, user, &everything(), Page::new(Some(2), Some(2)))
            .await
            .unwrap();
        let third = list(&pool, user, &everything(), Page::new(Some(2), Some(4)))
            .await
            .unwrap();
        let ids: Vec<_> = first
            .iter()
            .chain(&second)
            .chain(&third)
            .map(|b| b.id)
            .collect();
        assert_eq!(ids.len(), 5);
        assert_eq!(ids.iter().collect::<HashSet<_>>().len(), 5);
    }

    #[tokio::test]
    async fn tag_counts_cover_only_the_owner() {
        let (_dir, pool) = test_pool().await;
        let henry = insert_user(&pool, "henry").await.unwrap();
        let mei = insert_user(&pool, "mei").await.unwrap();
        create(&pool, henry, &valid("one", &["rust", "web"]), 1)
            .await
            .unwrap();
        create(&pool, henry, &valid("two", &["rust"]), 2)
            .await
            .unwrap();
        create(&pool, mei, &valid("three", &["secret"]), 3)
            .await
            .unwrap();

        let counts = tag_counts(&pool, henry).await.unwrap();
        let pairs: Vec<_> = counts.iter().map(|c| (c.tag.as_str(), c.count)).collect();
        assert_eq!(pairs, [("rust", 2), ("web", 1)]);
    }
}
