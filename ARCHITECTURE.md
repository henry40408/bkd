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
