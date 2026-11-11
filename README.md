# bkd

A single-binary, multi-user bookmark manager. Work in progress: the web front end and the JSON API work.

## Run

```sh
cargo run -- --db bkd.sqlite3 --addr 127.0.0.1:8080
```

On the first start, with an empty database, bkd logs a one-time setup token like `WDP4S-V8PGD`. Open the site and enter it, with the first admin's username and password, on the setup page (or send them to `POST /api/setup`). Afterwards, admins manage accounts under "Manage users" (create, promote, demote, disable, reset password, delete); bkd never lets the last active admin be removed. Everyone can change their own password under "Change password": new passwords need at least 12 characters and must not contain the username, and a change signs the account out everywhere else. A session also ends after 7 days without use. "Account" also lists the browsers signed in to your account, where you can end any of them. Admin changes to accounts (create, promote, demote, disable, reset password, delete) ask for your password again if you have not typed it in the last 5 minutes.

| Flag | Environment | Default | Meaning |
| --- | --- | --- | --- |
| `--db` | `BKD_DB` | `bkd.sqlite3` | Path of the SQLite database file. Created if missing. |
| `--addr` | `BKD_ADDR` | `127.0.0.1:8080` | Address to listen on. |
| `--secure-cookies` | `BKD_SECURE_COOKIES` | off | Mark the session cookie `Secure`. Turn on when serving over HTTPS. |

SQLite runs in WAL mode, so `bkd.sqlite3-wal` and `bkd.sqlite3-shm` appear next to the database while it is open. Back up with SQLite's backup API (`sqlite3 bkd.sqlite3 ".backup copy.sqlite3"`), or copy all three files together while bkd is stopped.

## Search

Every word you type must match somewhere in the title, URL, note or a tag. Case is ignored for ASCII only.

| Type | To |
| --- | --- |
| `"exact phrase"` | Match the words together, in order. A quote that is never closed runs to the end. Use `\"` for a quote inside. |
| `-word`, `-"a phrase"` | Leave out bookmarks that match. |
| `title:`, `url:`, `note:` | Match only that field: `url:github.com`. |
| `tag:` | Match a whole tag, ignoring case: `tag:rust`, `tag:"to read"`. |
| `status:` | Match the last link check: `status:broken`, `ok`, `redirected`, `unknown`, or `unchecked` for bookmarks never checked. |

Prefixes combine with the rest: `-tag:old`, `title:"rust book"`. Anything else is plain text, so `https://x.test` and a prefix with nothing after it search for what you typed. A search uses at most 8 terms of 100 characters each.

The address bar keeps where you are: the search, the tag or status you chose in the sidebar, and the "Import and export" and "Manage users" screens (for example `#/?q=rust&tag=web&status=broken`). Reload the page, use Back, or send the link, and you land on the same list. It is only in the address; the server never sees it. The page you scrolled to ("Show more") is not kept.

## Random

The "Random" button next to the search box shows one bookmark picked from what the search and the tag filter show right now (with neither, from all of yours). "Another" picks again; changing the search or the tag, or adding, editing or deleting a bookmark, closes the pick. Over the API: `GET /api/bookmarks/random?q=&tag=` (404 when nothing matches).

## Bulk actions

"Select" next to the search box turns on checkboxes. Tick the bookmarks you want (Shift-click ticks a range, "Select all" ticks every loaded row), then add a tag to all of them, remove a tag from all of them, or delete them. Tags are separated by commas, as in the add form. Delete asks first and cannot be undone. After an action, select mode ends. Over the API: `POST /api/bookmarks/bulk` with `{"ids": [...], "action": "delete" | "add_tags" | "remove_tags", "tags": [...]}` (up to 200 ids; one transaction, so it changes all of them or none; 404 if any id is missing or not yours). It answers `{"changed": n}`.

## Link check

In select mode, tick bookmarks and press "Check links". The server asks each address whether it still answers and shows the result under the bookmark; a bookmark never checked shows nothing. Results appear one by one as the server gets them, and a Stop button ends the check early; what was found so far stays saved. Results (the names below are read out by screen readers; on screen you see the icon and colour):

- **Link OK** (green check, "checked <time>"): the page answers at the saved address.
- **Broken link** (red cross, red background): HTTP 4xx or 5xx (except the ones below), a refused connection or failed DNS lookup, or a redirect loop.
- **Link moved** (amber arrow, amber background, "now at <address>"): the page answers, but at another address. bkd only tells you; it does not change the saved URL.
- **Link not confirmed** (grey question mark): the site refused robots (401, 403, 429), it did not answer within 10 seconds, or the address was not contacted because it is not public.

A result is cleared when you change the bookmark's URL. The sidebar lists each result under "Status" with a count (Broken, Moved, Not confirmed, OK, Not checked; a result with no bookmarks is left out); choose one to show only those bookmarks, or search with `status:broken`.

The server only contacts public addresses, since it makes the requests on behalf of every user. Addresses for private networks, this machine and link-local ranges (such as cloud metadata) are never contacted, including through a redirect or a host name that resolves to one. This means bkd cannot check links to services on your own network. Each request has a 10 second limit and follows at most 5 redirects. One user can run one check at a time (429 otherwise). Over the API: `POST /api/bookmarks/check` with `{"ids": [...]}` (1 to 200 ids; 404 if any id is missing or not yours). It answers with a server-sent event stream: an event `result` with `{"id", "link": {"status", "checked_at", "detail"}}` for each link as soon as it is checked, then `done`. Closing the connection stops the check.

## Import and export

Under "Import and export" in the sidebar, choose the bookmarks file your browser exported (Chrome, Firefox, Safari, Pocket and Raindrop all write the same `.html` format). Folders become tags, the date saved is kept when the file has one (Safari's does not), and a link you already have is skipped, so importing the same file twice is safe. Links that are not web links (`javascript:`, `place:`) are skipped and listed in the result. The same screen downloads your bookmarks as HTML (for a browser or another service) or JSON (a backup).

Over the API: send the file to `POST /api/import` with `Content-Type: text/html` and your session cookie, and read `GET /api/export?format=html|json`.

## Develop

```sh
cargo fmt
cargo clippy --all-targets -- -D warnings
cargo nextest run
```

### Browser tests

The `e2e/` directory holds end-to-end tests that drive the real page in headless Chrome or Chromium. It is a separate Cargo workspace, so `cargo nextest run` never compiles it or opens a browser.

```sh
cd e2e
cargo test --test e2e
```

Chrome or Chromium must be installed; the first run downloads a matching chromedriver. Each scenario starts its own bkd on a free port with a temporary database. Set `BKD_E2E_FEATURES=features/bookmarks.feature` to run one feature.
