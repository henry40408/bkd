# Architecture

## Stack

Rust, axum, sqlx (SQLite) and clap. The front end is plain HTML, CSS and JavaScript in `frontend/`, compiled into the binary with `include_str!`: no htmx, no Node build, no embedding crate.

## Database

`src/db.rs` opens the pool and runs the migrations in `migrations/` at startup (`sqlx::migrate!`). SQL is written with the runtime `sqlx::query` functions, not the `query!` macros, so building needs no database.

Every connection uses WAL, `synchronous = NORMAL`, `foreign_keys = ON` and a 5 second busy timeout. SQLite ignores foreign keys unless each connection enables them, and the cascade deletes below depend on it.

### Schema

- `users`: `username` is 3 to 32 characters of `a-z`, `0-9`, `.`, `_`, `-`, starting and ending with a letter or digit (a `CHECK` enforces it). It is ASCII only because SQLite folds case for ASCII alone, so the `UNIQUE` constraint is case-insensitive on every database. `disabled_at` is a nullable timestamp; `NULL` means active.
- `sessions`: stores a hash of the cookie token, never the token. Deleted with the user.
- `bookmarks`: every row belongs to one user. Every query must filter by `user_id`.
- `bookmark_tags`: one row per tag. Deleted with the bookmark. The application trims and lowercases tags, because SQLite cannot fold non-ASCII case, so tags may be in any script.

Deleting a user deletes their sessions, bookmarks and tags (`ON DELETE CASCADE`). Timestamps are Unix seconds. Tables are `STRICT`, so a value of the wrong type is rejected.

### Portability

Only SQLite is supported. SQL stays plain so a PostgreSQL port would mostly change the id columns, the case-insensitive username handling and the transaction used to serialize first-admin creation. `sqlx::Any` is not used.

## Authentication (`src/auth/`)

- `password`: Argon2id with the crate defaults. New passwords need 8 to 256 bytes; login accepts any password up to 256 bytes, so an older, weaker password still works. Hashing runs on the blocking thread pool. For an unknown or disabled user, login must call `verify_against_dummy_async` so response time does not reveal which usernames exist. `warm_up` builds the dummy hash at startup.
- `session`: the cookie holds 256 random bits; the database holds only their SHA-256. Sessions last 30 days. Every lookup joins `users`, so a disabled account loses access at once and `is_admin` is never cached. Disabling, deleting or resetting the password of a user must call `revoke_all_for_user`.
- `setup_token`: 50 random bits shown as `XXXXX-XXXXX` (Crockford base32). It lives only in memory and is compared as HMACs in constant time. Case, dashes and spaces are ignored, and `O`, `I`, `L` read as `0`, `1`, `1`.
- `first_admin`: one `INSERT ... WHERE NOT EXISTS` inside `BEGIN IMMEDIATE`, so concurrent submissions create exactly one admin.
- `rate_limit`: per-IP, in memory. A caller charges an attempt before checking the secret and refunds it only on success, so parallel requests cannot all pass the check and a success cannot erase earlier failures. Clients are grouped by IPv4 address or IPv6 /64; IPv4-mapped IPv6 addresses count as IPv4. When 10,000 addresses are tracked, new ones are refused until some windows end.
- `username`: the same rules as the `CHECK` in the schema, with ASCII lowercasing first.

## Front end (`frontend/`, served by `src/web/assets.rs`)

- Three files, `index.html`, `app.css` and `app.js`, served at `/`, `/app.css` and `/app.js` with `Cache-Control: no-cache` (they carry no version in their names).
- A strict Content-Security-Policy (`default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; ...`) applies. The page has no inline script, no inline style and no inline event handler, and a test checks this. Keep it that way: an inline one would be blocked. All dynamic text is set with `textContent` or DOM nodes, never `innerHTML`.
- One page has five screens: setup (when `/api/state` says `setup_required`), login, the bookmark list, "Import and export", and "Manage users" (admins only). `/api/state` picks the first screen.
- Search and tag filters run on the server (`q`, `tag`); the page highlights the matched terms. The list loads 50 at a time with a "Show more" button. A search answer that arrives after a newer one is dropped.
- The user screen hides the actions the server would refuse (changing yourself, changing the last active admin). The server enforces the same rules, so a stale screen only gets a 409 and reloads.
- A 401 from any call returns the user to the login screen.
- A bookmark's age ("3 days ago") is a `<time>` element: `datetime` holds the ISO time and the hover text holds the exact date, formatted by `Intl.DateTimeFormat` with the browser's own language and time zone.

## Web API (`src/web/`)

JSON under `/api`. Handlers are thin: they call `auth`, `bookmarks` and `users`.

