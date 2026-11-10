//! Bookmark search. Plain `LIKE`, chosen over FTS5 because FTS5's tokenizers split
//! words on spaces and punctuation and so handle Chinese poorly.
//!
//! Every whitespace-separated term must match (AND) in the title, URL, note or any
//! tag. `LIKE` ignores case for ASCII only, so `É` does not match `é`. Matching
//! highlights are drawn by the front end.
//!
//! Only SQL *shape* is built from strings here; every user value goes through a bind
//! parameter.

use sqlx::{QueryBuilder, Sqlite};

const MAX_TERMS: usize = 8;
const MAX_TERM_CHARS: usize = 100;

#[derive(Debug, Default, PartialEq, Eq)]
pub struct Filter {
    terms: Vec<String>,
    tag: Option<String>,
}

impl Filter {
    /// `query` is what the user typed in the search box; `tag` is an exact tag.
    /// Extra terms beyond the limit are ignored and long terms are cut.
    pub fn new(query: Option<&str>, tag: Option<&str>) -> Self {
        let terms = query
            .unwrap_or_default()
            .split_whitespace()
            .take(MAX_TERMS)
            .map(|term| term.chars().take(MAX_TERM_CHARS).collect())
            .collect();
        // A tag that could never be stored (too long, control characters) still has
        // to filter: dropping it would return every bookmark instead of none.
        let tag = tag.and_then(|raw| match crate::bookmarks::normalize_tag(raw) {
            Ok(normalized) => normalized,
            Err(_) => Some(raw.trim().to_lowercase()),
        });
        Self { terms, tag }
    }
}

/// `%term%` with the three characters that `LIKE` treats specially made literal.
fn contains_pattern(term: &str) -> String {
    let mut pattern = String::with_capacity(term.len() + 2);
    pattern.push('%');
    for character in term.chars() {
        if matches!(character, '\\' | '%' | '_') {
            pattern.push('\\');
        }
        pattern.push(character);
    }
    pattern.push('%');
    pattern
}

