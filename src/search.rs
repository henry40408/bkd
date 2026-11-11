//! Bookmark search. Plain `LIKE`, chosen over FTS5 because FTS5's tokenizers split
//! words on spaces and punctuation and so handle Chinese poorly.
//!
//! Every term must match (AND). A plain term matches in the title, URL, note or any
//! tag. `LIKE` ignores case for ASCII only, so `É` does not match `é`. Matching
//! highlights are drawn by the front end.
//!
//! The query syntax (the front end reads it the same way; `tests/search_syntax.json`
//! holds the cases both sides must agree on):
//!
//! - `"two words"` is one term. `\"` is a quote inside it. A quote that is never
//!   closed runs to the end of the query.
//! - `-term` must NOT match. A lone `-` is plain text.
//! - `title:`, `url:`, `note:` match only that field; `tag:` matches a tag exactly;
//!   `status:` matches the link check result (`ok`, `redirected`, `broken`, `unknown`
//!   or `unchecked`).
//!   The value may be quoted: `tag:"to read"`. Any other `name:` (such as
//!   `https://x.test`) and a prefix with nothing after it are plain text.
//!
//! Only SQL *shape* is built from strings here; every user value goes through a bind
//! parameter.

use sqlx::{QueryBuilder, Sqlite};

const MAX_TERMS: usize = 8;
const MAX_TERM_CHARS: usize = 100;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Field {
    Any,
    Title,
    Url,
    Note,
    Tag,
    Status,
}

#[derive(Debug, PartialEq, Eq)]
struct Term {
    field: Field,
    text: String,
    negated: bool,
}

#[derive(Debug, Default, PartialEq, Eq)]
pub struct Filter {
    terms: Vec<Term>,
    tag: Option<String>,
    status: Option<String>,
}

impl Filter {
    /// `query` is what the user typed in the search box; `tag` is an exact tag.
    /// Extra terms beyond the limit are ignored and long terms are cut.
    pub fn new(query: Option<&str>, tag: Option<&str>) -> Self {
        let terms = parse_query(query.unwrap_or_default());
        let tag = tag.and_then(exact_tag);
        Self {
            terms,
            tag,
            status: None,
        }
    }

    /// Keeps only bookmarks with this link check result (`unchecked` for none). An
    /// unknown name matches nothing.
    pub fn with_status(mut self, status: Option<&str>) -> Self {
        self.status = status
            .map(|status| status.trim().to_lowercase())
            .filter(|status| !status.is_empty());
        self
    }
}

impl Field {
    fn from_prefix(name: &str) -> Option<Self> {
        match name.to_lowercase().as_str() {
            "title" => Some(Self::Title),
            "url" => Some(Self::Url),
            "note" => Some(Self::Note),
            "tag" => Some(Self::Tag),
            "status" => Some(Self::Status),
            _ => None,
        }
    }

    #[cfg(test)]
    fn name(self) -> &'static str {
        match self {
            Self::Any => "any",
            Self::Title => "title",
            Self::Url => "url",
            Self::Note => "note",
            Self::Tag => "tag",
            Self::Status => "status",
        }
    }

    fn column(self) -> Option<&'static str> {
        match self {
            Self::Title => Some("title"),
            Self::Url => Some("url"),
            Self::Note => Some("note"),
            Self::Any | Self::Tag | Self::Status => None,
        }
    }
}

/// Reads a `title:`-style prefix at `*at` and moves past the colon. Anything else
/// (including `https:`) leaves `*at` alone and is plain text.
fn read_field(chars: &[char], at: &mut usize) -> Field {
    let name_length = chars[*at..]
        .iter()
        .take_while(|character| character.is_alphabetic())
        .count();
    let colon = *at + name_length;
    if chars.get(colon) != Some(&':') {
        return Field::Any;
    }
    let name: String = chars[*at..colon].iter().collect();
    let Some(field) = Field::from_prefix(&name) else {
        return Field::Any;
    };
    *at = colon + 1;
    field
}

