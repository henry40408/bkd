//! The bookmark list and the forms that change bookmarks. Saving, editing and deleting
//! call the API handlers; the list reads through the same `bookmarks` functions.

use axum::Json;
use axum::extract::{Form, Path, Query, State};
use axum::response::{IntoResponse, Redirect, Response};
use serde::Deserialize;

use super::support::{
    Active, Notice, Page, PageError, Section, Shell, View, form_refusal, redirect_with_notice,
    render, render_with_status,
};
use maud::{Markup, html};

use super::layout::{error_line, framed, time_tag};
use super::view::{ListView, SavedTime, domain_of, highlight, with_query};
use crate::auth::session::SessionUser;
use crate::auth::unix_now;
use crate::bookmarks::{self, Bookmark, BookmarkInput, DEFAULT_PAGE_SIZE, Page as Window};
use crate::bulk::{BulkActionInput, BulkInput};
use crate::linkcheck::{self, CheckInput};
use crate::search::{Filter, Highlight};
use crate::web::SharedState;
use crate::web::bookmark_routes;
use crate::web::error::ApiError;
use crate::web::guard::CurrentUser;

const PAGE_SIZE: u32 = DEFAULT_PAGE_SIZE;
// Keeps `(page - 1) * PAGE_SIZE` far from overflowing.
const MAX_PAGE: u32 = 1_000_000;
const CHECK_RETRY_AFTER: std::time::Duration = std::time::Duration::from_secs(10);

struct TagChip {
    label: Markup,
    href: String,
}

struct LinkNote {
    status: String,
    label: &'static str,
    text: String,
    checked: String,
}

struct BookmarkView {
    id: i64,
    url: String,
    plain_title: String,
    title: Markup,
    domain: Markup,
    note: Option<Markup>,
    tags: Vec<TagChip>,
    link: Option<LinkNote>,
    link_class: Option<String>,
    saved: SavedTime,
}

fn link_label(status: &str) -> &'static str {
    match status {
        "ok" => "Link OK",
        "redirected" => "Link moved",
        "broken" => "Broken link",
        _ => "Link not confirmed",
    }
}

fn link_note(link: &bookmarks::LinkCheck) -> Option<LinkNote> {
    if !matches!(
        link.status.as_str(),
        "ok" | "redirected" | "broken" | "unknown"
    ) {
        return None;
    }
    let checked = SavedTime::new(unix_now(), link.checked_at).exact;
    let text = match link.status.as_str() {
        "ok" => format!("checked {checked}"),
        "redirected" => format!("now at {}", link.detail),
        _ => link.detail.clone(),
    };
    Some(LinkNote {
        status: link.status.clone(),
        label: link_label(&link.status),
        text,
        checked,
    })
}

fn bookmark_view(bookmark: Bookmark, filter: &Filter, now: i64) -> BookmarkView {
    let tags = bookmark
        .tags
        .iter()
        .map(|tag| TagChip {
            label: highlight(tag, &filter.highlights(Highlight::Tag(tag))),
            href: with_query("/", &[("tag", tag)]),
        })
        .collect();
    let link = bookmark.link.as_ref().and_then(link_note);
    BookmarkView {
        id: bookmark.id,
        domain: highlight(
            &domain_of(&bookmark.url),
            &filter.highlights(Highlight::Url),
        ),
        title: highlight(&bookmark.title, &filter.highlights(Highlight::Title)),
        note: (!bookmark.note.is_empty())
            .then(|| highlight(&bookmark.note, &filter.highlights(Highlight::Note))),
        link_class: link.as_ref().map(|note| format!("link-{}", note.status)),
        link,
        tags,
        saved: SavedTime::new(now, bookmark.created_at),
        plain_title: bookmark.title,
        url: bookmark.url,
    }
}

#[derive(Default)]
struct AddForm {
    open: bool,
    url: String,
    title: String,
    note: String,
    tags: String,
    error: Option<String>,
}

