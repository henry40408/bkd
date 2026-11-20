package views

import (
	"fmt"
)

// goldenNow makes the January 2020 timestamps in the fixtures read "6 years ago".
const goldenNow = 1_794_900_000

func alice(section Section) Shell {
	return Shell{Username: "alice", IsAdmin: true, Section: section}
}

func emptyShell(username string, isAdmin bool) Shell {
	return Shell{Username: username, IsAdmin: isAdmin, Section: SectionBookmarks, AllCurrent: true}
}

func realRows() []BookmarkRow {
	day := int64(86_400)
	return []BookmarkRow{
		{
			ID: 3, URL: "https://rust-lang.org/", Title: "Rust language", Note: "systems",
			Tags:      []TagChip{{Name: "rust"}},
			Link:      &LinkCheck{Status: "redirected", CheckedAt: 1577900000, Detail: "https://www.rust-lang.org/"},
			CreatedAt: 1577836800 + 3*day,
		},
		{
			ID: 2, URL: "http://example.org/b", Title: "Beta",
			Tags:      []TagChip{{Name: "tools"}, {Name: "web"}},
			Link:      &LinkCheck{Status: "broken", CheckedAt: 1577900000, Detail: "HTTP 404"},
			CreatedAt: 1577836800 + 2*day,
		},
		{
			ID: 1, URL: "https://example.com/a?x=1&y=<2>", Title: `Example "A"`, Note: "A <note> & more",
			Tags:      []TagChip{{Name: "rust"}, {Name: "web"}},
			Link:      &LinkCheck{Status: "ok", CheckedAt: 1577900000},
			CreatedAt: 1577836800 + day,
		},
	}
}

// pageRows are the filler bookmarks "Page <high>" down to "Page <low>".
func pageRows(high, low int) []BookmarkRow {
	var rows []BookmarkRow
	for i := high; i >= low; i-- {
		rows = append(rows, BookmarkRow{
			ID: int64(4 + i), URL: fmt.Sprintf("https://page.example/%d", i), Title: fmt.Sprintf("Page %d", i),
			CreatedAt: 1577836800 + 1000 + int64(i),
		})
	}
	return rows
}

func listView(page uint32) ListView { return ListView{Page: page} }
