package server

import (
	"encoding/json"
	"net/http"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/bookmarks"
	"github.com/henry40408/bkd/internal/netscape"
)

type exportedBookmark struct {
	URL       string   `json:"url"`
	Title     string   `json:"title"`
	Note      string   `json:"note"`
	Tags      []string `json:"tags"`
	CreatedAt int64    `json:"created_at"`
}

func (a *App) handleExport(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	format, present := r.URL.Query()["format"]
	if !present {
		(&textError{http.StatusBadRequest, "Failed to deserialize query string: missing field `format`"}).write(w)
		return
	}
	contentType, filename, known := exportKind(format[0])
	if !known {
		a.writeAPIError(w, badRequest("format must be html or json"))
		return
	}
	list, err := a.bookmarks.All(r.Context(), user.UserID)
	if err != nil {
		a.writeAPIError(w, internal(err))
		return
	}
	body, err := renderExport(format[0], list)
	if err != nil {
		a.writeAPIError(w, internal(err))
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	_, _ = w.Write(body)
}

func exportKind(format string) (contentType, filename string, known bool) {
	switch format {
	case "html":
		return "text/html; charset=utf-8", "bkd-bookmarks.html", true
	case "json":
		return "application/json", "bkd-bookmarks.json", true
	default:
		return "", "", false
	}
}

func renderExport(format string, list []bookmarks.Bookmark) ([]byte, error) {
	if format == "html" {
		return []byte(netscape.Render(list)), nil
	}
	exported := make([]exportedBookmark, 0, len(list))
	for _, bookmark := range list {
		exported = append(exported, exportedBookmark{
			URL: bookmark.URL, Title: bookmark.Title, Note: bookmark.Note,
			Tags: bookmark.Tags, CreatedAt: bookmark.CreatedAt,
		})
	}
	return json.MarshalIndent(struct {
		Bookmarks []exportedBookmark `json:"bookmarks"`
	}{exported}, "", "  ")
}