struct BookmarksPage {
    shell: Shell,
    view: ListView,
    random_href: String,
    select_href: String,
    done_selecting_href: String,
    toggle_all_href: String,
    add: AddForm,
    pick: Option<BookmarkView>,
    pick_empty: bool,
    rows: Vec<BookmarkView>,
    empty_text: &'static str,
    summary: String,
    prev_href: Option<String>,
    next_href: Option<String>,
}

fn link_glyph(status: &str) -> Markup {
    html! {
        @match status {
            "ok" => svg viewBox="0 0 16 16" { path d="M3 8.5l3.2 3.2L13 4.5" {} },
            "redirected" => svg viewBox="0 0 16 16" { path d="M2.5 8h10.5M9 4l4 4-4 4" {} },
            "broken" => svg viewBox="0 0 16 16" { path d="M4 4l8 8M12 4l-8 8" {} },
            _ => "?",
        }
    }
}

fn link_note_line(link: &LinkNote) -> Markup {
    html! {
        p."link-note".(link.status) title={ "Checked " (link.checked) } {
            span.glyph aria-hidden="true" { (link_glyph(&link.status)) }
            span."sr-only" { (link.label) ": " }
            (link.text)
        }
    }
}

fn bookmark_item(bookmark: &BookmarkView, ticked: Option<bool>) -> Markup {
    let item_class = bookmark
        .link_class
        .as_deref()
        .map_or_else(|| "item".to_owned(), |class| format!("item {class}"));
    html! {
        li class=(item_class) data-id=(bookmark.id) {
            @if let Some(checked) = ticked {
                label."pick-box" {
                    input type="checkbox" name={ "id-" (bookmark.id) } value="1" checked[checked]
                        aria-label={ "Select " (bookmark.plain_title) };
                }
            }
            span.domain { (bookmark.domain) }
            div {
                a.title href=(bookmark.url) target="_blank" rel="noopener noreferrer" { (bookmark.title) }
                @if let Some(note) = &bookmark.note { p.note { (note) } }
                @if let Some(link) = &bookmark.link { (link_note_line(link)) }
                @if !bookmark.tags.is_empty() {
                    div.tags {
                        @for tag in &bookmark.tags { a.tag href=(tag.href) { (tag.label) } }
                    }
                }
            }
            div.when {
                (time_tag(&bookmark.saved))
                div.actions {
                    a.quiet href={ "/bookmarks/" (bookmark.id) "/edit" }
                        aria-label={ "Edit " (bookmark.plain_title) } { "Edit" }
                    a.quiet.danger href={ "/bookmarks/" (bookmark.id) "/delete" }
                        aria-label={ "Delete " (bookmark.plain_title) } { "Delete" }
                }
            }
        }
    }
}

impl BookmarksPage {
    fn search_bar(&self) -> Markup {
        let view = &self.view;
        html! {
            form.bar role="search" method="get" action="/" {
                input type="search" name="q" value=(view.q) aria-label="Search bookmarks"
                    aria-describedby="search-hint" placeholder="Search title, note, URL, or tag";
                @if !view.tag.is_empty() { input type="hidden" name="tag" value=(view.tag); }
                @if !view.status.is_empty() { input type="hidden" name="status" value=(view.status); }
                button type="submit" { "Search" }
                a.button."quiet-button" href=(self.random_href) { "Random" }
                @if view.select {
                    a.button."quiet-button".on href=(self.done_selecting_href) { "Done" }
                } @else {
                    a.button."quiet-button" href=(self.select_href) { "Select" }
                }
            }
            p."search-hint" id="search-hint" {
                "Use " code { "\"exact phrase\"" } ", " code { "-exclude" } ", " code { "tag:name" } ", "
                code { "title:" } ", " code { "url:" } ", " code { "note:" } " or " code { "status:" } "."
            }
        }
    }

