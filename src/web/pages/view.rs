//! Small helpers that turn stored values into what a page shows: times, domains,
//! highlighted text and links that keep the current filter.

use std::fmt::Write;

use maud::{Markup, html};

fn fold(character: char) -> char {
    character.to_lowercase().next().unwrap_or(character)
}

/// How many characters of `text` starting at `from` the first matching term covers.
fn match_length(text: &[char], from: usize, terms: &[Vec<char>]) -> Option<usize> {
    terms
        .iter()
        .filter(|term| !term.is_empty() && from + term.len() <= text.len())
        .filter(|term| {
            text[from..from + term.len()]
                .iter()
                .zip(term.iter())
                .all(|(a, b)| fold(*a) == fold(*b))
        })
        .map(Vec::len)
        .max()
}

/// `text` split into runs; `true` marks a run that matches a term, ignoring case.
fn split_matches(text: &str, terms: &[String]) -> Vec<(bool, String)> {
    let terms: Vec<Vec<char>> = terms.iter().map(|term| term.chars().collect()).collect();
    let characters: Vec<char> = text.chars().collect();
    let mut runs: Vec<(bool, String)> = Vec::new();
    let mut at = 0;
    while at < characters.len() {
        let length = match_length(&characters, at, &terms);
        let (matched, step) = (length.is_some(), length.unwrap_or(1));
        let piece: String = characters[at..at + step].iter().collect();
        match runs.last_mut() {
            Some((last, run)) if *last == matched => run.push_str(&piece),
            _ => runs.push((matched, piece)),
        }
        at += step;
    }
    runs
}

/// `text` with every occurrence of a term wrapped in `<mark>`. Maud escapes the text.
pub fn highlight(text: &str, terms: &[String]) -> Markup {
    html! {
        @for (matched, run) in split_matches(text, terms) {
            @if matched { mark { (run) } } @else { (run) }
        }
    }
}

/// The host of a URL without `www.`, or the text itself when it has no host.
pub fn domain_of(url: &str) -> String {
    let without_scheme = url.split_once("://").map_or(url, |(_, rest)| rest);
    let authority = without_scheme
        .split(['/', '?', '#'])
        .next()
        .unwrap_or_default();
    let host = authority
        .rsplit_once('@')
        .map_or(authority, |(_, host)| host);
    let host = match host.rsplit_once(':') {
        Some((name, port)) if port.chars().all(|c| c.is_ascii_digit()) => name,
        _ => host,
    };
    let host = host.to_lowercase();
    match host.strip_prefix("www.") {
        Some(rest) => rest.to_owned(),
        None if host.is_empty() => url.to_owned(),
        None => host,
    }
}

/// A saved time as the page shows it. The server cannot know the reader's time zone, so
/// the text is relative and the tooltip is in UTC; the script may replace it with local time.
pub struct SavedTime {
    pub iso: String,
    pub exact: String,
    pub ago: String,
}

impl SavedTime {
    pub fn new(now: i64, unix_seconds: i64) -> Self {
        let (year, month, day, hour, minute, second) = civil(unix_seconds);
        Self {
            iso: format!("{year:04}-{month:02}-{day:02}T{hour:02}:{minute:02}:{second:02}Z"),
            exact: format!("{year:04}-{month:02}-{day:02} {hour:02}:{minute:02} UTC"),
            ago: time_ago(now, unix_seconds),
        }
    }
}

/// Days since 1970-01-01 to a calendar date (Howard Hinnant's `civil_from_days`).
fn civil(unix_seconds: i64) -> (i64, i64, i64, i64, i64, i64) {
    let days = unix_seconds.div_euclid(86_400);
    let seconds_of_day = unix_seconds.rem_euclid(86_400);
    let shifted = days + 719_468;
    let era = shifted.div_euclid(146_097);
    let day_of_era = shifted.rem_euclid(146_097);
    let year_of_era =
        (day_of_era - day_of_era / 1_460 + day_of_era / 36_524 - day_of_era / 146_096) / 365;
    let day_of_year = day_of_era - (365 * year_of_era + year_of_era / 4 - year_of_era / 100);
    let month_index = (5 * day_of_year + 2) / 153;
    let day = day_of_year - (153 * month_index + 2) / 5 + 1;
    let month = if month_index < 10 {
        month_index + 3
    } else {
        month_index - 9
    };
    let year = year_of_era + era * 400 + i64::from(month <= 2);
    (
        year,
        month,
        day,
        seconds_of_day / 3_600,
        seconds_of_day % 3_600 / 60,
        seconds_of_day % 60,
    )
}

fn plural(count: i64, unit: &str) -> String {
    let suffix = if count == 1 { "" } else { "s" };
    format!("{count} {unit}{suffix} ago")
}

