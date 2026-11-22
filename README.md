# bkd

[![CI](https://github.com/henry40408/bkd/actions/workflows/ci.yml/badge.svg)](https://github.com/henry40408/bkd/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/henry40408/bkd/graph/badge.svg)](https://codecov.io/gh/henry40408/bkd)
[![Release](https://img.shields.io/github/v/release/henry40408/bkd)](https://github.com/henry40408/bkd/releases/latest)
[![License](https://img.shields.io/github/license/henry40408/bkd)](LICENSE.txt)
[![Go version](https://img.shields.io/github/go-mod/go-version/henry40408/bkd)](go.mod)
[![Docker](https://img.shields.io/badge/docker-ghcr.io-blue.svg)](https://ghcr.io/henry40408/bkd)
[![Casual Maintenance Intended](https://casuallymaintained.tech/badge.svg)](https://casuallymaintained.tech/)
[![Vibe Coded](https://img.shields.io/badge/vibe_coded-Claude-d97757?logo=anthropic&logoColor=white)](https://claude.com/claude-code)

A single-binary, multi-user bookmark manager with a web front end and a JSON API. Bookmarks have tags and notes, search has its own syntax, and bkd can check links, import and export browser bookmarks, and sign people in through a reverse proxy such as Authelia.

## Run

### Docker

Images for `linux/amd64` and `linux/arm64` are published to GitHub Container Registry. A release publishes the version tags (`1.2.3`, `1.2`) and `latest`; every merge to `main` publishes `main`, the newest unreleased build. The image runs as a non-root user (uid 65532) and keeps the database in the `/data` volume.

```sh
docker run -d \
  --name bkd \
  -p 8080:8080 \
  -v bkd_data:/data \
  ghcr.io/henry40408/bkd:latest
```

The image sets `BKD_DB=/data/bkd.sqlite3` and `BKD_ADDR=0.0.0.0:8080`. Override them, set `BKD_SECURE_COOKIES=true` when a proxy serves the site over HTTPS, or set `BKD_LOG_LEVEL=debug` for more log lines, with `-e`. A bind mount must be writable by uid 65532.

On the first start, with an empty database, bkd logs the one-time setup token. Read it with `docker logs bkd`.

To sign in through a reverse proxy such as Authelia, set `BKD_FORWARD_AUTH_TRUSTED_PROXIES` to the proxy's address or CIDR range. `BKD_FORWARD_AUTH_USER_HEADER`, `BKD_FORWARD_AUTH_GROUPS_HEADER` and `BKD_FORWARD_AUTH_ADMIN_GROUP` tune it. See "Sign in through a reverse proxy" below.

To build the image yourself, run `docker build -t bkd .`.

### From source

```sh
go run ./cmd/bkd --db bkd.sqlite3 --addr 127.0.0.1:8080
```

### Setup and settings

On the first start, with an empty database, bkd logs a one-time setup token like `WDP4S-V8PGD`. Whoever can read the log can create the first admin, so keep the log private. Open the site and enter it, with the first admin's username and password, on the setup page (or send them to `POST /api/setup`). Afterwards, admins manage accounts under "Manage users" (create, promote, demote, disable, reset password, delete); bkd never lets the last active admin be removed. Everyone can change their own password under "Change password": new passwords need at least 12 characters and must not contain the username, and a change signs the account out everywhere else. A session also ends after 7 days without use. "Account" also lists the browsers signed in to your account, where you can end any of them. Admin changes to accounts (create, promote, demote, disable, reset password, delete) ask for your password again if you have not typed it in the last 5 minutes.

| Flag | Environment | Default | Meaning |
| --- | --- | --- | --- |
| `--db` | `BKD_DB` | `bkd.sqlite3` | Path of the SQLite database file. Created if missing. |
| `--addr` | `BKD_ADDR` | `127.0.0.1:8080` | Address to listen on. |
| `--secure-cookies` | `BKD_SECURE_COOKIES` | off | Mark the session cookie `Secure`. Turn on when serving over HTTPS. |
| `--link-check-interval` | `BKD_LINK_CHECK_INTERVAL` | `0` (off) | Seconds between rounds of the background link check. `0` turns it off for the whole server. See "Link check". |
| `--link-check-batch` | `BKD_LINK_CHECK_BATCH` | `20` | Bookmarks the background link check visits per round, all users together (1 to 200). |
| none | `BKD_LOG_LEVEL` | `info` | Log level: `debug`, `info`, `warn` or `error`. Environment only, no flag. Logs go to stderr. |

`RUST_LOG` is retired: bkd no longer reads it, and refuses to start while it is set, even empty, printing `RUST_LOG -> BKD_LOG_LEVEL`. Rename it (or unset it) to continue.

## Sign in through a reverse proxy (Authelia)

bkd can let a reverse proxy that already signs people in (Authelia, Authentik, oauth2-proxy) tell it who is calling. Set `--forward-auth-trusted-proxies` to the proxy's address and bkd believes the `Remote-User` header, but only on requests that come from that address. Any other client's header is ignored.

| Flag | Environment | Default | Meaning |
| --- | --- | --- | --- |
| `--forward-auth-trusted-proxies` | `BKD_FORWARD_AUTH_TRUSTED_PROXIES` | none (off) | Addresses or CIDR ranges, comma separated, such as `127.0.0.1,172.18.0.0/16`. |
| `--forward-auth-user-header` | `BKD_FORWARD_AUTH_USER_HEADER` | `Remote-User` | Header with the user name. |
| `--forward-auth-groups-header` | `BKD_FORWARD_AUTH_GROUPS_HEADER` | `Remote-Groups` | Header with the groups, comma separated. |
| `--forward-auth-admin-group` | `BKD_FORWARD_AUTH_ADMIN_GROUP` | none | Members of this group are admins and everyone else is a member, checked on every request. Without it, roles stay as admins set them in bkd. |

- A user bkd has not seen is created on the first request. A name that fits bkd's rules (3 to 32 characters: `a-z`, `0-9`, `.`, `_`, `-`) is needed; the name is lowercased. An account of the same name that already has a password is the same person.
- Accounts the proxy makes have no password and cannot sign in without the proxy. They have no "Account" page and no "Sign out" (sign out at the proxy), and admin changes do not ask for a password.
- The proxy decides who is signed in on every request, so signing out at the proxy signs out of bkd. A disabled account still gets no access.
- Without an admin group, the first account in an empty database is the admin, as with the setup token (two requests at once still make only one admin), and everyone after is a member. Anyone who reaches the proxy first becomes the admin, so protect the site before you start bkd, or set an admin group.
- The proxy must replace `Remote-User` and `Remote-Groups` on every request it forwards, and bkd must not be reachable except through it. A header the proxy does not replace passes straight through from the client, and then anyone can name themselves an admin. With nginx:

  ```nginx
  auth_request_set $user   $upstream_http_remote_user;
  auth_request_set $groups $upstream_http_remote_groups;
  proxy_set_header Remote-User   $user;
  proxy_set_header Remote-Groups $groups;
  ```

  With Caddy, use `forward_auth` with `copy_headers Remote-User Remote-Groups`.
- List the proxy's exact address, not a wide range, and do not publish bkd's port. With Docker, a published port can make every client look like the bridge gateway, which would then be trusted. Put bkd on a network only the proxy shares.
- An account of the same name that has a password is the same person, admin role included. Only turn this on if your identity provider does not let people pick their own user names.
- Delete does not remove a proxy user for good: the next request creates the account again. Disable the account instead.
- The rate limit and the session list see the proxy's address, not the end user's.

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

Every screen has its own address: the search, the tag or status you chose in the sidebar, how much of the list is shown (the list grows with "Load more", and `page=2` shows the first 100 bookmarks), and the "Import and export" and "Manage users" screens (for example `/?q=rust&tag=web&status=broken&page=2`). Reload the page, use Back, or send the link, and you land on the same list. bkd also works with JavaScript turned off; the script only makes pages change faster.

## Random

The "Random" button next to the search box shows one bookmark picked from what the search and the tag filter show right now (with neither, from all of yours). "Another" picks again; changing the search or the tag, or adding, editing or deleting a bookmark, closes the pick. Over the API: `GET /api/bookmarks/random?q=&tag=` (404 when nothing matches).

## Bulk actions

"Select" next to the search box turns on checkboxes. Tick the bookmarks you want (Shift-click ticks a range, "Select all" ticks every row shown, and reads "Select the 50 shown" while "Load more" still hides some), then add a tag to all of them, remove a tag from all of them, or delete them. Tags are separated by commas, as in the add form. Delete asks first and cannot be undone. After an action, select mode ends. Over the API: `POST /api/bookmarks/bulk` with `{"ids": [...], "action": "delete" | "add_tags" | "remove_tags", "tags": [...]}` (up to 200 ids; one transaction, so it changes all of them or none; 404 if any id is missing or not yours). It answers `{"changed": n}`.

## Link check

In select mode, tick bookmarks and press "Check links". The server asks each address whether it still answers and shows the result under the bookmark; a bookmark never checked shows nothing. Results appear one by one as the server gets them, and a Stop button ends the check early; what was found so far stays saved. Results (the names below are read out by screen readers; on screen you see the icon and colour):

- **Link OK** (green check, "checked <time>"): the page answers at the saved address.
- **Broken link** (red cross, red background): HTTP 4xx or 5xx (except the ones below), a refused connection or failed DNS lookup, or a redirect loop.
- **Link moved** (amber arrow, amber background, "now at <address>"): the page answers, but at another address. bkd does not change the saved URL by itself, but the edit form shows the new address and offers "Use new address" (with JavaScript on), which fills it in for you to save.
- **Link not confirmed** (grey question mark): the site refused robots (401, 403, 429), it did not answer within 10 seconds, or the address was not contacted because it is not public.

A site that answers 429 or 503 is asked to be left alone, and so is a site that does not answer twice in a row: the check does not ask it about the other bookmarks, and says so ("example.com asked us to slow down, so some links were left unchecked"). Each bookmark gets 10 seconds in all (HEAD and GET together). Without a script the page is one response, so it stops waiting after 45 seconds and tells you that some links were not checked. The background check leaves such a site alone for the `Retry-After` it gave (up to 24 hours), or 1 minute, doubling each time in a row, up to 24 hours; one normal answer starts it over. The wait follows the site that answered, so a short link that redirects to a throttling site does not stop the short-link site. A page behind Cloudflare's bot check (`cf-mitigated: challenge`) is "not confirmed", not broken. The checker identifies itself as `bkd-link-checker/<version>` with the project's address in its `User-Agent`, unless you choose another one (see "User-Agent" below).

A result is cleared when you change the bookmark's URL. The sidebar lists each result under "Status" with a count (Broken, Moved, Not confirmed, OK, Not checked; a result with no bookmarks is left out); choose one to show only those bookmarks, or search with `status:broken`.

The server only contacts public addresses, since it makes the requests on behalf of every user. Addresses for private networks, this machine and link-local ranges (such as cloud metadata) are never contacted, including through a redirect or a host name that resolves to one. This means bkd cannot check links to services on your own network. Each request has a 10 second limit and follows at most 5 redirects. One user can run one check at a time (429 otherwise). Over the API: `POST /api/bookmarks/check` with `{"ids": [...]}` (1 to 200 ids; 404 if any id is missing or not yours). It answers with a server-sent event stream: an event `result` with `{"id", "link": {"status", "checked_at", "detail"}}` for each link as soon as it is checked, then `done`. Closing the connection stops the check.

### Background check

bkd can also check your bookmarks without being asked. It is off until both sides agree: the administrator starts the server with `--link-check-interval` (seconds between rounds), and you tick "Check my bookmarks in the background" under "Settings". Each round checks up to `--link-check-batch` bookmarks of the users who ticked it, never-checked ones first and then the longest unchecked, with users taking turns so one large collection does not use up the rounds. A bookmark with a result less than 7 days old is skipped. Requests to one site are 1.5 seconds apart. Results are the same as for the button, and the same public-address rule applies. Over the API: `GET` and `PUT /api/settings` with `{"link_check_enabled": bool, "link_check_user_agent": string}`.

### User-Agent

Some sites turn away anything that says it is a robot. Under "Settings", "User-Agent" sets the `User-Agent` header that bkd sends to the sites your bookmarks point at: the "Check links" button, the background check, and the add and edit forms when they read a page for its title and description (see "Page details"); the box shows the built-in value as a hint. Leave it empty to keep the built-in one. The setting is yours alone: it does not change what other users' checks send. It is trimmed, at most 200 characters and printable ASCII only (a line break or an accented letter is refused with 400). Over the API, `PUT /api/settings` requires `link_check_enabled`; `link_check_user_agent` is optional there, and leaving it out (or sending `null`) keeps the stored value, while `""` goes back to the built-in one. `GET /api/settings` answers `""` when the built-in value is in use.

## Page details

The add and edit forms read the page behind the address while you fill them in, and show what the page says about itself: its title as the placeholder of the Title field and its description as the placeholder of the Note field. Leave a field blank and it is saved with the page's text; type in it and what you typed is saved. If the page has no title, a blank title becomes the domain, and a blank note stays empty. The form reads the page when the address changes (half a second after you stop typing, or when you leave the field), says "Reading the page…" under the address while it works, and says why when it cannot ("Could not read the page: HTTP 404"); you can still save. A page that failed is not read again until the address changes. "Read the page again" (next to the address, in both forms) reads it now whatever happened before: use it when a read failed for a reason that has passed, or when a saved bookmark's page has changed since it was read. The new title and description appear as the placeholders at once and are saved with the form, so clear the Title or the Note first if you want the page's text instead of your own; a page that says nothing, or cannot be read, leaves the stored details as they are. Saving while the page is still being read waits for the answer, for 5 seconds at most. The edit form reads nothing when it opens: it shows what was stored and reads again only when the address changes or you press the button. Without JavaScript a form cannot read the page, so a blank title becomes the domain, as before.

bkd keeps the page's title and description apart from your own title and note (`site_title`, `site_description`), and your own text is never overwritten by them later. When you change a bookmark's URL, the stored details are replaced by those of the new address, or cleared when the new page could not be read. The details are not shown in the list and are not searched. Open Graph tags (`og:title`, `og:description`) are preferred; without them the page's `<title>` and `<meta name="description">` are used. Text is cleaned to one line (control characters removed) and cut to at most 300 characters for the title and 500 for the description, in the form's placeholders as well as in what is saved. Pages are read as UTF-8 only, and only the first 512 KiB, which is where the head of a page is. Images (`og:image`) are not read or shown.

Reading a page follows the same rules as the link check: public addresses only, at most 5 redirects, 10 seconds, your User-Agent (see above), and the one-at-a-time slot of the link check (429 while one runs). Import and the JSON API do not read pages: a bookmark made through them has no page details, and the API still requires a title.

To read the pages of bookmarks that have none (after an import, for example), go to select mode, tick them and press "Read pages". It works like "Check links": the server reads one page after another from the same site, the progress shows with a Stop button, a site that asks to be left alone is skipped, and when it ends a summary says how many pages were read, how many notes were filled and how many pages could not be read. For each page that says something, the page's title and description are stored as its details, and a note that is blank is filled with the description, so an imported bookmark gets one. Your own title and a note that has text are never changed, and a page that cannot be read or says nothing changes nothing. Up to 200 bookmarks at a time, like the link check, and one at a time with it.

Over the API: `POST /api/bookmarks/read` with `{"ids": [1, 2]}` does the same as the button and answers with server-sent events like `POST /api/bookmarks/check`: `result` (`id`, `site`, `note_filled`), `failed` (`id`, `detail`), `skipped` (`id`, `reason`, `host`), `done`. `POST /api/site-preview` with `{"url": "https://..."}` answers `{"title", "description"}` (empty strings for what the page does not say). Errors: 422 (no `url`), 429 (a check or another read is running), 502 (the page could not be read; the message says why). A bookmark's `site` field is `{"title", "description"}`, or `null` when no page was read; `PUT /api/bookmarks/{id}` keeps it while the URL stays and clears it when the URL changes.

## Import and export

Under "Import and export" in the sidebar, choose the bookmarks file your browser exported (Chrome, Firefox, Safari, Pocket and Raindrop all write the same `.html` format). Folders become tags, the date saved is kept when the file has one (Safari's does not), and a link you already have is skipped, so importing the same file twice is safe. Links that are not web links (`javascript:`, `place:`) are skipped and listed in the result. The same screen downloads your bookmarks as HTML (for a browser or another service) or JSON (a backup).

Over the API: send the file to `POST /api/import` with `Content-Type: text/html` and your session cookie, and read `GET /api/export?format=html|json`.

## Health check

`GET /healthz` needs no sign-in and answers `200 ok` while the database responds, `503 unavailable` otherwise. The Docker image has no shell or `curl`, so it sets no `HEALTHCHECK`; point your orchestrator or proxy at the endpoint and allow a few failures in a row before restarting.

## Develop

```sh
go build -o bin/bkd ./cmd/bkd
go test -race ./...
golangci-lint fmt --diff ./...
golangci-lint run ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
```

Third-party modules are vendored in `vendor/`, so builds need no network.

### Browser tests

The `e2e/` directory holds end-to-end tests that drive the real page in headless Chrome or Chromium. It is a separate Go module, so `go test ./...` at the root never opens a browser.

```sh
go build -o bin/bkd ./cmd/bkd
cd e2e
E2E_CHROME=/path/to/chrome go test -count=1 -v ./...
```

Chrome or Chromium must be installed; `E2E_CHROME` pins the binary (unset, the tests look for one). Each scenario starts its own bkd on a free port with a temporary database. Set `E2E_FEATURES=features/bookmarks.feature` to run one feature (comma-separated for more).

## License

[MIT](LICENSE.txt)