    fn add_form(&self) -> Markup {
        let add = &self.add;
        html! {
            details.add #add open[add.open] {
                summary { "Add bookmark" }
                form.stack method="post" action="/bookmarks" {
                    label { "URL " input type="url" name="url" value=(add.url) placeholder="https://" required; }
                    label {
                        "Title "
                        input type="text" name="title" value=(add.title) placeholder="Leave blank to use the domain";
                    }
                    label { "Note " textarea name="note" rows="2" { (add.note) } }
                    label {
                        "Tags, separated by commas "
                        input type="text" name="tags" value=(add.tags) placeholder="rust, reading";
                    }
                    (error_line(add.error.as_deref()))
                    div { button type="submit" { "Save bookmark" } }
                }
            }
        }
    }

    fn pick_section(&self) -> Markup {
        html! {
            @if let Some(pick) = &self.pick {
                section.pick aria-label="Random bookmark" {
                    a.title href=(pick.url) target="_blank" rel="noopener noreferrer" { (pick.title) }
                    span.domain { (pick.domain) }
                    @if let Some(note) = &pick.note { p.note { (note) } }
                    div.actions {
                        a.quiet href=(self.random_href) { "Another" }
                        a.quiet href={ "/bookmarks/" (pick.id) "/edit" } { "Edit" }
                        a.quiet href=(self.view.href()) { "Close" }
                    }
                }
            } @else if self.pick_empty {
                p.notice role="status" { "Nothing to pick from here." }
            }
        }
    }

    fn bulk_panel(&self) -> Markup {
        let view = &self.view;
        html! {
            input type="hidden" name="q" value=(view.q);
            input type="hidden" name="tag" value=(view.tag);
            input type="hidden" name="status" value=(view.status);
            input type="hidden" name="page" value=(view.page);
            div.bulk role="region" aria-label="Bulk actions" {
                div."bulk-panel" {
                    input type="text" name="tags" placeholder="Tags, separated by commas"
                        aria-label="Tags for adding or removing";
                    button type="submit" name="action" value="add_tags" { "Add tags" }
                    button.quiet type="submit" name="action" value="remove_tags" { "Remove tags" }
                    button.quiet type="submit" name="action" value="check" { "Check links" }
                    button.quiet.danger type="submit" name="action" value="delete" { "Delete" }
                }
                p.hint {
                    a.quiet href=(self.toggle_all_href) {
                        @if view.all { "Clear selection" } @else { "Select all on this page" }
                    }
                }
                p.hint { "Tick the bookmarks you want, then choose an action." }
            }
        }
    }

    fn list(&self) -> Markup {
        html! {
            @if self.rows.is_empty() {
                div.empty { (self.empty_text) }
            } @else {
                p.summary { (self.summary) }
                ul.list.selecting[self.view.select] {
                    @for row in &self.rows { (bookmark_item(row, self.view.select.then_some(self.view.all))) }
                }
            }
        }
    }

    fn pager(&self) -> Markup {
        html! {
            @if self.prev_href.is_some() || self.next_href.is_some() {
                nav.pager aria-label="Pages" {
                    @if let Some(href) = &self.prev_href {
                        a.button."quiet-button" href=(href) rel="prev" { "Newer" }
                    }
                    @if let Some(href) = &self.next_href {
                        a.button."quiet-button" href=(href) rel="next" { "Older" }
                    }
                }
            }
        }
    }
}

impl View for BookmarksPage {
    fn markup(&self) -> Markup {
        let content = html! {
            (self.search_bar())
            (self.add_form())
            (self.pick_section())
            @if self.view.select {
                form.bulk-form method="post" action="/bookmarks/bulk" {
                    (self.bulk_panel())
                    (self.list())
                }
            } @else {
                (self.list())
            }
            (self.pager())
        };
        framed("bkd", &self.shell, content)
    }
}