| Route | Auth | Purpose |
| --- | --- | --- |
| `GET /api/state` | none | `{setup_required, user}`; the front end reads it to pick the first screen |
| `POST /api/setup` | setup token | create the first admin and sign in |
| `POST /api/login`, `POST /api/logout` | none | start and end a session |
| `GET /api/bookmarks?q=&tag=&limit=&offset=` | session | newest first; `limit` defaults to 50, at most 200 |
| `GET /api/bookmarks/random?q=&tag=` | session | one bookmark chosen at random (`ORDER BY RANDOM() LIMIT 1`) from those `q` and `tag` let through; 404 when none |
| `POST /api/bookmarks/check` | session | `{ids}`, 1 to 200; 404 and nothing checked if any id is missing or not the caller's; checks them (HEAD, then GET when HEAD does not succeed) and answers with server-sent events: `result` `{id, link: {status, checked_at, detail}}` per link, stored as it arrives, then `done` |
| `POST /api/bookmarks/bulk` | session | `{ids, action}` with `delete`, `add_tags` or `remove_tags` (+ `tags`); 1 to 200 ids, one transaction; 404 and no change if any id is missing or not the caller's; 400 if a bookmark would exceed 20 tags; answers `{changed}` (bookmarks actually changed) |
| `POST /api/bookmarks`, `PUT`/`DELETE /api/bookmarks/{id}` | session | create, replace, delete |
| `GET /api/tags` | session | the user's tags with counts |
| `POST /api/import` | session | body is a Netscape bookmark file sent as `text/html`; returns `{added, duplicates, invalid, invalid_examples}` |
| `GET /api/export?format=html\|json` | session | the user's own bookmarks as a download (`Content-Disposition: attachment`) |
| `GET /api/users`, `POST /api/users` | admin | list accounts; create one (`username`, `password`, optional `is_admin`) |
| `PUT /api/users/{id}/role` | admin | `{is_admin}` promote or demote |
| `PUT /api/users/{id}/disabled` | admin | `{is_disabled}` disable or enable |
| `PUT /api/users/{id}/password` | admin | `{password}` reset; ends all sessions of that user |
| `DELETE /api/users/{id}` | admin | delete the account with its bookmarks and sessions |

### Import and export (`netscape.rs`, `import.rs`, `web/transfer_routes.rs`)