/// Appends ` AND ...` conditions for a query that selects from `bookmarks`.
pub fn push_conditions(query: &mut QueryBuilder<Sqlite>, filter: &Filter) {
    for term in &filter.terms {
        let pattern = contains_pattern(term);
        query.push(" AND (");
        for column in ["title", "url", "note"] {
            query.push(format_args!("bookmarks.{column} LIKE "));
            query.push_bind(pattern.clone());
            query.push(" ESCAPE '\\' OR ");
        }
        query.push(
            "EXISTS (SELECT 1 FROM bookmark_tags
                     WHERE bookmark_tags.bookmark_id = bookmarks.id AND bookmark_tags.tag LIKE ",
        );
        query.push_bind(pattern);
        query.push(" ESCAPE '\\'))");
    }
    if let Some(tag) = &filter.tag {
        query.push(
            " AND EXISTS (SELECT 1 FROM bookmark_tags
                          WHERE bookmark_tags.bookmark_id = bookmarks.id AND bookmark_tags.tag = ",
        );
        query.push_bind(tag.clone());
        query.push(")");
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::bookmarks::{self, BookmarkInput, Page};
    use crate::db::test_support::{insert_user, test_pool};
    use sqlx::SqlitePool;

    async fn add(pool: &SqlitePool, user: i64, title: &str, url: &str, note: &str, tags: &[&str]) {
        let bookmark = BookmarkInput {
            url: url.to_owned(),
            title: title.to_owned(),
            note: note.to_owned(),
            tags: tags.iter().map(|tag| (*tag).to_owned()).collect(),
        }
        .validate()
        .unwrap();
        bookmarks::create(pool, user, &bookmark, 0).await.unwrap();
    }

    async fn titles(pool: &SqlitePool, user: i64, query: &str, tag: Option<&str>) -> Vec<String> {
        let filter = Filter::new(Some(query), tag);
        let mut titles: Vec<_> = bookmarks::list(pool, user, &filter, Page::new(None, None))
            .await
            .unwrap()
            .into_iter()
            .map(|bookmark| bookmark.title)
            .collect();
        titles.sort();
        titles
    }

    #[test]
    fn special_characters_are_made_literal() {
        assert_eq!(contains_pattern("100%_\\"), "%100\\%\\_\\\\%");
    }

    #[test]
    fn the_filter_splits_terms_and_limits_them() {
        let filter = Filter::new(Some("  a   b\tc "), None);
        assert_eq!(filter.terms, ["a", "b", "c"]);
        let many = (0..20).map(|n| n.to_string()).collect::<Vec<_>>().join(" ");
        assert_eq!(Filter::new(Some(&many), None).terms.len(), MAX_TERMS);
        let long = "x".repeat(500);
        assert_eq!(
            Filter::new(Some(&long), None).terms[0].len(),
            MAX_TERM_CHARS
        );
    }

    #[test]
    fn the_tag_filter_is_normalized() {
        assert_eq!(
            Filter::new(None, Some("  Rust ")).tag.as_deref(),
            Some("rust")
        );
        assert_eq!(Filter::new(None, Some("   ")).tag, None);
    }

    #[tokio::test]
    async fn terms_match_title_url_note_and_tags() {
        let (_dir, pool) = test_pool().await;
        let user = insert_user(&pool, "henry").await.unwrap();
        add(&pool, user, "by title zebra", "https://a.test/1", "", &[]).await;
        add(&pool, user, "by url", "https://zebra.test/2", "", &[]).await;
        add(
            &pool,
            user,
            "by note",
            "https://a.test/3",
            "a zebra here",
            &[],
        )
        .await;
        add(&pool, user, "by tag", "https://a.test/4", "", &["zebra"]).await;
        add(&pool, user, "unrelated", "https://a.test/5", "", &["horse"]).await;

        let found = titles(&pool, user, "zebra", None).await;
        assert_eq!(found, ["by note", "by tag", "by title zebra", "by url"]);
    }

    #[tokio::test]
    async fn every_term_must_match_but_in_any_field() {
        let (_dir, pool) = test_pool().await;
        let user = insert_user(&pool, "henry").await.unwrap();
        add(&pool, user, "rust async", "https://a.test/1", "", &["book"]).await;
        add(&pool, user, "rust only", "https://a.test/2", "", &[]).await;
        add(&pool, user, "book only", "https://a.test/3", "", &["book"]).await;

        assert_eq!(titles(&pool, user, "rust book", None).await, ["rust async"]);
    }

    #[tokio::test]
    async fn chinese_text_matches_without_word_breaks() {
        let (_dir, pool) = test_pool().await;
        let user = insert_user(&pool, "henry").await.unwrap();
        add(
            &pool,
            user,
            "學習程式設計",
            "https://a.test/1",
            "",
            &["教學"],
        )
        .await;
        add(&pool, user, "烹飪", "https://a.test/2", "", &[]).await;

        assert_eq!(titles(&pool, user, "程式", None).await, ["學習程式設計"]);
        assert_eq!(titles(&pool, user, "教學", None).await, ["學習程式設計"]);
    }

    #[tokio::test]
    async fn ascii_case_is_ignored() {
        let (_dir, pool) = test_pool().await;
        let user = insert_user(&pool, "henry").await.unwrap();
        add(&pool, user, "Rust Book", "https://a.test/1", "", &[]).await;
        assert_eq!(titles(&pool, user, "rUsT", None).await, ["Rust Book"]);
    }

    #[tokio::test]
    async fn like_wildcards_in_the_query_are_literal() {
        let (_dir, pool) = test_pool().await;
        let user = insert_user(&pool, "henry").await.unwrap();
        add(&pool, user, "100% done", "https://a.test/1", "", &[]).await;
        add(&pool, user, "1000 done", "https://a.test/2", "", &[]).await;
        add(&pool, user, "snake_case", "https://a.test/3", "", &[]).await;
        add(&pool, user, "snakeXcase", "https://a.test/4", "", &[]).await;
        add(&pool, user, "back\\slash", "https://a.test/5", "", &[]).await;

        assert_eq!(titles(&pool, user, "100%", None).await, ["100% done"]);
        assert_eq!(
            titles(&pool, user, "snake_case", None).await,
            ["snake_case"]
        );
        assert_eq!(titles(&pool, user, "\\", None).await, ["back\\slash"]);
        assert_eq!(titles(&pool, user, "%", None).await, ["100% done"]);
    }

    #[tokio::test]
    async fn quotes_in_the_query_cannot_change_the_sql() {
        let (_dir, pool) = test_pool().await;
        let user = insert_user(&pool, "henry").await.unwrap();
        add(&pool, user, "it's fine", "https://a.test/1", "", &[]).await;
        assert_eq!(titles(&pool, user, "it's", None).await, ["it's fine"]);
        assert!(titles(&pool, user, "' OR 1=1 --", None).await.is_empty());
    }

    #[tokio::test]
    async fn the_tag_filter_is_exact_and_combines_with_terms() {
        let (_dir, pool) = test_pool().await;
        let user = insert_user(&pool, "henry").await.unwrap();
        add(&pool, user, "rust guide", "https://a.test/1", "", &["rust"]).await;
        add(
            &pool,
            user,
            "go guide",
            "https://a.test/2",
            "",
            &["rustacean"],
        )
        .await;
        add(&pool, user, "cooking", "https://a.test/3", "", &["rust"]).await;

        assert_eq!(
            titles(&pool, user, "", Some("Rust")).await,
            ["cooking", "rust guide"]
        );
        assert_eq!(
            titles(&pool, user, "guide", Some("rust")).await,
            ["rust guide"]
        );
    }

    #[tokio::test]
    async fn an_impossible_tag_matches_nothing() {
        let (_dir, pool) = test_pool().await;
        let user = insert_user(&pool, "henry").await.unwrap();
        add(&pool, user, "anything", "https://a.test/1", "", &["rust"]).await;
        let too_long = "x".repeat(200);
        assert!(titles(&pool, user, "", Some(&too_long)).await.is_empty());
        assert!(titles(&pool, user, "", Some("a\u{1f}b")).await.is_empty());
    }

    #[tokio::test]
    async fn search_never_returns_another_users_bookmarks() {
        let (_dir, pool) = test_pool().await;
        let henry = insert_user(&pool, "henry").await.unwrap();
        let mei = insert_user(&pool, "mei").await.unwrap();
        add(
            &pool,
            mei,
            "shared word",
            "https://a.test/1",
            "",
            &["shared"],
        )
        .await;

        assert!(titles(&pool, henry, "shared", None).await.is_empty());
        assert!(titles(&pool, henry, "", Some("shared")).await.is_empty());
        assert!(titles(&pool, henry, "", None).await.is_empty());
    }
}