impl View for EditPage {
    fn markup(&self) -> Markup {
        let content = html! {
            h1 { "Edit bookmark" }
            form.stack."edit-form" method="post" action={ "/bookmarks/" (self.id) "/edit" } {
                label { "URL " input type="url" name="url" value=(self.url) placeholder="https://" required; }
                label {
                    "Title "
                    input type="text" name="title" value=(self.title) placeholder="Leave blank to use the domain";
                }
                label { "Note " textarea name="note" rows="3" { (self.note) } }
                label { "Tags, separated by commas " input type="text" name="tags" value=(self.tags); }
                (error_line(self.error.as_deref()))
                div {
                    button type="submit" { "Save" }
                    " "
                    a."quiet-link" href="/" { "Cancel" }
                }
            }
        };
        framed("Edit bookmark · bkd", &self.shell, content)
    }
}

impl View for ConfirmDeletePage {
    fn markup(&self) -> Markup {
        let content = html! {
            h1 { (self.heading) }
            p.summary { (self.detail) }
            form.stack method="post" action=(self.action) {
                @for field in &self.hidden {
                    input type="hidden" name=(field.name) value=(field.value);
                }
                div {
                    button."danger-button" type="submit" { "Delete" }
                    " "
                    a."quiet-link" href=(self.cancel_href) { "Keep" }
                }
            }
        };
        framed("Delete · bkd", &self.shell, content)
    }
}

#[derive(Deserialize, Default)]
pub struct ListQuery {
    q: Option<String>,
    tag: Option<String>,
    status: Option<String>,
    page: Option<u32>,
    select: Option<String>,
    all: Option<String>,
    notice: Option<String>,
}

impl ListQuery {
    fn view(&self) -> ListView {
        ListView {
            q: self.q.clone().unwrap_or_default(),
            tag: self.tag.clone().unwrap_or_default(),
            status: self.status.clone().unwrap_or_default(),
            page: self.page.unwrap_or(1).clamp(1, MAX_PAGE),
            select: self.select.is_some(),
            all: self.all.is_some(),
        }
    }
}

struct ListContext<'a> {
    view: ListView,
    notice: Option<&'a str>,
    add: AddForm,
    pick: PickResult,
}

enum PickResult {
    NotAsked,
    Nothing,
    Chosen(Bookmark),
}

async fn render_list(
    state: &SharedState,
    user: &SessionUser,
    context: ListContext<'_>,
) -> Result<BookmarksPage, PageError> {
    let ListContext {
        view,
        notice,
        add,
        pick,
    } = context;
    let filter = Filter::new(
        Some(&view.q),
        Some(&view.tag)
            .filter(|t| !t.is_empty())
            .map(String::as_str),
    )
    .with_status(
        Some(&view.status)
            .filter(|s| !s.is_empty())
            .map(String::as_str),
    );
    let offset = (view.page - 1) * PAGE_SIZE;
    let window = Window::new(Some(PAGE_SIZE + 1), Some(offset));
    let mut found = bookmarks::list(&state.pool, user.user_id, &filter, window).await?;
    let has_next = found.len() > PAGE_SIZE as usize;
    found.truncate(PAGE_SIZE as usize);
    let now = unix_now();
    let rows: Vec<BookmarkView> = found
        .into_iter()
        .map(|bookmark| bookmark_view(bookmark, &filter, now))
        .collect();
    let shell = Shell::load(
        state,
        user,
        Active {
            section: Section::Bookmarks,
            tag: &view.tag,
            status: &view.status,
            notice,
        },
    )
    .await?;
    let (pick, pick_empty) = match pick {
        PickResult::NotAsked => (None, false),
        PickResult::Nothing => (None, true),
        PickResult::Chosen(bookmark) => (Some(bookmark_view(bookmark, &filter, now)), false),
    };
    let summary = format!("Showing {}–{}", offset + 1, offset + rows.len() as u32);
    Ok(BookmarksPage {
        random_href: view.without_select().href_to("/random"),
        select_href: view.with_select().href(),
        done_selecting_href: view.without_select().href(),
        toggle_all_href: view.with_all(!view.all).href(),
        prev_href: (view.page > 1).then(|| view.href_for_page(view.page - 1)),
        next_href: has_next.then(|| view.href_for_page(view.page + 1)),
        empty_text: empty_text(&view),
        shell,
        view,
        add,
        pick,
        pick_empty,
        rows,
        summary,
    })
}

