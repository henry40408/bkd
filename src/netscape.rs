//! The Netscape bookmark file: the HTML that Chrome, Firefox, Safari, Pocket and
//! Raindrop all export. The parser is a tolerant tokenizer, not an HTML parser: the
//! format is a fixed handful of tags and real files are often not well formed.

use crate::bookmarks::Bookmark;

/// One `<A>` link, with the folders around it already turned into tags.
#[derive(Debug, PartialEq, Eq)]
pub struct Link {
    pub url: String,
    pub title: String,
    pub note: String,
    pub tags: Vec<String>,
    /// The `ADD_DATE` attribute as written, normally Unix seconds.
    pub added: Option<i64>,
}

#[derive(Debug)]
enum Token {
    Start {
        name: String,
        attributes: Vec<(String, String)>,
    },
    End(String),
    Text(String),
}

pub fn parse(html: &str) -> Vec<Link> {
    Parser::default().run(tokenize(html))
}

#[derive(Default)]
enum Capture {
    #[default]
    Nothing,
    Folder,
    Link,
    Note,
}

#[derive(Default)]
struct Parser {
    links: Vec<Link>,
    /// One entry per open `<DL>`: the name of the folder it belongs to, if any.
    folders: Vec<Option<String>>,
    pending_folder: Option<String>,
    open_link: Option<Vec<(String, String)>>,
    /// Where `<DD>` text goes: the link just closed, until another item starts.
    note_target: Option<usize>,
    capture: Capture,
    text: String,
}

impl Parser {
    fn run(mut self, tokens: Vec<Token>) -> Vec<Link> {
        for token in tokens {
            self.handle(token);
        }
        self.flush_note();
        self.links
    }

    fn handle(&mut self, token: Token) {
        match token {
            Token::Text(text) => {
                if !matches!(self.capture, Capture::Nothing) {
                    self.text.push_str(&text);
                }
            }
            Token::Start { name, .. } if name == "br" && matches!(self.capture, Capture::Note) => {
                self.text.push('\n');
            }
            token => {
                self.flush_note();
                self.handle_tag(token);
            }
        }
    }

    fn handle_tag(&mut self, token: Token) {
        match token {
            Token::Start { name, attributes } => match name.as_str() {
                "h3" => {
                    self.note_target = None;
                    self.start_capture(Capture::Folder);
                }
                "dl" => {
                    self.note_target = None;
                    self.folders.push(self.pending_folder.take());
                }
                "a" => {
                    self.open_link = Some(attributes);
                    self.start_capture(Capture::Link);
                }
                "dd" if self.note_target.is_some() => self.start_capture(Capture::Note),
                _ => {}
            },
            Token::End(name) => match name.as_str() {
                "h3" => {
                    let folder = collapse_whitespace(&self.take_text());
                    self.pending_folder = Some(folder);
                }
                "dl" => {
                    self.folders.pop();
                }
                "a" => self.finish_link(),
                _ => {}
            },
            Token::Text(_) => {}
        }
    }

    fn start_capture(&mut self, capture: Capture) {
        self.capture = capture;
        self.text.clear();
    }

    fn take_text(&mut self) -> String {
        self.capture = Capture::Nothing;
        std::mem::take(&mut self.text)
    }

    fn finish_link(&mut self) {
        let title = collapse_whitespace(&self.take_text());
        let Some(attributes) = self.open_link.take() else {
            return;
        };
        let attribute = |wanted: &str| {
            attributes
                .iter()
                .find(|(name, _)| name == wanted)
                .map(|(_, value)| value.as_str())
        };
        let mut tags: Vec<String> = self.folders.iter().flatten().cloned().collect();
        tags.extend(
            attribute("tags")
                .unwrap_or_default()
                .split(',')
                .filter(|tag| !tag.trim().is_empty())
                .map(str::to_owned),
        );
        self.links.push(Link {
            url: attribute("href").unwrap_or_default().trim().to_owned(),
            title,
            note: String::new(),
            tags,
            added: attribute("add_date")
                .or_else(|| attribute("time_added"))
                .and_then(|value| value.trim().parse().ok()),
        });
        self.note_target = Some(self.links.len() - 1);
    }

