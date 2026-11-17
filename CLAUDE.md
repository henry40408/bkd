# CLAUDE.md

Guidance for Claude Code working in this repository. bkd is being rewritten from Rust to Go; this file is completed when the Rust code is removed.

## Accepted differences from the Rust server

The Rust server's behaviour is the specification. These differences are known and accepted; statuses and JSON error texts otherwise match.

- JSON request bodies (`decodeJSON`) are more lenient than serde: keys match case-insensitively, a duplicate key takes the last value, `null` in a non-pointer field is ignored, and invalid UTF-8 or a lone surrogate becomes U+FFFD. Rust answers 422 or 400 in those cases.
- Plain-text bodies of framework errors (415, 400, 422, 413, 405) and CLI error messages differ in wording; statuses are the same.
- Internationalised host names go through `idna.Lookup` (STD3 rules and hyphen checks on), where WHATWG turns both off. A rare host that IDNA refuses (for example `-bücher.example`) keeps its Unicode spelling, so a redirect detail can differ in text from Rust. Non-decimal IPv4 spellings and compressed IPv6 are not normalised.
