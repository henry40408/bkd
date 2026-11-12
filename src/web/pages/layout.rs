//! The page frames and the small parts that every page repeats.

use maud::{DOCTYPE, Markup, html};

use super::support::{FilterLink, Section, Shell};
use super::view::SavedTime;

fn document(title: &str, body: Markup) -> Markup {
    html! {
        (DOCTYPE)
        html lang="en" {
            head {
                meta charset="utf-8";
                meta name="viewport" content="width=device-width, initial-scale=1";
                title { (title) }
                link rel="stylesheet" href="/app.css";
                script src="/app.js" defer {}
            }
            body { (body) }
        }
    }
}

/// A page with no sidebar: sign-in, set-up and errors.
pub fn bare(title: &str, content: Markup) -> Markup {
    document(
        title,
        html! {
            main.login #main { (content) }
        },
    )
}

/// A signed-in page: the sidebar around `content`.
pub fn framed(title: &str, shell: &Shell, content: Markup) -> Markup {
    document(
        title,
        html! {
            div.app {
                input #menu-toggle.sr-only type="checkbox";
                header.topbar {
                    label.menu for="menu-toggle" aria-label="Open menu" { "☰" }
                    p.brand { a href="/" { "bkd" } }
                }
                label.backdrop for="menu-toggle" {}
                (sidebar(shell))
                main #main tabindex="-1" {
                    @if let Some(text) = shell.notice {
                        p.notice role="status" { (text) }
                    }
                    (content)
                }
            }
        },
    )
}

fn current(is_current: bool) -> Option<&'static str> {
    is_current.then_some("page")
}

fn filter_list(filters: &[FilterLink]) -> Markup {
    html! {
        ul {
            @for filter in filters {
                li {
                    a href=(filter.href) aria-current=[current(filter.current)] {
                        (filter.label)
                        @if let Some(count) = filter.count { span.count { (count) } }
                    }
                }
            }
        }
    }
}

fn sidebar(shell: &Shell) -> Markup {
    let on = |section| current(shell.section == section);
    html! {
        nav.side #sidebar aria-label="Filters" {
            p.brand { a href="/" { "bkd" } }
            ul {
                li { a href="/" aria-current=[current(shell.all_current)] { "All bookmarks" } }
            }
            @if !shell.statuses.is_empty() {
                h2 { "Status" }
                (filter_list(&shell.statuses))
            }
            h2 { "Tags" }
            (filter_list(&shell.tags))
            div.me {
                span { "Signed in as " strong { (shell.username) } }
                a href="/transfer" aria-current=[on(Section::Transfer)] { "Import and export" }
                @if !shell.forwarded {
                    a href="/account" aria-current=[on(Section::Account)] { "Account" }
                }
                @if shell.is_admin {
                    a href="/users" aria-current=[on(Section::Users)] { "Manage users" }
                }
                @if !shell.forwarded {
                    form method="post" action="/logout" {
                        button.link type="submit" { "Sign out" }
                    }
                }
            }
        }
    }
}

/// A form's refusal, shown above its button.
pub fn error_line(error: Option<&str>) -> Markup {
    html! {
        @if let Some(error) = error {
            p.err role="alert" { (error) }
        }
    }
}

/// A time with the exact UTC time as its tooltip; the script may show local time.
pub fn time_tag(time: &SavedTime) -> Markup {
    html! {
        time datetime=(time.iso) title=(time.exact) { (time.ago) }
    }
}