fn empty_text(view: &ListView) -> &'static str {
    let filtered = !view.q.trim().is_empty() || !view.tag.is_empty() || !view.status.is_empty();
    if filtered {
        "No bookmarks match."
    } else {
        "No bookmarks yet. Add one above, or import a file."
    }
}

pub async fn list(
    State(state): State<SharedState>,
    Page(CurrentUser(user)): Page<CurrentUser>,
    Query(query): Query<ListQuery>,
) -> Result<Response, PageError> {
    let context = ListContext {
        view: query.view(),
        notice: query.notice.as_deref(),
        add: AddForm::default(),
        pick: PickResult::NotAsked,
    };
    Ok(render(&render_list(&state, &user, context).await?)?.into_response())
}

pub async fn random(
    State(state): State<SharedState>,
    Page(CurrentUser(user)): Page<CurrentUser>,
    Query(query): Query<ListQuery>,
) -> Result<Response, PageError> {
    let view = query.view();
    let filter = Filter::new(
        Some(&view.q),
        Some(&view.tag)
            .filter(|t| !t.is_empty())
            .map(String::as_str),
    )
    .with_status(
        Some(&view.status)
            .filter(|s| !s.is_empty())
            .map(String::as_str),
    );
    let pick = match bookmarks::random(&state.pool, user.user_id, &filter).await? {
        Some(bookmark) => PickResult::Chosen(bookmark),
        None => PickResult::Nothing,
    };
    let context = ListContext {
        view,
        notice: None,
        add: AddForm::default(),
        pick,
    };
    Ok(render(&render_list(&state, &user, context).await?)?.into_response())
}

#[derive(Deserialize)]
pub struct BookmarkForm {
    url: String,
    #[serde(default)]
    title: String,
    #[serde(default)]
    note: String,
    #[serde(default)]
    tags: String,
}

impl BookmarkForm {
    /// A blank title falls back to the domain, as the old front end did.
    fn into_input(self) -> BookmarkInput {
        let title = match self.title.trim() {
            "" => domain_of(self.url.trim()),
            title => title.to_owned(),
        };
        BookmarkInput {
            url: self.url.trim().to_owned(),
            title,
            note: self.note.trim().to_owned(),
            tags: split_tags(&self.tags),
        }
    }
}

fn split_tags(text: &str) -> Vec<String> {
    text.split(',').map(str::to_owned).collect()
}

pub async fn create(
    State(state): State<SharedState>,
    Page(user): Page<CurrentUser>,
    Form(form): Form<BookmarkForm>,
) -> Result<Response, PageError> {
    let session_user = user.0.clone();
    let add = AddForm {
        open: true,
        url: form.url.clone(),
        title: form.title.clone(),
        note: form.note.clone(),
        tags: form.tags.clone(),
        error: None,
    };
    let outcome =
        bookmark_routes::create(State(state.clone()), user, Json(form.into_input())).await;
    match outcome {
        Ok(_) => Ok(redirect_with_notice("/", Notice::BookmarkSaved)),
        Err(error) => {
            let (status, message) = form_refusal(error)?;
            let context = ListContext {
                view: ListView {
                    page: 1,
                    ..ListView::default()
                },
                notice: None,
                add: AddForm {
                    error: Some(message),
                    ..add
                },
                pick: PickResult::NotAsked,
            };
            render_with_status(status, &render_list(&state, &session_user, context).await?)
        }
    }
}

struct EditPage {
    shell: Shell,
    id: i64,
    url: String,
    title: String,
    note: String,
    tags: String,
    error: Option<String>,
}

async fn edit_shell(state: &SharedState, user: &SessionUser) -> Result<Shell, PageError> {
    let active = Active {
        section: Section::Bookmarks,
        tag: "",
        status: "",
        notice: None,
    };
    Shell::load(state, user, active).await
}