    fn flush_note(&mut self) {
        if !matches!(self.capture, Capture::Note) {
            return;
        }
        let note = self.take_text();
        if let Some(link) = self.note_target.and_then(|index| self.links.get_mut(index)) {
            link.note = note.trim().to_owned();
        }
    }
}

fn collapse_whitespace(text: &str) -> String {
    text.split_whitespace().collect::<Vec<_>>().join(" ")
}

fn tokenize(html: &str) -> Vec<Token> {
    let mut tokens = Vec::new();
    let mut rest = html;
    while !rest.is_empty() {
        let Some(open) = rest.find('<') else {
            tokens.push(Token::Text(decode_entities(rest)));
            break;
        };
        if open > 0 {
            tokens.push(Token::Text(decode_entities(&rest[..open])));
        }
        rest = &rest[open..];
        let (token, consumed) = read_markup(rest);
        tokens.extend(token);
        rest = &rest[consumed..];
    }
    tokens
}

/// Reads one `<...>` at the start of `input`. Returns the token (none for comments and
/// the doctype) and how many bytes it used.
fn read_markup(input: &str) -> (Option<Token>, usize) {
    if let Some(comment) = input.strip_prefix("<!--") {
        let length = comment.find("-->").map_or(comment.len(), |end| end + 3);
        return (None, 4 + length);
    }
    let after = &input[1..];
    let first = after.chars().next();
    if first == Some('!') || first == Some('?') {
        return (None, input.find('>').map_or(input.len(), |end| end + 1));
    }
    let is_tag = first.is_some_and(|c| c == '/' || c.is_ascii_alphabetic());
    if !is_tag {
        return (Some(Token::Text("<".to_owned())), 1);
    }
    let end = tag_end(input);
    let inner = &input[1..end];
    let consumed = (end + 1).min(input.len());
    (Some(parse_tag(inner)), consumed)
}

/// Index of the `>` that closes the tag, ignoring any inside a quoted attribute value.
/// A quote opens a value only right after `=`, so the apostrophe in an unquoted
/// `HREF=x?q=bob's` does not swallow the links that follow.
fn tag_end(input: &str) -> usize {
    let mut quote = None;
    let mut previous = ' ';
    for (index, c) in input.char_indices().skip(1) {
        match (quote, c) {
            (None, '>') => return index,
            (None, '"' | '\'') if previous == '=' => quote = Some(c),
            (Some(open), _) if open == c => quote = None,
            _ => {}
        }
        if quote.is_none() && !c.is_whitespace() {
            previous = c;
        }
    }
    input.len()
}

fn parse_tag(inner: &str) -> Token {
    if let Some(name) = inner.strip_prefix('/') {
        return Token::End(name.trim().to_ascii_lowercase());
    }
    let name_end = inner
        .find(|c: char| c.is_whitespace() || c == '/')
        .unwrap_or(inner.len());
    Token::Start {
        name: inner[..name_end].to_ascii_lowercase(),
        attributes: parse_attributes(&inner[name_end..]),
    }
}

fn parse_attributes(mut input: &str) -> Vec<(String, String)> {
    let mut attributes = Vec::new();
    loop {
        input = input.trim_start_matches(|c: char| c.is_whitespace() || c == '/');
        if input.is_empty() {
            return attributes;
        }
        let name_end = input
            .find(|c: char| c.is_whitespace() || c == '=' || c == '/')
            .unwrap_or(input.len());
        let name = input[..name_end].to_ascii_lowercase();
        input = input[name_end..].trim_start();
        let Some(after_equals) = input.strip_prefix('=') else {
            attributes.push((name, String::new()));
            continue;
        };
        let (value, remaining) = read_attribute_value(after_equals.trim_start());
        attributes.push((name, decode_entities(value)));
        input = remaining;
    }
}

fn read_attribute_value(input: &str) -> (&str, &str) {
    let Some(quote) = input.chars().next().filter(|c| matches!(c, '"' | '\'')) else {
        let end = input.find(char::is_whitespace).unwrap_or(input.len());
        return (&input[..end], &input[end..]);
    };
    let body = &input[1..];
    match body.find(quote) {
        Some(end) => (&body[..end], &body[end + 1..]),
        None => (body, ""),
    }
}

