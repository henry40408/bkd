package main

import "time"

// demoUser is the first account, so it is an admin and the sidebar shows every
// entry.
const demoUser = "demo"

// linkResult is a stored link check result, since a real check would need the
// network. The zero value means the link was never checked.
type linkResult struct {
	status string
	detail string
}

// demoBookmark is one bookmark with its age, how long ago it was saved.
type demoBookmark struct {
	url   string
	title string
	note  string
	tags  []string
	age   time.Duration
	link  linkResult
}

// days is an age in days. The page prints an age as a bucket (hours,
// "yesterday", days, weeks, months), so each age below sits well inside its
// bucket: the seconds a run takes cannot tip one into the next.
func days(count float64) time.Duration {
	return time.Duration(count * float64(24*time.Hour))
}

var linkOK = linkResult{status: "ok"}

// demoBookmarks run from the newest to the oldest. The ones about SQLite are
// what the search screen finds; the two on reserved .example hosts carry the
// link problems, so no real site is called broken.
var demoBookmarks = []demoBookmark{
	{
		url:   "https://www.sqlite.org/wal.html",
		title: "Write-Ahead Logging",
		note:  "Readers do not block the writer. Read the checkpoint section first.",
		tags:  []string{"sqlite", "databases", "reference"},
		age:   3*time.Hour + 30*time.Minute,
		link:  linkOK,
	},
	{
		url:   "https://go.dev/doc/database/",
		title: "Accessing relational databases",
		note:  "Works with a pure-Go SQLite driver, so the build needs no cgo.",
		tags:  []string{"go", "sqlite", "databases"},
		age:   30 * time.Hour,
		link:  linkOK,
	},
	{
		url:   "https://litestream.io/how-it-works/",
		title: "Litestream: how it works",
		note:  "Streams WAL pages to object storage. Candidate for the nightly backup job.",
		tags:  []string{"sqlite", "backups", "ops"},
		age:   days(2.25),
		link:  linkOK,
	},
	{
		url:   "https://developer.mozilla.org/en-US/docs/Web/CSS/CSS_grid_layout",
		title: "CSS grid layout",
		note:  "Keep the subgrid examples handy for the dashboard redesign.",
		tags:  []string{"css", "design", "reference"},
		age:   days(4.25),
		link:  linkOK,
	},
	{
		url:   "https://blog.fieldnotes.example/posts/sqlite-in-production",
		title: "SQLite in production: field notes",
		note:  "Backup and busy-timeout lessons from a small team. Worth a second read.",
		tags:  []string{"sqlite", "reading"},
		age:   days(6.25),
		link:  linkResult{status: "redirected", detail: "https://fieldnotes.example/sqlite-in-production"},
	},
	{
		url:   "https://sre.google/sre-book/table-of-contents/",
		title: "Site Reliability Engineering",
		note:  "Start with the chapters on service level objectives and monitoring.",
		tags:  []string{"ops", "reading"},
		age:   days(9.25),
		link:  linkOK,
	},
	{
		url:   "https://www.sqlite.org/lang_upsert.html",
		title: "UPSERT",
		note:  "ON CONFLICT DO UPDATE, and the excluded.* columns.",
		tags:  []string{"sqlite", "reference"},
		age:   days(12.25),
		link:  linkOK,
	},
	{
		url:   "https://owasp.org/www-project-top-ten/",
		title: "OWASP Top Ten",
		tags:  []string{"security", "reference"},
		age:   days(15.25),
		link:  linkOK,
	},
	{
		url:   "https://wiki.acme-tools.example/runbooks/restore",
		title: "Restore runbook (old wiki)",
		note:  "Still linked from the on-call page.",
		tags:  []string{"ops", "runbooks"},
		age:   days(22.25),
		link:  linkResult{status: "broken", detail: "HTTP 404"},
	},
	{
		url:   "https://www.w3.org/WAI/WCAG22/quickref/",
		title: "How to Meet WCAG 2.2",
		note:  "Filter by level AA when reviewing a pull request.",
		tags:  []string{"accessibility", "design", "reference"},
		age:   days(26.25),
		link:  linkOK,
	},
	{
		url:   "https://pkg.go.dev/log/slog",
		title: "log/slog",
		note:  "ReplaceAttr is the place to redact a field before it reaches the log.",
		tags:  []string{"go", "logging", "reference"},
		age:   days(40.25),
		link:  linkOK,
	},
	{
		url:   "https://12factor.net/",
		title: "The Twelve-Factor App",
		note:  "Config in the environment, logs as event streams: a good checklist for a new service.",
		tags:  []string{"ops", "reading"},
		age:   days(48.25),
		link:  linkOK,
	},
	{
		url:   "https://www.rfc-editor.org/rfc/rfc9110",
		title: "RFC 9110: HTTP Semantics",
		tags:  []string{"http", "reference"},
		age:   days(75.25),
		link:  linkOK,
	},
	{
		url:   "https://www.postgresql.org/docs/current/indexes.html",
		title: "PostgreSQL documentation: Indexes",
		tags:  []string{"postgres", "databases", "reference"},
		age:   days(100.25),
		link:  linkOK,
	},
	{
		url:   "https://baking.example/no-knead-sourdough",
		title: "No-knead sourdough, start to finish",
		note:  "Twelve hours in the fridge makes the crumb. Bake covered for the first 30 minutes.",
		tags:  []string{"recipes"},
		age:   days(130.25),
	},
	{
		url:   "https://caniuse.com/",
		title: "Can I use",
		tags:  []string{"css", "reference"},
		age:   days(170.25),
		link:  linkOK,
	},
	{
		url:   "https://kubernetes.io/docs/concepts/overview/",
		title: "Kubernetes overview",
		tags:  []string{"ops", "reference"},
		age:   days(210.25),
		link:  linkOK,
	},
}
