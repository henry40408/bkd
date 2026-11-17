# CLAUDE.md

Guidance for Claude Code working in this repository. bkd is being rewritten from Rust to Go; this file is completed when the Rust code is removed.

## Commands

Run from the repository root with `GOFLAGS=-mod=vendor`.

- Build: `go build -o bin/bkd ./cmd/bkd`
- Run: `go run ./cmd/bkd --db bkd.sqlite3 --addr 127.0.0.1:8080`
- Tests: `go test -race ./...`; single test: `go test ./internal/server -run TestName`. `cmd/bkd/main_test.go` builds the binary once in `TestMain` and execs it; set `BKD_COVERDIR` to build it with `-cover`.
- Lint: `gofmt -l`, `go vet ./...`, staticcheck.

## Configuration (`cmd/bkd`)

- Precedence is flag, then environment variable, then default. Names and ranges are in the README table; `--version` prints `bkd <version>`.
- `BKD_LOG_LEVEL` (`debug|info|warn|error`, default `info`) is environment only: there is no flag and no target or filter syntax. Logs are slog text on stderr.
- `RUST_LOG` is **retired**: setting it, even empty, fails startup with exit code **1** and the text `  RUST_LOG -> BKD_LOG_LEVEL`. The check runs before flags, so it also stops `--version`. A bad value (log level, address, range, boolean) exits with **2**; runtime failures (database, listening) exit with 1.
- Update `legacyEnvVars` and `liveEnvVars` (`cmd/bkd/env.go`, `options.go`) and the README table whenever an environment variable is added, renamed or removed; tests check `liveEnvVars` against the options and the README.
- Like clap: `BKD_SECURE_COOKIES` accepts exactly `true` or `false`; `BKD_FORWARD_AUTH_TRUSTED_PROXIES` splits on commas without dropping empty pieces, so an empty value is an error, not "off" (leave it unset).
- Startup order (`serve.go`): database, password warm-up, setup token log line (before the listener binds), link checker (`linkcheck.New(linkcheck.Config{})`, never `Allow`, which is for tests), background check when the interval is above 0, hourly expired-session sweep (first run at once), then `listening on http://<addr>`. SIGINT and SIGTERM drain for 10 seconds, then close; the database is closed last, which checkpoints the WAL.
- `http.Server.WriteTimeout` stays 0: the link check stream is long-lived.

## Accepted differences from the Rust server

The Rust server's behaviour is the specification. These differences are known and accepted; statuses and JSON error texts otherwise match.

- JSON request bodies (`decodeJSON`) are more lenient than serde: keys match case-insensitively, a duplicate key takes the last value, `null` in a non-pointer field is ignored, and invalid UTF-8 or a lone surrogate becomes U+FFFD. Rust answers 422 or 400 in those cases.
- Plain-text bodies of framework errors (415, 400, 422, 413, 405) and CLI error messages differ in wording; statuses are the same.
- Internationalised host names go through `idna.Lookup` (STD3 rules and hyphen checks on), where WHATWG turns both off. A rare host that IDNA refuses (for example `-bücher.example`) keeps its Unicode spelling, so a redirect detail can differ in text from Rust. Non-decimal IPv4 spellings and compressed IPv6 are not normalised.
