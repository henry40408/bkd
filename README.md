# bkd

A single-binary, multi-user bookmark manager. Work in progress: only the database layer exists so far.

## Run

```sh
cargo run -- --db bkd.sqlite3
```

| Flag | Environment | Default | Meaning |
| --- | --- | --- | --- |
| `--db` | `BKD_DB` | `bkd.sqlite3` | Path of the SQLite database file. Created if missing. |

SQLite runs in WAL mode, so `bkd.sqlite3-wal` and `bkd.sqlite3-shm` appear next to the database while it is open. Back up with SQLite's backup API (`sqlite3 bkd.sqlite3 ".backup copy.sqlite3"`), or copy all three files together while bkd is stopped.

## Develop

```sh
cargo fmt
cargo clippy --all-targets -- -D warnings
cargo nextest run
```
