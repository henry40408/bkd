package views

import (
	"fmt"
	"strconv"
	"strings"
)

// statusNames are the link statuses a list can be narrowed to: how a chip names
// the status and what the list heading says.
var statusNames = map[string]struct{ chip, heading string }{
	"broken":     {"Broken", "Broken links"},
	"redirected": {"Moved", "Moved links"},
	"unknown":    {"Not confirmed", "Links not confirmed"},
	"ok":         {"OK", "Links that are OK"},
	"unchecked":  {"Not checked", "Links not checked"},
}

// Filtered says whether the list is narrowed by a search, a tag or a status.
func (v ListView) Filtered() bool {
	return strings.TrimSpace(v.Q) != "" || v.Tag != "" || v.Status != ""
}

// WithoutTag drops the tag filter and goes back to the first page.
func (v ListView) WithoutTag() ListView {
	v.Tag, v.Page = "", 1
	return v
}

// WithoutStatus drops the status filter and goes back to the first page.
func (v ListView) WithoutStatus() ListView {
	v.Status, v.Page = "", 1
	return v
}

// listHeading names what the list shows; a search wins over a tag or a status.
func listHeading(list ListView) string {
	if strings.TrimSpace(list.Q) != "" {
		return "Search results"
	}
	if list.Tag != "" {
		return "Tagged “" + list.Tag + "”"
	}
	if names, known := statusNames[list.Status]; known {
		return names.heading
	}
	if list.Status != "" {
		return "Search results"
	}
	return "All bookmarks"
}

func bookmarkCount(count int64) string {
	if count == 1 {
		return "1 bookmark"
	}
	return strconv.FormatInt(count, 10) + " bookmarks"
}

// countLabel is the number beside the heading: every bookmark, or how many of
// them match.
func countLabel(filtered bool, matching, total int64) string {
	if filtered {
		return fmt.Sprintf("%d of %d", matching, total)
	}
	return bookmarkCount(total)
}

// footText says how much of the list is on the page; empty for an empty list.
func footText(shown, matching int64) string {
	switch {
	case matching == 0:
		return ""
	case shown < matching:
		return fmt.Sprintf("Showing %d of %d", shown, matching)
	case matching == 1:
		return "1 bookmark shown"
	}
	return fmt.Sprintf("All %d bookmarks shown", matching)
}

// selectAllLabel names the select-all control: it can only reach what is shown.
func selectAllLabel(shown, matching int64) string {
	if shown < matching {
		return fmt.Sprintf("Select the %d shown", shown)
	}
	return "Select all"
}