/// Reads a bare word or a quoted phrase at `*at`.
fn read_value(chars: &[char], at: &mut usize) -> String {
    let mut value = String::new();
    if chars.get(*at) != Some(&'"') {
        while let Some(&character) = chars.get(*at) {
            if character.is_whitespace() {
                break;
            }
            value.push(character);
            *at += 1;
        }
        return value;
    }
    *at += 1;
    while let Some(&character) = chars.get(*at) {
        *at += 1;
        match character {
            '"' => break,
            '\\' if chars.get(*at) == Some(&'"') => {
                value.push('"');
                *at += 1;
            }
            _ => value.push(character),
        }
    }
    value
}

fn parse_query(query: &str) -> Vec<Term> {
    let chars: Vec<char> = query.chars().collect();
    let mut terms = Vec::new();
    let mut at = 0;
    while at < chars.len() && terms.len() < MAX_TERMS {
        if chars[at].is_whitespace() {
            at += 1;
            continue;
        }
        let start = at;
        let negated =
            chars[at] == '-' && chars.get(at + 1).is_some_and(|next| !next.is_whitespace());
        if negated {
            at += 1;
        }
        let field = read_field(&chars, &mut at);
        let value = read_value(&chars, &mut at);
        let (field, negated, text) = match (value.chars().all(char::is_whitespace), field) {
            (true, Field::Any) => continue,
            // A prefix with nothing (or only spaces) after it is what the user typed, not a filter.
            (true, _) => (Field::Any, false, chars[start..at].iter().collect()),
            (false, _) => (field, negated, value),
        };
        terms.push(Term {
            field,
            text: text.chars().take(MAX_TERM_CHARS).collect(),
            negated,
        });
    }
    terms
}

/// The stored form of a tag, or a lowercase fallback for one that could never be
/// stored (too long, control characters): it must match nothing, not everything.
fn exact_tag(raw: &str) -> Option<String> {
    match crate::bookmarks::normalize_tag(raw) {
        Ok(normalized) => normalized,
        Err(_) => Some(raw.trim().to_lowercase()),
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
        query.push(if term.negated { " AND NOT (" } else { " AND (" });
        push_term_match(query, term);
        query.push(")");
    }
    if let Some(tag) = &filter.tag {
        query.push(" AND ");
        push_tag_equals(query, tag.clone());
    }
    if let Some(status) = &filter.status {
        query.push(" AND ");
        push_status_equals(query, status.clone());
    }
}

fn push_term_match(query: &mut QueryBuilder<Sqlite>, term: &Term) {
    if term.field == Field::Tag {
        push_tag_equals(query, exact_tag(&term.text).unwrap_or_default());
        return;
    }
    if term.field == Field::Status {
        push_status_equals(query, term.text.trim().to_lowercase());
        return;
    }
    let pattern = contains_pattern(&term.text);
    let columns: &[&str] = match term.field.column() {
        Some(column) => &[column],
        None => &["title", "url", "note"],
    };
    for (index, column) in columns.iter().enumerate() {
        if index > 0 {
            query.push(" OR ");
        }
        query.push(format_args!("bookmarks.{column} LIKE "));
        query.push_bind(pattern.clone());
        query.push(" ESCAPE '\\'");
    }
    if term.field == Field::Any {
        query.push(
            " OR EXISTS (SELECT 1 FROM bookmark_tags
                         WHERE bookmark_tags.bookmark_id = bookmarks.id AND bookmark_tags.tag LIKE ",
        );
        query.push_bind(pattern);
        query.push(" ESCAPE '\\')");
    }
}

/// `unchecked` is a bookmark with no link check. COALESCE keeps `-status:broken` from
/// dropping those rows: comparing NULL gives NULL, and NOT NULL is still not true.
fn push_status_equals(query: &mut QueryBuilder<Sqlite>, status: String) {
    query.push("COALESCE(bookmarks.link_status, 'unchecked') = ");
    query.push_bind(status);
}

