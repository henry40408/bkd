# bkd

A single-binary, multi-user bookmark manager. Work in progress: the web front end and the JSON API work.

## Run

```sh
cargo run -- --db bkd.sqlite3 --addr 127.0.0.1:8080
```

On the first start, with an empty database, bkd logs a one-time setup token like `WDP4S-V8PGD`. Open the site and enter it, with the first admin's username and password, on the setup page (or send them to `POST /api/setup`). Afterwards, admins manage accounts under "Manage users" (create, promote, demote, disable, reset password, delete); bkd never lets the last active admin be removed.

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

Prefixes combine with the rest: `-tag:old`, `title:"rust book"`. Anything else is plain text, so `https://x.test` and a prefix with nothing after it search for what you typed. A search uses at most 8 terms of 100 characters each.

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