fn decode_entities(text: &str) -> String {
    let mut decoded = String::with_capacity(text.len());
    let mut rest = text;
    while let Some(amp) = rest.find('&') {
        decoded.push_str(&rest[..amp]);
        rest = &rest[amp..];
        match decode_one_entity(rest) {
            Some((character, consumed)) => {
                decoded.push(character);
                rest = &rest[consumed..];
            }
            None => {
                decoded.push('&');
                rest = &rest[1..];
            }
        }
    }
    decoded.push_str(rest);
    decoded
}

/// Decodes the entity at the start of `input` (which begins with `&`).
fn decode_one_entity(input: &str) -> Option<(char, usize)> {
    // The longest name we accept is short, so a stray `&` never scans far.
    let end = input.char_indices().take(12).find(|(_, c)| *c == ';')?.0;
    let name = &input[1..end];
    let character = match name {
        "amp" => '&',
        "lt" => '<',
        "gt" => '>',
        "quot" => '"',
        "apos" => '\'',
        "nbsp" => ' ',
        _ => {
            let number = name.strip_prefix('#')?;
            let code = match number.strip_prefix(['x', 'X']) {
                Some(hex) => u32::from_str_radix(hex, 16).ok()?,
                None => number.parse().ok()?,
            };
            char::from_u32(code)?
        }
    };
    Some((character, end + 1))
}

/// A flat file: every bookmark carries its tags in `TAGS`, which every importer that
/// understands tags reads, and the rest read the links and ignore the attribute.
pub fn render(bookmarks: &[Bookmark]) -> String {
    let mut html = String::from(
        "<!DOCTYPE NETSCAPE-Bookmark-file-1>\n\
         <META HTTP-EQUIV=\"Content-Type\" CONTENT=\"text/html; charset=UTF-8\">\n\
         <TITLE>Bookmarks</TITLE>\n\
         <H1>Bookmarks</H1>\n\
         <DL><p>\n",
    );
    for bookmark in bookmarks {
        html.push_str(&format!(
            "    <DT><A HREF=\"{}\" ADD_DATE=\"{}\"",
            escape(&bookmark.url),
            bookmark.created_at
        ));
        if !bookmark.tags.is_empty() {
            html.push_str(&format!(
                " TAGS=\"{}\"",
                escape(&joined_tags(&bookmark.tags))
            ));
        }
        html.push_str(&format!(">{}</A>\n", escape(&bookmark.title)));
        if !bookmark.note.is_empty() {
            html.push_str(&format!("    <DD>{}\n", escape(&bookmark.note)));
        }
    }
    html.push_str("</DL><p>\n");
    html
}

fn joined_tags(tags: &[String]) -> String {
    // A comma inside a tag would split it in two when the file is read back.
    let safe: Vec<String> = tags.iter().map(|tag| tag.replace(',', " ")).collect();
    safe.join(",")
}

fn escape(text: &str) -> String {
    text.replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
}

#[cfg(test)]
mod tests {
    use super::*;

    fn link(html: &str) -> Link {
        let mut links = parse(html);
        assert_eq!(links.len(), 1, "{html:?}");
        links.remove(0)
    }

    #[test]
    fn a_chrome_style_file_turns_folders_into_tags() {
        let html = r#"<!DOCTYPE NETSCAPE-Bookmark-file-1>
<!-- This is an automatically generated file. -->
<META HTTP-EQUIV="Content-Type" CONTENT="text/html; charset=UTF-8">
<TITLE>Bookmarks</TITLE>
<H1>Bookmarks</H1>
<DL><p>
    <DT><H3 ADD_DATE="1" LAST_MODIFIED="2" PERSONAL_TOOLBAR_FOLDER="true">Bookmarks bar</H3>
    <DL><p>
        <DT><A HREF="https://a.test/" ADD_DATE="1700000000" ICON="data:image/png;base64,AAA=">A</A>
        <DT><H3>Dev</H3>
        <DL><p>
            <DT><A HREF="https://b.test/">B</A>
        </DL><p>
        <DT><A HREF="https://c.test/">C</A>
    </DL><p>
    <DT><A HREF="https://d.test/">D</A>
</DL><p>"#;
        let links = parse(html);
        let summary: Vec<(&str, &[String])> = links
            .iter()
            .map(|link| (link.url.as_str(), link.tags.as_slice()))
            .collect();
        let bar = String::from("Bookmarks bar");
        let dev = String::from("Dev");
        assert_eq!(
            summary,
            [
                ("https://a.test/", &[bar.clone()][..]),
                ("https://b.test/", &[bar.clone(), dev][..]),
                ("https://c.test/", &[bar][..]),
                ("https://d.test/", &[][..]),
            ]
        );
        assert_eq!(links[0].added, Some(1_700_000_000));
        assert_eq!(links[1].added, None);
    }

