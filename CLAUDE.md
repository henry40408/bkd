# CLAUDE.md

Guidance for Claude Code (claude.ai/code) working in this repository.

## Overview

bkd is a single-binary, multi-user bookmark manager (Go, standard library `net/http`, pure-Go SQLite): a web front end rendered on the server, a linkding-compatible JSON API under `/api` (token only; four session routes the pages call sit beside it), a link checker, browser bookmark import and export, and sign-in through a reverse proxy (forward auth). It was rewritten from Rust; the Rust server's behaviour was the specification; the values captured from it that remain are listed under Tests. `ARCHITECTURE.md` describes the design in depth and the README is the user documentation; read the section of `ARCHITECTURE.md` for the area you touch before changing security-related code.

## Commands

Run from the repository root. The Go version comes from `go.mod` alone (`go` + `toolchain` lines); CI and the Dockerfile install nothing version-specific (the Docker image's Go is used when it is at least the `toolchain` line; `GOTOOLCHAIN=auto` only moves up).

- Build: `go build -o bin/bkd ./cmd/bkd`
- Run: `go run ./cmd/bkd --db bkd.sqlite3 --addr 127.0.0.1:8080`
- Tests: `go test -race ./...`; single test: `go test ./internal/server -run TestName`
- Coverage (as CI): `BKD_COVERDIR=$PWD/covdata go test -mod=vendor -coverprofile=coverage.out ./...`, then `go tool covdata textfmt -i=covdata -o coverage-bin.out` and append the second profile to the first without its `mode:` line. `-coverprofile` ignores the binary that `cmd/bkd`'s `TestMain` builds and execs; with `BKD_COVERDIR` set, `TestMain` builds it with `-cover` and sets `GOCOVERDIR` on every child, so the covdata dir holds its coverage. Unset, tests build a plain binary.
- Lint (CI-enforced): `golangci-lint fmt --diff ./...` (must print nothing) and `golangci-lint run ./...`, at the root and again inside `e2e/`. Config is in `.golangci.yml` (defaults plus the gofmt formatter); suppress a finding with `//nolint:<linter> // reason` (`//lint:ignore` is not honoured). `run` does not check formatting, so `fmt --diff` is a separate step. CI installs the binary through `golangci/golangci-lint-action` (`install-only`), so the linter release floats with the action and dependabot tracks only the action.
- Mutation testing (CI-enforced, efficacy at least 95% per shard; about 1330 mutants in all): `.gremlins.yaml` holds the efficacy threshold and the `e2e/` and `vendor/` exclusions. Run `go clean -testcache`, then `GOMAXPROCS=4 nice -n 15 go run github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0 unleash --workers 2 .` (`GOMAXPROCS`, `--workers` and `nice` only limit local load). Gremlins is run through `go run`, not vendored. CI splits the module into six `mutation-shard (<name>)` matrix jobs (server-api, server-pages, auth-users-db, linkcheck, bookmarks-netscape-search, views-cmd), each on its own runner with 165-280 mutants; the `mutation` job `needs` all of them and fails if any shard fails or is cancelled, so branch protection names only `mutation`. gremlins v0.6.0 takes one path, so a shard is the whole module minus a `--exclude-files` regexp (RE2 has no lookahead, so the matrix lists the complement); that keeps coverage computed from every package's tests. Each shard must reach 95 by itself (not a module-wide average), keeps its own zero-mutant guard and uploads its log as an artifact. A new package must be added to a shard, or no shard mutates it. Pushes mutate the files fully; pull requests only the diff. Pitfalls:
  - Never pass `--threshold-efficacy` on the command line: v0.6.0 reads the flag as 0 ("no threshold") and always exits 0. Keep it in `.gremlins.yaml`.
  - A cached coverage run makes gremlins' per-mutant timeout near zero, so most mutants "time out" and the score is meaningless; a run that ends in seconds is suspect.
  - Never pass `--test-cpu`: v0.6.0 builds the single argument "-cpu 2", every mutant fails to build and is counted KILLED.
  - `e2e/` must be excluded: it is a separate module with no coverage profile. `vendor/` is excluded too (`.gremlins.yaml`, and in every shard's `--exclude-files`, which replaces the file's list).
  - A surviving `CONDITIONALS_BOUNDARY` mutant usually means a missing "exactly at the limit" test. The survivors left are equivalent (capacity hints, a slice cut at the limit, log-only branches).
  - `NOT COVERED` mutants are not scored; they point at code the tests never reach.
  - After an interrupted run check `ps`: gremlins and its `*.test` children can stay behind (SIGTERM, then `kill -9`).
- Dependency checks (CI-enforced, the `deps` job): `go mod tidy -diff`, `go mod vendor`, then `git status --porcelain -- vendor go.mod go.sum` must print nothing (`git diff` would miss an untracked vendored package), `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...`. Licences: `GOROOT=$(go env GOROOT) go run github.com/google/go-licenses/v2@v2.0.1 check ./... --include_tests --ignore github.com/henry40408/bkd --allowed_licenses=<list>`, run at the root and again in `e2e/` (with `--ignore github.com/henry40408/bkd/e2e`). The allow-list lives only in the `deps` job's `ALLOWED_LICENSES` in `.github/workflows/ci.yml`; copy it from there, and read the `e2e/` command there for the one difference. `GOROOT` must be set explicitly or go-licenses fails on the standard library. If it reports an unknown or disallowed licence, do not widen the list or swap the module without asking.
- Format before committing: `golangci-lint fmt` on the files you touched.

Third-party modules are **vendored and committed** (`vendor/`); builds and tests use `-mod=vendor` (Go selects it automatically while `vendor/` exists). To add or update a dependency: `go get <module>@<version>`, `go mod tidy`, `go mod vendor`, and commit `go.mod`, `go.sum` and `vendor/` together. A `go get` without `go mod vendor` breaks the build, and the CI `deps` job catches it. Never use a version published less than 7 days ago (Go modules, GitHub Actions, base images, the Go toolchain), indirect ones included; check with `go list -m -json <module>@<version>` (the `Time` field) and pin an older version when needed. Dependabot uses a 7 day cooldown.

### E2E (`e2e/`)

Gherkin scenarios (`e2e/features/*.feature`) run through godog and drive the Go binary in a real browser via chromedp (Chrome DevTools Protocol; there is no driver to download).

- A **separate Go module** with its own `go.mod` and committed `vendor/`; `go test ./...` at the root does not run it. `e2e/go.mod` mirrors the root `go`/`toolchain` lines (CI checks it). Update its dependencies the same way as the root ones, inside `e2e/` (`go get`, `go mod tidy`, `go mod vendor`).
- Run from `e2e/`: `go test -count=1 -v ./...` (`-count=1` because the test cache cannot see a rebuilt `bin/bkd`). One or more features: `E2E_FEATURES=features/bookmarks.feature` (comma-separated).
- Each scenario starts its own bkd (free port, temporary database), its own fake outside web server for link checks and its own Chrome. Scenarios run one at a time.
- Needs a local Chromium or Chrome (chromedp starts it headless). `E2E_CHROME` pins the binary; CI sets it to `/usr/bin/google-chrome`. On macOS `/Applications/Chromium.app` is found.
- The binary is `bin/bkd` (or `E2E_BINARY`), built by the harness only when it is missing. An existing binary is not rebuilt, so rebuild it after changing server code before E2E.
- Every click goes through `clickUntil` (`e2e/internal/harness/click.go`). On CI, Chrome sometimes accepts a click but delivers no event; `clickUntil` then dispatches a scripted `click()` and logs a `WARNING`. A covered control or a broken handler still fails.
- README screenshots: rebuild `bin/bkd`, then `cd e2e && go run ./cmd/screenshots` (needs Chrome; writes `../screenshots/`). The demo bookmarks are made up and written through the API, so it needs no network.
- Run `golangci-lint fmt --diff ./...` and `golangci-lint run ./...` inside `e2e/` before committing (the CI `lint` job does; the `deps` job runs the tidy, vendor, govulncheck and licence checks there too).

### UI changes require screenshot updates

A change to what the site renders (`internal/views/`, `internal/assets/static/`) is committed with the regenerated four images under `screenshots/`, which `README.md` embeds: rebuild `bin/bkd`, then run the screenshot command above.

### Tests

- Unit and integration tests sit beside the code. `internal/dbtest.Open` gives a throwaway migrated database; `internal/server/harness_test.go` builds the app with `httptest`.
- There are no `testdata/` directories: every expected value is written in the test that uses it. Values captured from the last Rust server are never regenerated from Go output; change one only on purpose and say why in the commit message:
  - `internal/db/migrate_test.go`: `rustMigrations` (sqlx's five `_sqlx_migrations` rows with their SHA-384 checksums), `rustSchema` (what the Rust migrations left in `sqlite_master`, byte for byte) and `rustRows` (the migration records with sqlx's `installed_on` and `execution_time`, plus some data). `createRustDatabase` builds a Rust-era database from them; the store must open it unchanged.
  - `internal/auth/password_test.go`: `rustPasswordVectors`, password hashes from the Rust server that must verify.
  - `internal/search/search_test.go`: `TestParseAgreesWithTheRustParser`, the search syntax cases from the Rust parser.
- `cmd/bkd/main_test.go` builds the binary once in `TestMain` and `exec`s it. Read the listening address from the log line `listening on http://<addr>`.

## Architecture

Thin binary (`cmd/bkd`) over internal packages, so tests build the handler directly (`server.New(config).Handler()`).

| Package | Responsibility |
| --- | --- |
| `cmd/bkd` | Flags and environment (`options.go`), `slog` logging (`logging.go`), startup and graceful shutdown (`serve.go`), `legacyEnvVars` (`env.go`) |
| `internal/db` | Opens SQLite (WAL, foreign keys, busy timeout), `WithImmediateTx`, the migration runner and `migrations/*.sql` |
| `internal/dbtest` | Test helper: a fresh migrated database |
| `internal/auth` | Argon2id `Hasher`, sessions, the setup token, first admin, forward auth (`ForwardAuth`), per-IP rate limiter, username rules |
| `internal/users` | Account queries and changes, including the last-admin rule |
| `internal/bookmarks` | `Store` (create, update, list, tags, counts), validation, bulk actions |
| `internal/search` | The search syntax: `Parse`, `Filter`, SQL and highlights |
| `internal/netscape`, `internal/importer` | Netscape bookmark HTML parser and renderer; import into the store |
| `internal/sitemeta` | `Parse`: a page's Open Graph or HTML title and description from its head (no network; the fetch is `linkcheck.Checker.ReadPage`) |
| `internal/truncate` | `Runes`: cut a text to a number of characters, shared by bookmarks, search, importer and server |
| `internal/linkcheck` | The link checker and its SSRF guard (public addresses only) |
| `internal/backgroundcheck` | The periodic background link check |
| `internal/server` | `App`, `Config`, routes, middleware, the four session routes the pages call (`api_*.go`), the linkding API (`linkding_*.go`), pages (`pages_*.go`), shared `service_*.go`, SSE |
| `internal/views` | Page builders returning strings with an escaping builder (`html.go`) |
| `internal/assets` | CSS, JS and icons in `static/`, embedded with `go:embed` |
| `internal/apidocs` | The OpenAPI document of the linkding-compatible API (`openapi.json`, written by hand, names the linkding version it follows) and a vendored Swagger UI, served under `/api/docs` |
| `internal/version` | `Version()` |
| `vendor/` | Vendored third-party modules, committed |

`App` (`internal/server/app.go`) holds the database, the sessions, the hasher, the rate limiters, the setup token (behind a mutex) and the link checker. There is no package-level mutable state.

### Routes and layers

`Handler` (`internal/server/routes.go`) mounts the HTML pages, the static assets, the four session routes under `/api` that `app.js` calls (`mountAPI`: link check, read pages, site preview, export) and the linkding-compatible API (`mountLinkding`, token only); every response goes through `securityHeaders`. `mountAPI` routes add `apiHeaders` and `sameOriginOnly` (a state-changing request whose `Origin` is present must equal `Host`); page routes add `pageHeaders` and, for forms, `formOriginRequired`. Pages and the API handlers share the `service_*.go` functions so the rules exist once. See `ARCHITECTURE.md` for the route table.

### Security design

- Passwords are Argon2id; session tokens and setup tokens are stored or compared as hashes; session cookies are `HttpOnly`, `SameSite=Lax` (`__Host-` prefixed with `--secure-cookies`). Verification must not short-circuit on an unknown or disabled user (`Hasher.VerifyDummy`, same cost as a wrong password).
- Every bookmark query carries the session's `user_id`; another user's bookmark answers 404.
- The first admin needs the one-time setup token logged at startup; it is cleared once the first admin exists.
- The link checker contacts public addresses only, including through redirects and DNS answers. Production wiring is `linkcheck.New(linkcheck.Config{})`; `Allow` is for tests only.
- Never log passwords or tokens.

### Storage and migrations

- `internal/db/migrations/*.sql` are embedded and run by `migrate.go`, which writes sqlx's `_sqlx_migrations` table, descriptions and SHA-384 checksums exactly, so an existing Rust-era database opens as is.
- **Never edit a released migration file** (the checksum is verified). Add a new numbered file. A database that already has a migration newer than the Rust binary knows makes the Rust binary refuse to start, so a rollback to Rust is only possible while no new migration has been applied.
- Writers use `BEGIN IMMEDIATE` (`db.WithImmediateTx`) so concurrent writers wait on the busy timeout.

## Configuration (`cmd/bkd`)

- Precedence is flag, then environment variable, then default. Names and ranges are in the README table; `--version` prints `bkd <version>`.
- `BKD_LOG_LEVEL` (`debug|info|warn|error`, default `info`) is environment only: there is no flag and no target or filter syntax. Logs are slog text on stderr.
- `RUST_LOG` is **retired**: setting it, even empty, fails startup with exit code **1** and the text `  RUST_LOG -> BKD_LOG_LEVEL`. The check runs before flags, so it also stops `--version`. A bad value (log level, address, range, boolean) exits with **2**; runtime failures (database, listening) exit with 1.
- Update `legacyEnvVars` and `liveEnvVars` (`cmd/bkd/env.go`, `options.go`) and the README table whenever an environment variable is added, renamed or removed; tests check `liveEnvVars` against the options and the README. A silently ignored variable is worse than a startup failure.
- Like clap: `BKD_SECURE_COOKIES` accepts exactly `true` or `false`; `BKD_FORWARD_AUTH_TRUSTED_PROXIES` splits on commas without dropping empty pieces, so an empty value is an error, not "off" (leave it unset).
- Startup order (`serve.go`): database, password warm-up, setup token log line (before the listener binds), link checker (`linkcheck.New(linkcheck.Config{})`, never `Allow`, which is for tests), background check when the interval is above 0, hourly expired-session sweep (first run at once), then `listening on http://<addr>`. SIGINT and SIGTERM drain for 10 seconds, then close; the database is closed last, which checkpoints the WAL.
- `http.Server.WriteTimeout` stays 0: the link check stream is long-lived.

## Versioning & Release

The version is never stored in a manifest: `internal/version.Version()` returns the `-ldflags "-X github.com/henry40408/bkd/internal/version.version=$GIT_VERSION"` value when set (not empty, not `dev`), else the module version Go stamps from VCS, else `unknown`. Docker builds pass `GIT_VERSION` as a build arg because `.dockerignore` excludes `.git`. Release with `gh release create --generate-notes`; its tag makes `.github/workflows/docker.yml` publish the versioned image (multi-arch to GHCR), and every push to `main` publishes `main`. Never hand-edit versions or `git tag`.

The image is `gcr.io/distroless/static-debian12:nonroot` (uid 65532; existing volumes depend on it), keeps the database in the `/data` volume and sets `BKD_DB` and `BKD_ADDR`.

## Conventions

- `go.mod` is the only source of the Go version (`go` and `toolchain` lines); `e2e/go.mod` mirrors those two lines (CI checks it).
- Keep the error texts and statuses of the pages and of the four session routes compatible with the Rust server (the expected values in the tests) when touching handlers. The linkding routes follow linkding and Django REST framework instead (`{"detail": ...}`); bkd's own JSON API of the Rust server is gone, so nothing there needs to match.
- A change to the linkding routes or to what they answer is made in `internal/apidocs/openapi.json` too (tests compare its paths, methods and fields with the real ones) and in the README's list of differences. The document names the linkding version it follows (`x-linkding-version`); the README must name the same.
- GitHub Actions are pinned by commit SHA with the tag as a trailing comment (`uses: owner/repo@<sha> # v1.2.3`).

## Accepted differences from the Rust server

The Rust server's behaviour is the specification. These differences are known and accepted; statuses and JSON error texts otherwise match.

- JSON request bodies (`decodeJSON`) are more lenient than serde: keys match case-insensitively, a duplicate key takes the last value, `null` in a non-pointer field is ignored, and invalid UTF-8 or a lone surrogate becomes U+FFFD. Rust answers 422 or 400 in those cases.
- Plain-text bodies of framework errors (415, 400, 422, 413, 405) and CLI error messages differ in wording; statuses are the same.
- The Rust server had a separate Account page (sessions and password). It is merged into Settings: `GET /account` answers 303 to `/settings`, and the three forms post to `/settings/password` and `/settings/sessions/...` (the old `/account/...` addresses are gone). The statuses and error texts of those forms are the same, except that a proxy user who posts to the two session forms is refused with 403 like the password form (the Rust server answered 404 or 303); a refused form shows its error on the Settings page, in its own section.
- Internationalised host names go through `idna.Lookup` (STD3 rules and hyphen checks on), where WHATWG turns both off. A rare host that IDNA refuses (for example `-bücher.example`) keeps its Unicode spelling, so a redirect detail can differ in text from Rust. Non-decimal IPv4 spellings and compressed IPv6 are not normalised.