fn time_ago(now: i64, then: i64) -> String {
    let hours = (now - then).div_euclid(3_600);
    if hours < 1 {
        return "just now".to_owned();
    }
    if hours < 24 {
        return plural(hours, "hour");
    }
    let days = hours / 24;
    if days == 1 {
        return "yesterday".to_owned();
    }
    if days < 7 {
        return plural(days, "day");
    }
    if days < 30 {
        return plural(days / 7, "week");
    }
    if days < 365 {
        return plural(days / 30, "month");
    }
    plural(days / 365, "year")
}

/// What a list page is showing: the search, the tag, the link status and the page.
#[derive(Clone, Default)]
pub struct ListView {
    pub q: String,
    pub tag: String,
    pub status: String,
    pub page: u32,
    pub select: bool,
    /// Select mode with every bookmark on the page already ticked.
    pub all: bool,
}

impl ListView {
    /// The address of this view, with the page changed.
    pub fn href_for_page(&self, page: u32) -> String {
        Self {
            page,
            ..self.clone()
        }
        .href()
    }

    pub fn href(&self) -> String {
        self.href_to("/")
    }

    /// The same view on another path, such as `/random`.
    pub fn href_to(&self, path: &str) -> String {
        let mut pairs = Vec::new();
        if !self.q.trim().is_empty() {
            pairs.push(("q", self.q.trim()));
        }
        if !self.tag.is_empty() {
            pairs.push(("tag", self.tag.as_str()));
        }
        if !self.status.is_empty() {
            pairs.push(("status", self.status.as_str()));
        }
        let page = self.page.to_string();
        if self.page > 1 {
            pairs.push(("page", &page));
        }
        if self.select {
            pairs.push(("select", "1"));
        }
        if self.select && self.all {
            pairs.push(("all", "1"));
        }
        with_query(path, &pairs)
    }

    pub fn without_select(&self) -> Self {
        Self {
            select: false,
            all: false,
            ..self.clone()
        }
    }

    /// The same select-mode view with every bookmark ticked, or none.
    pub fn with_all(&self, all: bool) -> Self {
        Self {
            select: true,
            all,
            ..self.clone()
        }
    }

    pub fn with_select(&self) -> Self {
        Self {
            select: true,
            ..self.clone()
        }
    }
}

/// `path` with the pairs appended, after any query it already has.
pub fn with_query(path: &str, pairs: &[(&str, &str)]) -> String {
    let mut out = path.to_owned();
    for (name, value) in pairs {
        out.push(if out.contains('?') { '&' } else { '?' });
        out.push_str(name);
        out.push('=');
        percent_encode_into(&mut out, value);
    }
    out
}

fn percent_encode_into(out: &mut String, value: &str) {
    for byte in value.bytes() {
        if byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_' | b'.' | b'~') {
            out.push(char::from(byte));
        } else {
            let _ = write!(out, "%{byte:02X}");
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn highlight_wraps_matches_and_escapes_everything() {
        let marked = highlight("Rust <b> & Rust", &["rust".to_owned()]);
        assert_eq!(
            marked.into_string(),
            "<mark>Rust</mark> &lt;b&gt; &amp; <mark>Rust</mark>"
        );
    }

    #[test]
    fn highlight_without_terms_still_escapes() {
        assert_eq!(highlight("<script>", &[]).into_string(), "&lt;script&gt;");
    }

    #[test]
    fn highlight_handles_non_ascii_text() {
        let marked = highlight("學習 Rust 語言", &["rust".to_owned(), "語言".to_owned()]);
        assert_eq!(
            marked.into_string(),
            "學習 <mark>Rust</mark> <mark>語言</mark>"
        );
    }

    #[test]
    fn domains_lose_scheme_port_userinfo_and_www() {
        assert_eq!(
            domain_of("https://www.Example.com:8080/a?b#c"),
            "example.com"
        );
        assert_eq!(domain_of("http://me@host.test/"), "host.test");
        assert_eq!(domain_of("not a url"), "not a url");
    }

    #[test]
    fn times_are_formatted_in_utc() {
        let time = SavedTime::new(1_700_000_000 + 3 * 86_400, 1_700_000_000);
        assert_eq!(time.iso, "2023-11-14T22:13:20Z");
        assert_eq!(time.exact, "2023-11-14 22:13 UTC");
        assert_eq!(time.ago, "3 days ago");
    }

    #[test]
    fn relative_times_use_the_largest_unit() {
        assert_eq!(time_ago(100, 90), "just now");
        assert_eq!(time_ago(2 * 3_600, 0), "2 hours ago");
        assert_eq!(time_ago(86_400 + 7_200, 0), "yesterday");
        assert_eq!(time_ago(14 * 86_400, 0), "2 weeks ago");
        assert_eq!(time_ago(400 * 86_400, 0), "1 year ago");
    }

    #[test]
    fn list_links_keep_the_filter_and_encode_it() {
        let view = ListView {
            q: "a b&c".to_owned(),
            tag: "to read".to_owned(),
            page: 3,
            ..ListView::default()
        };
        assert_eq!(view.href(), "/?q=a%20b%26c&tag=to%20read&page=3");
        assert_eq!(ListView::default().href(), "/");
    }
}