- **Format.** Import reads the Netscape bookmark HTML that Chrome, Firefox, Safari, Pocket and Raindrop export. `netscape.rs` is a small tolerant tokenizer, not an HTML parser: it knows `A`, `H3`, `DL`, `DD` and `BR`, decodes entities, and survives unclosed tags and truncated input. This avoids a large HTML dependency for a fixed, tiny grammar.
- **Mapping.** Each folder level above a link becomes one tag, and the `TAGS` attribute adds more. `ADD_DATE` (or Pocket's `time_added`) becomes `created_at` (milliseconds and microseconds are scaled down; a missing, zero, negative or future date becomes now). A `<DD>` right after a link is its note.
- **Rules.** Every link goes through the same validation as `POST /api/bookmarks`, with one difference (`BookmarkInput::fitted`): an over-long title, note or tag set is shortened and an unusable tag is dropped, instead of refusing the whole link. A missing title falls back to the URL. A link that is not `http(s)` (`javascript:`, `place:`, `about:`) is skipped and counted, and the report names the first five.
- **Duplicates.** A URL the user already has, or that appears twice in the file, is skipped, so importing the same file twice adds nothing. The match is on the exact URL string, per user.
- **Transaction.** The whole file is inserted in one `BEGIN IMMEDIATE` transaction: a failure leaves nothing behind, and a concurrent write cannot make the upgrade to a writer fail. The write lock is held for the whole insert, so a very large file briefly delays other users' writes.
- **Limits.** The import route accepts up to 20 MiB; every other route keeps 64 KiB.
- **CSRF.** The import body is not JSON, so the route requires `Content-Type: text/html` (else 415). A cross-site form cannot send that type, and a script that sets it needs a CORS preflight, which the server never answers. The `Origin` check still applies.
- **Export.** HTML is a flat file with each bookmark's tags in `TAGS`, in the order created; a comma inside a tag becomes a space so it cannot split. JSON has `url`, `title`, `note`, `tags` and `created_at`. Both contain only the caller's bookmarks. JSON is a backup; only HTML is imported.

### User management

- There is no public registration and no command-line recovery, so the system must never lose its last active admin. Demoting, disabling or deleting an admin runs in a `BEGIN IMMEDIATE` transaction that first checks another active admin remains; otherwise the API answers 409. Two admins removing each other at the same moment are serialized, so exactly one succeeds. A disabled admin does not count.
- An admin may demote, disable or delete themselves while another active admin remains.
- Disabling and password reset delete every session of the user in the same transaction. Enabling again does not bring old sessions back.
- Hashing a password takes tens of milliseconds, and an admin may be demoted in that gap. Each `users::*` change therefore re-checks inside its transaction that the caller is still an active admin. Login does the same: `session::create_if_unchanged` inserts the session only if the account is still active and still has the verified hash, so a reset or disable that lands during verification cannot be followed by a session.
- `AdminUser` reads the role from the database on every request, so a demoted admin is refused at once (403; 401 when not signed in).

- **Ownership.** Every bookmark query carries the session's `user_id`. A bookmark that belongs to someone else answers 404, the same as a missing one.
- **Validation** (`bookmarks.rs`). URLs must be `http://` or `https://` because the front end renders them as links, and a `javascript:` URL would run script. Tags are trimmed, lowercased and deduplicated. Only a `ValidBookmark` can be stored.
- **Link check** (`linkcheck.rs`). The server makes requests to addresses users choose, so the SSRF rule is the core of the module: only public addresses are contacted. A custom DNS resolver drops every non-public address from a name's answers, and the connection uses only what is left, so a second DNS answer cannot change where it connects (no rebinding). IP literals in the URL and in every redirect are checked by `Guard::allows_url`, because the resolver never sees them. IPv4-mapped IPv6 is judged as its IPv4 address. No proxy is used. Only tests build a checker that allows loopback. Targets on one host run one after another; a global semaphore caps requests in flight. The route is a server-sent event stream (POST, so the front end reads it with `fetch`, not `EventSource`): the stream owns the running check and the user's slot, so a client that disconnects or presses Stop drops both and the unfinished requests are aborted. The result (`link_status`, `link_checked_at`, `link_detail`) is stored with a `WHERE url = ?` guard, so an edit during the check is not overwritten, and `bookmarks::update` clears it when the URL changes. Search reads it with `status:`; `COALESCE(link_status, 'unchecked')` keeps `-status:broken` from dropping bookmarks never checked.
- **Bulk actions** (`bulk.rs`). Ownership of every id is checked inside the transaction (`count(*)` of the caller's rows must equal the number of ids), so another user's id behaves like a missing one and nothing leaks or half-applies. Tag rules are the ones in `bookmarks.rs`; the 20-tag limit is checked after the inserts and rolls the batch back. The front end only selects rows it has loaded (`state.selected`), trims the selection after every refresh, and ends select mode after an action.
- **Search** (`search.rs`). `LIKE` with `ESCAPE`, so `%`, `_` and `\` in the query are literal. `q` is parsed into terms (`"phrase"`, `-exclude`, `title:`/`url:`/`note:`/`tag:` prefixes, max 8 terms of 100 chars); every term must match. A plain term matches title, URL, note or a tag; a prefixed term matches that field only, and `tag:` is an exact, case-insensitive tag match. `LIKE` ignores case for ASCII only. Dynamic SQL contains only placeholders; user text is always bound. The front end parses the query again (`parseQuery` in `app.js`) to highlight per field; `tests/search_syntax.json` holds the cases both parsers must agree on, checked by a Rust unit test and an e2e scenario.
- **CSRF.** Three layers: `SameSite=Strict` on the cookie, JSON-only bodies (a form post gets 415), and a check that a state-changing request's `Origin`, if present, equals `Host`. Requests without `Origin` (command-line clients) pass.
- **Rate limits.** Login: 10 attempts per minute per IP. Setup: 5. Successful attempts are refunded; failures are not.
- **Client address.** The peer address of the TCP connection. `X-Forwarded-For` is ignored, so behind a reverse proxy every client shares the proxy's address and one budget. Do not put bkd behind a proxy until trusted-proxy support exists.
- **Host header.** Writes (POST, PUT, DELETE) are refused with 403 when the `Origin` header does not equal `Host`. A proxy that rewrites `Host` breaks every write, so it must pass `Host` through unchanged.
- **Session cookie.** `bkd_session`, or `__Host-bkd_session` when `--secure-cookies` is on, so a sibling subdomain cannot plant a cookie.
- **Shutdown.** SIGTERM and Ctrl-C both stop the server. In-flight requests get 10 seconds to finish.
- **Password work.** Argon2 runs at most as many times at once as there are CPU cores, so a burst of logins cannot exhaust memory.
- **Malformed input.** A body or query that axum cannot parse gets axum's plain-text 4xx response, not the JSON error format.
- **Responses** carry `Cache-Control: no-store` and `X-Content-Type-Options: nosniff`. Bodies over 64 KiB are refused (20 MiB for the import route).
- **Setup token flow.** At startup, if no user exists, a token is generated and logged at WARN. It lives in memory and is cleared when the first admin is created, or when a request finds that someone else won the race.


## Browser tests (`e2e/`)

- A separate Cargo workspace (cucumber + thirtyfour), not part of the root package's build, so the normal build and `cargo nextest run` stay free of a WebDriver client. It is run by hand with `cd e2e && cargo test --test e2e`; there is no CI for it.
- `cargo build` of the root crate runs first, and each scenario starts that binary with a temporary database and a free port. The setup token is read from the log line the server prints at startup.
- A scenario sets up what it needs through the JSON API (`e2e/src/api.rs`) and exercises the behavior it is about through the browser. Features: `features/*.feature`; step code: `tests/e2e/steps/`.
- Edit and Delete show only under a hovering pointer, so the browser is started with a fine-pointer setting and steps hover the row before clicking.
- The double-Enter scenarios slow `fetch` down and watch for `securitypolicyviolation`: the CSP's `form-action 'none'` keeps a native submission out of the address bar, so a missing `preventDefault` shows only as a violation.
- The responsive feature resizes the window through the DevTools protocol and checks the drawer, `inert` and the row actions on a phone. The keyboard feature checks where focus lands after the page rebuilds. Its focus step waits for the focus, then reads it again after 300 ms, because the browser drops focus from a just-hidden control only at its next frame.
