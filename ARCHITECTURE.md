# Architecture

## Stack

Rust, axum, sqlx (SQLite) and clap; rust-embed will embed the front end. The front end is plain HTML and JavaScript embedded in the binary: no htmx, no Node build.

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

## Web API (`src/web/`)

JSON under `/api`. Handlers are thin: they call `auth`, `bookmarks` and `users`.

| Route | Auth | Purpose |
| --- | --- | --- |
| `GET /api/state` | none | `{setup_required, user}`; the front end reads it to pick the first screen |
| `POST /api/setup` | setup token | create the first admin and sign in |
| `POST /api/login`, `POST /api/logout` | none | start and end a session |
| `GET /api/bookmarks?q=&tag=&limit=&offset=` | session | newest first; `limit` defaults to 50, at most 200 |
| `POST /api/bookmarks`, `PUT`/`DELETE /api/bookmarks/{id}` | session | create, replace, delete |
| `GET /api/tags` | session | the user's tags with counts |
| `GET /api/users`, `POST /api/users` | admin | list accounts; create one (`username`, `password`, optional `is_admin`) |
| `PUT /api/users/{id}/role` | admin | `{is_admin}` promote or demote |
| `PUT /api/users/{id}/disabled` | admin | `{is_disabled}` disable or enable |
| `PUT /api/users/{id}/password` | admin | `{password}` reset; ends all sessions of that user |
| `DELETE /api/users/{id}` | admin | delete the account with its bookmarks and sessions |

### User management

- There is no public registration and no command-line recovery, so the system must never lose its last active admin. Demoting, disabling or deleting an admin runs in a `BEGIN IMMEDIATE` transaction that first checks another active admin remains; otherwise the API answers 409. Two admins removing each other at the same moment are serialized, so exactly one succeeds. A disabled admin does not count.
- An admin may demote, disable or delete themselves while another active admin remains.
- Disabling and password reset delete every session of the user in the same transaction. Enabling again does not bring old sessions back.
- Hashing a password takes tens of milliseconds, and an admin may be demoted in that gap. Each `users::*` change therefore re-checks inside its transaction that the caller is still an active admin. Login does the same: `session::create_if_unchanged` inserts the session only if the account is still active and still has the verified hash, so a reset or disable that lands during verification cannot be followed by a session.
- `AdminUser` reads the role from the database on every request, so a demoted admin is refused at once (403; 401 when not signed in).

- **Ownership.** Every bookmark query carries the session's `user_id`. A bookmark that belongs to someone else answers 404, the same as a missing one.
- **Validation** (`bookmarks.rs`). URLs must be `http://` or `https://` because the front end renders them as links, and a `javascript:` URL would run script. Tags are trimmed, lowercased and deduplicated. Only a `ValidBookmark` can be stored.
- **Search** (`search.rs`). `LIKE` with `ESCAPE`, so `%`, `_` and `\` in the query are literal. Every term must match one of title, URL, note or a tag. `LIKE` ignores case for ASCII only. Dynamic SQL contains only placeholders; user text is always bound.
- **CSRF.** Three layers: `SameSite=Strict` on the cookie, JSON-only bodies (a form post gets 415), and a check that a state-changing request's `Origin`, if present, equals `Host`. Requests without `Origin` (command-line clients) pass.
- **Rate limits.** Login: 10 attempts per minute per IP. Setup: 5. Successful attempts are refunded; failures are not.
- **Client address.** The peer address of the TCP connection. `X-Forwarded-For` is ignored, so behind a reverse proxy every client shares the proxy's address and one budget. Do not put bkd behind a proxy until trusted-proxy support exists.
- **Host header.** Writes (POST, PUT, DELETE) are refused with 403 when the `Origin` header does not equal `Host`. A proxy that rewrites `Host` breaks every write, so it must pass `Host` through unchanged.
- **Session cookie.** `bkd_session`, or `__Host-bkd_session` when `--secure-cookies` is on, so a sibling subdomain cannot plant a cookie.
- **Shutdown.** SIGTERM and Ctrl-C both stop the server. In-flight requests get 10 seconds to finish.
- **Password work.** Argon2 runs at most as many times at once as there are CPU cores, so a burst of logins cannot exhaust memory.
- **Malformed input.** A body or query that axum cannot parse gets axum's plain-text 4xx response, not the JSON error format.
- **Responses** carry `Cache-Control: no-store` and `X-Content-Type-Options: nosniff`. Bodies over 64 KiB are refused.
- **Setup token flow.** At startup, if no user exists, a token is generated and logged at WARN. It lives in memory and is cleared when the first admin is created, or when a request finds that someone else won the race.

