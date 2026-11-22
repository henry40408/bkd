package views

import "strconv"

// ErrorPage is the page for a refused request: the status, the reason and a
// way back.
func ErrorPage(status int, message string) string {
	code := strconv.Itoa(status)
	card := el("div", []attr{classes("card")},
		el("h1", nil, text(code)),
		el("p", []attr{classes("err"), a("role", "alert")}, text(message)),
		el("p", nil, el("a", []attr{a("href", "/")}, text("Back to bkd"))),
	)
	return bare(code+" · bkd", card)
}

// NoticeText is the fixed text for a notice key. The text never comes from
// the address, so a crafted link cannot put words on the page. It returns ""
// for an unknown key.
func NoticeText(key string) string {
	switch key {
	case "password-changed":
		return "Password changed. Your other browsers were signed out."
	case "session-ended":
		return "That browser was signed out."
	case "others-ended":
		return "All other browsers were signed out."
	case "user-changed":
		return "Saved."
	case "confirmed":
		return "Password confirmed. You can change accounts for the next few minutes."
	case "bookmark-saved":
		return "Bookmark saved."
	case "bookmark-deleted":
		return "Bookmark deleted."
	case "settings-saved":
		return "Settings saved."
	case "bulk-done":
		return "Done."
	case "check-incomplete":
		return "Some links were not checked: a site asked to be left alone, did not answer, " +
			"or the time ran out. Check them again later."
	case "read-incomplete":
		return "Some pages were not read: a site asked to be left alone, did not answer, " +
			"or the time ran out. Read them again later."
	case "nothing-selected":
		return "Tick at least one bookmark first."
	}
	return ""
}