pub async fn edit_form(
    State(state): State<SharedState>,
    Page(CurrentUser(user)): Page<CurrentUser>,
    Path(id): Path<i64>,
) -> Result<Response, PageError> {
    let bookmark = bookmarks::get(&state.pool, user.user_id, id)
        .await?
        .ok_or_else(PageError::not_found)?;
    let page = EditPage {
        shell: edit_shell(&state, &user).await?,
        id,
        url: bookmark.url,
        title: bookmark.title,
        note: bookmark.note,
        tags: bookmark.tags.join(", "),
        error: None,
    };
    Ok(render(&page)?.into_response())
}

pub async fn edit_submit(
    State(state): State<SharedState>,
    Page(user): Page<CurrentUser>,
    Path(id): Path<i64>,
    Form(form): Form<BookmarkForm>,
) -> Result<Response, PageError> {
    let session_user = user.0.clone();
    let page = EditPage {
        shell: edit_shell(&state, &session_user).await?,
        id,
        url: form.url.clone(),
        title: form.title.clone(),
        note: form.note.clone(),
        tags: form.tags.clone(),
        error: None,
    };
    let outcome =
        bookmark_routes::update(State(state), user, Path(id), Json(form.into_input())).await;
    match outcome {
        Ok(_) => Ok(redirect_with_notice("/", Notice::BookmarkSaved)),
        Err(ApiError::NotFound) => Err(PageError::not_found()),
        Err(error) => {
            let (status, message) = form_refusal(error)?;
            render_with_status(
                status,
                &EditPage {
                    error: Some(message),
                    ..page
                },
            )
        }
    }
}

pub(super) struct HiddenField {
    pub(super) name: String,
    pub(super) value: String,
}

pub(super) struct ConfirmDeletePage {
    pub(super) shell: Shell,
    pub(super) heading: String,
    pub(super) detail: String,
    pub(super) action: String,
    pub(super) hidden: Vec<HiddenField>,
    pub(super) cancel_href: String,
}

pub async fn delete_form(
    State(state): State<SharedState>,
    Page(CurrentUser(user)): Page<CurrentUser>,
    Path(id): Path<i64>,
) -> Result<Response, PageError> {
    let bookmark = bookmarks::get(&state.pool, user.user_id, id)
        .await?
        .ok_or_else(PageError::not_found)?;
    let page = ConfirmDeletePage {
        shell: edit_shell(&state, &user).await?,
        heading: "Delete this bookmark?".to_owned(),
        detail: bookmark.title,
        action: "/bookmarks/delete".to_owned(),
        hidden: vec![HiddenField {
            name: "id".to_owned(),
            value: id.to_string(),
        }],
        cancel_href: "/".to_owned(),
    };
    Ok(render(&page)?.into_response())
}

#[derive(Deserialize)]
pub struct DeleteForm {
    id: i64,
}

pub async fn delete_submit(
    State(state): State<SharedState>,
    Page(user): Page<CurrentUser>,
    Form(form): Form<DeleteForm>,
) -> Result<Response, PageError> {
    bookmark_routes::delete(State(state), user, Path(form.id)).await?;
    Ok(redirect_with_notice("/", Notice::BookmarkDeleted))
}

/// What the selection form sent: ticked ids (`id-<n>`), the action, and where to return.
struct BulkRequest {
    ids: Vec<i64>,
    action: String,
    tags: String,
    confirmed: bool,
    view: ListView,
}

impl BulkRequest {
    fn parse(fields: Vec<(String, String)>) -> Self {
        let mut request = Self {
            ids: Vec::new(),
            action: String::new(),
            tags: String::new(),
            confirmed: false,
            view: ListView {
                select: true,
                page: 1,
                ..ListView::default()
            },
        };
        for (name, value) in fields {
            match name.as_str() {
                "action" => request.action = value,
                "tags" => request.tags = value,
                "confirm" => request.confirmed = value == "yes",
                "q" => request.view.q = value,
                "tag" => request.view.tag = value,
                "status" => request.view.status = value,
                "page" => request.view.page = value.parse().unwrap_or(1).clamp(1, MAX_PAGE),
                other => {
                    if let Some(id) = other.strip_prefix("id-").and_then(|id| id.parse().ok()) {
                        request.ids.push(id);
                    }
                }
            }
        }
        request
    }

