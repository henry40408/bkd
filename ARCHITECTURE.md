# Architecture

## Stack

Rust, sqlx (SQLite) and clap today; axum and rust-embed are planned for the web layer. The front end is plain HTML and JavaScript embedded in the binary: no htmx, no Node build.

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

Nothing in the web layer calls these modules yet.

- `password`: Argon2id with the crate defaults. New passwords need 8 to 256 bytes; login accepts any password up to 256 bytes, so an older, weaker password still works. Hashing runs on the blocking thread pool. For an unknown or disabled user, login must call `verify_against_dummy_async` so response time does not reveal which usernames exist. `warm_up` builds the dummy hash at startup.
- `session`: the cookie holds 256 random bits; the database holds only their SHA-256. Sessions last 30 days. Every lookup joins `users`, so a disabled account loses access at once and `is_admin` is never cached. Disabling, deleting or resetting the password of a user must call `revoke_all_for_user`.
- `setup_token`: 50 random bits shown as `XXXXX-XXXXX` (Crockford base32). It lives only in memory and is compared as HMACs in constant time. Case, dashes and spaces are ignored, and `O`, `I`, `L` read as `0`, `1`, `1`.
- `first_admin`: one `INSERT ... WHERE NOT EXISTS` inside `BEGIN IMMEDIATE`, so concurrent submissions create exactly one admin.
- `rate_limit`: per-IP, in memory. A caller charges an attempt before checking the secret and refunds it only on success, so parallel requests cannot all pass the check and a success cannot erase earlier failures. Clients are grouped by IPv4 address or IPv6 /64; IPv4-mapped IPv6 addresses count as IPv4. When 10,000 addresses are tracked, new ones are refused until some windows end.
- `username`: the same rules as the `CHECK` in the schema, with ASCII lowercasing first.