fn push_tag_equals(query: &mut QueryBuilder<Sqlite>, tag: String) {
    query.push(
        "EXISTS (SELECT 1 FROM bookmark_tags
                 WHERE bookmark_tags.bookmark_id = bookmarks.id AND bookmark_tags.tag = ",
    );
    query.push_bind(tag);
    query.push(")");
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

    /// `tests/search_syntax.json` is shared with the front end's parser test.
    #[test]
    fn parsing_agrees_with_the_shared_cases() {
        let cases: Vec<serde_json::Value> =
            serde_json::from_str(include_str!("../tests/search_syntax.json")).unwrap();
        assert!(!cases.is_empty());
        for case in cases {
            let query = case["query"].as_str().unwrap();
            let parsed: Vec<_> = parse_query(query)
                .into_iter()
                .map(|term| serde_json::json!([term.field.name(), term.text, term.negated]))
                .collect();
            assert_eq!(
                serde_json::Value::Array(parsed),
                case["terms"],
                "query: {query}"
            );
        }
    }

    #[test]
    fn terms_and_their_length_are_limited() {
        let many = (0..20).map(|n| n.to_string()).collect::<Vec<_>>().join(" ");
        assert_eq!(parse_query(&many).len(), MAX_TERMS);
        let long = "x".repeat(500);
        assert_eq!(parse_query(&long)[0].text.len(), MAX_TERM_CHARS);
        let long_quoted = format!("title:\"{long}\"");
        assert_eq!(parse_query(&long_quoted)[0].text.len(), MAX_TERM_CHARS);
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
    async fn phrases_match_as_one_term() {
        let (_dir, pool) = test_pool().await;
        let user = insert_user(&pool, "henry").await.unwrap();
        add(&pool, user, "the quick fox", "https://a.test/1", "", &[]).await;
        add(&pool, user, "quick the fox", "https://a.test/2", "", &[]).await;

        assert_eq!(
            titles(&pool, user, "\"quick fox\"", None).await,
            ["the quick fox"]
        );
        assert_eq!(titles(&pool, user, "quick fox", None).await.len(), 2);
    }

    #[tokio::test]
    async fn excluded_terms_remove_matches_from_every_field() {
        let (_dir, pool) = test_pool().await;
        let user = insert_user(&pool, "henry").await.unwrap();
        add(&pool, user, "rust a", "https://a.test/1", "", &[]).await;
        add(&pool, user, "rust b", "https://a.test/2", "old news", &[]).await;
        add(&pool, user, "rust c", "https://a.test/3", "", &["old"]).await;
        add(&pool, user, "rust d", "https://old.test/4", "", &[]).await;

        assert_eq!(titles(&pool, user, "rust -old", None).await, ["rust a"]);
        assert_eq!(
            titles(&pool, user, "-\"old news\" rust", None).await,
            ["rust a", "rust c", "rust d"]
        );
        // Only an exclusion: everything that does not match.
        assert_eq!(titles(&pool, user, "-rust", None).await.len(), 0);
    }

    #[tokio::test]
    async fn status_finds_bookmarks_by_their_link_check() {
        let (_dir, pool) = test_pool().await;
        let user = insert_user(&pool, "henry").await.unwrap();
        for (title, status) in [
            ("fine", Some("ok")),
            ("dead", Some("broken")),
            ("fresh", None),
        ] {
            add(
                &pool,
                user,
                title,
                &format!("https://a.test/{title}"),
                "",
                &[],
            )
            .await;
            sqlx::query("UPDATE bookmarks SET link_status = ?1 WHERE title = ?2")
                .bind(status)
                .bind(title)
                .execute(&pool)
                .await
                .unwrap();
        }

        assert_eq!(titles(&pool, user, "status:broken", None).await, ["dead"]);
        assert_eq!(titles(&pool, user, "STATUS:OK", None).await, ["fine"]);
        assert_eq!(
            titles(&pool, user, "status:unchecked", None).await,
            ["fresh"]
        );
        // A bookmark that was never checked is not "not broken" by accident of NULL.
        assert_eq!(
            titles(&pool, user, "-status:broken", None).await,
            ["fine", "fresh"]
        );
        assert!(
            titles(&pool, user, "status:nonsense", None)
                .await
                .is_empty()
        );

        let only =
            |status: &str, query: &str| Filter::new(Some(query), None).with_status(Some(status));
        let list = |filter: Filter| {
            let pool = pool.clone();
            async move {
                let page = Page::new(None, None);
                let mut titles: Vec<_> = bookmarks::list(&pool, user, &filter, page)
                    .await
                    .unwrap()
                    .into_iter()
                    .map(|bookmark| bookmark.title)
                    .collect();
                titles.sort();
                titles
            }
        };
        assert_eq!(list(only("broken", "")).await, ["dead"]);
        assert_eq!(list(only(" Unchecked ", "")).await, ["fresh"]);
        assert_eq!(list(only("ok", "dead")).await, Vec::<String>::new());
        assert_eq!(list(only("", "")).await, ["dead", "fine", "fresh"]);
        assert!(list(only("nonsense", "")).await.is_empty());
    }

    #[tokio::test]
    async fn field_prefixes_search_only_that_field() {
        let (_dir, pool) = test_pool().await;
        let user = insert_user(&pool, "henry").await.unwrap();
        add(&pool, user, "zebra", "https://a.test/1", "", &[]).await;
        add(&pool, user, "by url", "https://zebra.test/2", "", &[]).await;
        add(&pool, user, "by note", "https://a.test/3", "zebra", &[]).await;
        add(&pool, user, "by tag", "https://a.test/4", "", &["zebra"]).await;

        assert_eq!(titles(&pool, user, "title:zebra", None).await, ["zebra"]);
        assert_eq!(titles(&pool, user, "url:zebra", None).await, ["by url"]);
        assert_eq!(titles(&pool, user, "note:zebra", None).await, ["by note"]);
        assert_eq!(titles(&pool, user, "tag:zebra", None).await, ["by tag"]);
        assert_eq!(
            titles(&pool, user, "zebra -title:zebra", None).await,
            ["by note", "by tag", "by url"]
        );
    }

    #[tokio::test]
    async fn the_tag_prefix_is_exact_case_insensitive_and_may_contain_spaces() {
        let (_dir, pool) = test_pool().await;
        let user = insert_user(&pool, "henry").await.unwrap();
        add(&pool, user, "exact", "https://a.test/1", "", &["rust"]).await;
        add(
            &pool,
            user,
            "longer",
            "https://a.test/2",
            "",
            &["rustacean"],
        )
        .await;
        add(&pool, user, "spaced", "https://a.test/3", "", &["to read"]).await;

        assert_eq!(titles(&pool, user, "tag:Rust", None).await, ["exact"]);
        assert_eq!(
            titles(&pool, user, "tag:\"to read\"", None).await,
            ["spaced"]
        );
        assert_eq!(
            titles(&pool, user, "-tag:rust -tag:rustacean", None).await,
            ["spaced"]
        );
        let too_long = format!("tag:{}", "x".repeat(200));
        assert!(titles(&pool, user, &too_long, None).await.is_empty());
    }

    #[tokio::test]
    async fn an_empty_prefix_and_urls_are_plain_text() {
        let (_dir, pool) = test_pool().await;
        let user = insert_user(&pool, "henry").await.unwrap();
        add(&pool, user, "has https", "https://x.test/1", "", &[]).await;
        add(&pool, user, "tag: colon", "https://a.test/2", "", &[]).await;

        assert_eq!(
            titles(&pool, user, "https://x.test", None).await,
            ["has https"]
        );
        assert_eq!(titles(&pool, user, "tag:", None).await, ["tag: colon"]);
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