    fn hidden_fields(&self) -> Vec<HiddenField> {
        let field = |name: &str, value: &str| HiddenField {
            name: name.to_owned(),
            value: value.to_owned(),
        };
        let mut fields = vec![
            field("action", "delete"),
            field("confirm", "yes"),
            field("q", &self.view.q),
            field("tag", &self.view.tag),
            field("status", &self.view.status),
            field("page", &self.view.page.to_string()),
        ];
        fields.extend(self.ids.iter().map(|id| field(&format!("id-{id}"), "1")));
        fields
    }
}

pub async fn bulk(
    State(state): State<SharedState>,
    Page(user): Page<CurrentUser>,
    Form(fields): Form<Vec<(String, String)>>,
) -> Result<Response, PageError> {
    let request = BulkRequest::parse(fields);
    let back = request.view.href();
    if request.ids.is_empty() {
        return Ok(Redirect::to(&with_notice(&back, "nothing-selected")).into_response());
    }
    match request.action.as_str() {
        "check" => check_links(&state, &user.0, request.ids).await?,
        "delete" if !request.confirmed => {
            return confirm_bulk_delete(&state, &user.0, &request).await;
        }
        action => run_bulk(&state, user, action, &request).await?,
    }
    Ok(Redirect::to(&with_notice(&back, "bulk-done")).into_response())
}

fn with_notice(href: &str, key: &str) -> String {
    let separator = if href.contains('?') { '&' } else { '?' };
    format!("{href}{separator}notice={key}")
}

async fn confirm_bulk_delete(
    state: &SharedState,
    user: &SessionUser,
    request: &BulkRequest,
) -> Result<Response, PageError> {
    let count = request.ids.len();
    let page = ConfirmDeletePage {
        shell: edit_shell(state, user).await?,
        heading: format!(
            "Delete {count} bookmark{}?",
            if count == 1 { "" } else { "s" }
        ),
        detail: "This cannot be undone.".to_owned(),
        action: "/bookmarks/bulk".to_owned(),
        hidden: request.hidden_fields(),
        cancel_href: request.view.href(),
    };
    Ok(render(&page)?.into_response())
}

async fn run_bulk(
    state: &SharedState,
    user: CurrentUser,
    action: &str,
    request: &BulkRequest,
) -> Result<(), PageError> {
    let action = match action {
        "delete" => BulkActionInput::Delete,
        "add_tags" => BulkActionInput::AddTags {
            tags: split_tags(&request.tags),
        },
        "remove_tags" => BulkActionInput::RemoveTags {
            tags: split_tags(&request.tags),
        },
        _ => return Err(PageError::bad_request("Choose an action.")),
    };
    let input = BulkInput {
        ids: request.ids.clone(),
        action,
    };
    let _changed = bookmark_routes::bulk(State(state.clone()), user, Json(input)).await?;
    Ok(())
}

/// Without a script there is no live progress, so the page waits for every check to
/// finish and then shows the list again with the results.
async fn check_links(
    state: &SharedState,
    user: &SessionUser,
    ids: Vec<i64>,
) -> Result<(), PageError> {
    let valid = CheckInput { ids }
        .validate()
        .map_err(|error| PageError::bad_request(&error.to_string()))?;
    let slot = state
        .active_checks
        .start(user.user_id)
        .ok_or(PageError::TooManyRequests(CHECK_RETRY_AFTER))?;
    let targets = linkcheck::targets(&state.pool, user.user_id, &valid)
        .await?
        .ok_or_else(PageError::not_found)?;
    let mut running = state.link_checker.start(targets);
    while let Some(checked) = running.next().await {
        linkcheck::store(&state.pool, user.user_id, &[checked], unix_now()).await?;
    }
    drop(slot);
    Ok(())
}
