package netscape

import (
	"strconv"
	"strings"

	"github.com/henry40408/bkd/internal/bookmarks"
)

const fileHeader = `<!DOCTYPE NETSCAPE-Bookmark-file-1>
<META HTTP-EQUIV="Content-Type" CONTENT="text/html; charset=UTF-8">
<TITLE>Bookmarks</TITLE>
<H1>Bookmarks</H1>
<DL><p>
`

// Render writes a flat file: every bookmark carries its tags in TAGS, which
// every importer that understands tags reads, and the rest read the links and
// ignore the attribute.
func Render(list []bookmarks.Bookmark) string {
	var html strings.Builder
	html.WriteString(fileHeader)
	for _, bookmark := range list {
		html.WriteString(`    <DT><A HREF="` + escape(bookmark.URL) + `" ADD_DATE="` + strconv.FormatInt(bookmark.CreatedAt, 10) + `"`)
		if len(bookmark.Tags) > 0 {
			html.WriteString(` TAGS="` + escape(joinedTags(bookmark.Tags)) + `"`)
		}
		html.WriteString(">" + escape(bookmark.Title) + "</A>\n")
		if bookmark.Note != "" {
			html.WriteString("    <DD>" + escape(bookmark.Note) + "\n")
		}
	}
	html.WriteString("</DL><p>\n")
	return html.String()
}

// joinedTags replaces a comma inside a tag, which would split it in two when
// the file is read back.
func joinedTags(tags []string) string {
	safe := make([]string, len(tags))
	for i, tag := range tags {
		safe[i] = strings.ReplaceAll(tag, ",", " ")
	}
	return strings.Join(safe, ",")
}

var escaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

func escape(text string) string { return escaper.Replace(text) }
