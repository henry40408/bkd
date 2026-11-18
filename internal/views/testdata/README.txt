Reference output captured from the Rust implementation (bkd at bcc032a, debug build).

Each *.golden.html is the raw response body of one request. The first line is an
HTML comment "<!-- METHOD path -> status [Location] -->" written by the capture
script; tests drop that line before comparing.

Capture: start the Rust binary on a throwaway database, set up an admin, create
bookmarks and users through the API, rewrite all timestamps to fixed 2020 values
with sqlite3 (so "6 years ago" stays stable), forge two sessions with fixed
tokens (f*64 for the admin "alice", e*64 for the member "bob"), then request each
page. Dynamic pages (account page with its session list) are not captured.

Go output must never overwrite these files. If Rust output is a bug, say so in the
commit message that changes the golden.