    #[test]
    fn the_tags_attribute_follows_the_folder_tags() {
        let html = r#"<DL><DT><H3>Reading</H3><DL>
            <DT><A HREF="https://a.test/" TAGS="rust,web dev,,">A</A></DL></DL>"#;
        assert_eq!(link(html).tags, ["Reading", "rust", "web dev"]);
    }

    #[test]
    fn a_description_after_an_item_becomes_its_note() {
        let html = "<DL><DT><A HREF=\"https://a.test/\">A</A>\n<DD>first line<br>second\n\
                    <DT><A HREF=\"https://b.test/\">B</A></DL>";
        let links = parse(html);
        assert_eq!(links[0].note, "first line\nsecond");
        assert_eq!(links[1].note, "");
    }

    #[test]
    fn a_folder_description_is_not_a_note() {
        let html = "<DL><DT><A HREF=\"https://a.test/\">A</A>\n\
                    <DT><H3>F</H3>\n<DD>about the folder\n<DL><DT><A HREF=\"https://b.test/\">B</A></DL></DL>";
        let links = parse(html);
        assert_eq!(links[0].note, "");
        assert_eq!(links[1].note, "");
    }

    #[test]
    fn entities_quotes_and_odd_markup_are_handled() {
        let html = "<dt><a href='https://a.test/?x=1&amp;y=2' add_date=5 private>\
                    Tom &amp; Jerry &#x4E2D;&#25991; &unknown; &</a>";
        let link = link(html);
        assert_eq!(link.url, "https://a.test/?x=1&y=2");
        assert_eq!(link.title, "Tom & Jerry 中文 &unknown; &");
        assert_eq!(link.added, Some(5));
    }

    #[test]
    fn a_greater_than_inside_an_attribute_does_not_end_the_tag() {
        let link = link("<A HREF=\"https://a.test/?q=a>b\">t</A>");
        assert_eq!(link.url, "https://a.test/?q=a>b");
    }

    #[test]
    fn an_apostrophe_in_an_unquoted_value_does_not_swallow_later_links() {
        let links =
            parse("<A HREF=https://x.test/?q=bob's>one</A><DT><A HREF=\"https://y.test/\">two</A>");
        let urls: Vec<&str> = links.iter().map(|link| link.url.as_str()).collect();
        assert_eq!(urls, ["https://x.test/?q=bob's", "https://y.test/"]);
    }

    #[test]
    fn a_pocket_style_date_is_read() {
        let link = link("<a href=\"https://a.test/\" time_added=\"1700000000\" tags=\"x,y\">t</a>");
        assert_eq!(link.added, Some(1_700_000_000));
        assert_eq!(link.tags, ["x", "y"]);
    }

    #[test]
    fn garbage_and_truncated_input_do_not_panic() {
        for input in [
            "",
            "<",
            "<A",
            "<A HREF=\"x",
            "<!--",
            "<!DOCTYPE",
            "</",
            "&",
            "&#;",
            "&#99999999999;",
            "<DL><DL></DL></DL></DL>",
            "plain text 中文 <3",
        ] {
            let _ = parse(input);
        }
    }

    #[test]
    fn exported_bookmarks_read_back_the_same() {
        let bookmark = Bookmark {
            id: 1,
            url: "https://a.test/?x=1&y=\"2\"".to_owned(),
            title: "<b>Tom</b> & Jerry".to_owned(),
            note: "line one\nline <two>".to_owned(),
            tags: vec!["a,b".to_owned(), "c".to_owned()],
            created_at: 1_700_000_000,
            updated_at: 1_700_000_001,
            link: None,
        };
        let links = parse(&render(&[bookmark]));
        assert_eq!(
            links,
            [Link {
                url: "https://a.test/?x=1&y=\"2\"".to_owned(),
                title: "<b>Tom</b> & Jerry".to_owned(),
                note: "line one\nline <two>".to_owned(),
                tags: vec!["a b".to_owned(), "c".to_owned()],
                added: Some(1_700_000_000),
            }]
        );
    }
}
