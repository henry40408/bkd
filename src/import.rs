//! Importing a Netscape bookmark file: parse, apply the usual bookmark rules, skip what
//! cannot be kept and report it.

use anyhow::Result;
use serde::Serialize;
use sqlx::SqlitePool;

use crate::bookmarks::{self, BookmarkInput, ImportEntry};
use crate::netscape::{self, Link};

/// How many rejected links the report names. The count covers all of them.
const INVALID_EXAMPLES: usize = 5;

// Some exporters write milliseconds or microseconds instead of seconds.
const SECONDS_LIMIT: i64 = 100_000_000_000;

#[derive(Debug, Serialize)]
pub struct Report {
    pub added: usize,
    pub duplicates: usize,
    pub invalid: usize,
    pub invalid_examples: Vec<InvalidLink>,
}

#[derive(Debug, Serialize)]
pub struct InvalidLink {
    pub url: String,
    pub reason: &'static str,
}

pub async fn import_html(pool: &SqlitePool, user_id: i64, html: &str, now: i64) -> Result<Report> {
    let links = netscape::parse(html);
    let mut entries = Vec::new();
    let mut invalid = Vec::new();
    for link in &links {
        match to_entry(link, now) {
            Ok(entry) => entries.push(entry),
            Err(rejected) => invalid.push(rejected),
        }
    }
    let added = bookmarks::import(pool, user_id, &entries).await?;
    Ok(Report {
        added,
        duplicates: entries.len() - added,
        invalid: invalid.len(),
        invalid_examples: invalid.into_iter().take(INVALID_EXAMPLES).collect(),
    })
}

fn to_entry(link: &Link, now: i64) -> Result<ImportEntry, InvalidLink> {
    let input = BookmarkInput::fitted(&link.url, &link.title, &link.note, &link.tags);
    let bookmark = input.validate().map_err(|error| InvalidLink {
        url: link.url.chars().take(200).collect(),
        reason: error.0,
    })?;
    Ok(ImportEntry {
        bookmark,
        created_at: created_at(link.added, now),
    })
}

/// A missing, zero, negative or future date means the exporter did not know it, so the
/// link counts as saved now instead of landing in 1970 or after the present.
fn created_at(added: Option<i64>, now: i64) -> i64 {
    match added.map(seconds) {
        Some(timestamp) if timestamp > 0 && timestamp <= now => timestamp,
        _ => now,
    }
}

fn seconds(mut timestamp: i64) -> i64 {
    while timestamp >= SECONDS_LIMIT {
        timestamp /= 1000;
    }
    timestamp
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::test_support::{insert_user, test_pool};

    const NOW: i64 = 1_800_000_000;

    #[test]
    fn an_unusable_date_means_now() {
        assert_eq!(created_at(Some(1_700_000_000), NOW), 1_700_000_000);
        for unusable in [None, Some(0), Some(-5), Some(NOW + 1)] {
            assert_eq!(created_at(unusable, NOW), NOW, "{unusable:?}");
        }
    }

    #[test]
    fn timestamps_in_milliseconds_and_microseconds_become_seconds() {
        assert_eq!(seconds(1_700_000_000), 1_700_000_000);
        assert_eq!(seconds(1_700_000_000_000), 1_700_000_000);
        assert_eq!(seconds(1_700_000_000_000_000), 1_700_000_000);
    }

    #[tokio::test]
    async fn links_are_added_skipped_or_reported() {
        let (_dir, pool) = test_pool().await;
        let user = insert_user(&pool, "alice").await.unwrap();
        let html = r#"<DL>
            <DT><H3>Work</H3><DL>
              <DT><A HREF="https://a.test/" ADD_DATE="1700000000" TAGS="Rust">A</A>
              <DD>a note
              <DT><A HREF="https://a.test/">A again</A>
              <DT><A HREF="javascript:alert(1)">bad</A>
              <DT><A HREF="place:sort=8">bad</A>
              <DT><A HREF="https://no-title.test/"></A>
            </DL></DL>"#;
        let report = import_html(&pool, user, html, NOW).await.unwrap();
        assert_eq!((report.added, report.duplicates, report.invalid), (2, 1, 2));
        let stored = bookmarks::all(&pool, user).await.unwrap();
        assert_eq!(stored[0].created_at, 1_700_000_000);
        assert_eq!(stored[0].tags, ["rust", "work"]);
        assert_eq!(stored[0].note, "a note");
        assert_eq!(stored[1].title, "https://no-title.test/");
        assert_eq!(stored[1].created_at, NOW);

        let again = import_html(&pool, user, html, NOW).await.unwrap();
        assert_eq!((again.added, again.duplicates), (0, 3));
    }
}
