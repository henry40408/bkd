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

## Develop

```sh
cargo fmt
cargo clippy --all-targets -- -D warnings
cargo nextest run
```
