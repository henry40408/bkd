package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/bookmarks"
	"github.com/henry40408/bkd/internal/netscape"
)

// handleImport takes only text/html. A cross-site form cannot send that type, and
// a script that sets it needs a CORS preflight this server never grants, so the
// check keeps the CSRF protection the JSON-only routes get from their content type.
// The body is read first, so a file over the limit is 413 whatever its type.
func (a *App) handleImport(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	body, failure := readBody(r)
	if failure != nil {
		failure.write(w)
		return
	}
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if !strings.HasPrefix(contentType, "text/html") {
		a.writeAPIError(w, unsupportedMediaType("send the file as text/html"))
		return
	}
	report, fault := a.importBookmarks(r.Context(), user, decodeLossy(body))
	if fault != nil {
		a.writeAPIError(w, fault)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

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

// decodeLossy reads a file that may not be UTF-8, giving each invalid sequence one
// U+FFFD as Rust's String::from_utf8_lossy does. strings.ToValidUTF8 would give a
// run of bad bytes one replacement, and the importer would show another title.
func decodeLossy(data []byte) string {
	if utf8.Valid(data) {
		return string(data)
	}
	var text strings.Builder
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r != utf8.RuneError || size > 1 {
			text.Write(data[:size])
			data = data[size:]
			continue
		}
		text.WriteRune(utf8.RuneError)
		data = data[invalidPrefixLength(data):]
	}
	return text.String()
}

// invalidPrefixLength is how many bytes of data[0:] start a UTF-8 sequence that
// then breaks off: the lead byte and each continuation byte allowed after it (the
// "maximal subpart" of the Unicode standard, table 3-7). Always at least one.
func invalidPrefixLength(data []byte) int {
	allowed := continuationRanges(data[0])
	length := 1
	for _, bounds := range allowed {
		if length >= len(data) || data[length] < bounds[0] || data[length] > bounds[1] {
			break
		}
		length++
	}
	return length
}

// continuationRanges lists the byte ranges that may follow a lead byte.
func continuationRanges(lead byte) [][2]byte {
	anyContinuation := [2]byte{0x80, 0xBF}
	switch {
	case lead >= 0xC2 && lead <= 0xDF:
		return [][2]byte{anyContinuation}
	case lead == 0xE0:
		return [][2]byte{{0xA0, 0xBF}, anyContinuation}
	case lead == 0xED:
		return [][2]byte{{0x80, 0x9F}, anyContinuation}
	case lead >= 0xE1 && lead <= 0xEF:
		return [][2]byte{anyContinuation, anyContinuation}
	case lead == 0xF0:
		return [][2]byte{{0x90, 0xBF}, anyContinuation, anyContinuation}
	case lead >= 0xF1 && lead <= 0xF3:
		return [][2]byte{anyContinuation, anyContinuation, anyContinuation}
	case lead == 0xF4:
		return [][2]byte{{0x80, 0x8F}, anyContinuation, anyContinuation}
	default:
		return nil
	}
}
