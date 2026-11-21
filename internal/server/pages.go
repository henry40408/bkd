package server

import (
	"context"
	"net/http"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/bookmarks"
	"github.com/henry40408/bkd/internal/views"
)

// Forms post to this origin only. A page has no inline script or style, as the
// API's front end never had.
const pageContentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; " +
	"connect-src 'self'; img-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// page wraps one page route. As in the API's stack, the body limit is outermost
// and the origin check innermost, so a refusal carries the page headers too.
func (a *App) page(limit int64, route http.Handler) http.Handler {
	return limitBody(limit, a.bodyTimeout, pageHeaders(a.formOriginRequired(route)))
}

// pageHeaders sets what every page carries. Referrer-Policy is same-origin, not
// no-referrer, because a browser sends Origin: null with a form post under
// no-referrer, which the origin check refuses.
func pageHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("Content-Security-Policy", pageContentSecurityPolicy)
		header.Set("Referrer-Policy", "same-origin")
		header.Set("Cache-Control", "no-store")
		header.Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// formOriginRequired refuses a form post unless its Origin names this site. The
// API lets a request without Origin through, because only a script can send one;
// a browser always sends Origin with a form post, so here a missing one is a
// refusal.
func (a *App) formOriginRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		safe := r.Method == http.MethodGet || r.Method == http.MethodHead
		if !safe && !originNamesThisHost(r) {
			a.writePageError(w, failedPage(http.StatusForbidden, "This request came from another site."))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func originNamesThisHost(r *http.Request) bool {
	if _, sent := r.Header["Origin"]; !sent {
		return false
	}
	return originMatchesHost(r)
}

type pageHandler func(w http.ResponseWriter, r *http.Request) *pageError

type pageUserHandler func(w http.ResponseWriter, r *http.Request, user auth.SessionUser) *pageError

// serve writes the page error a handler gives back.
func (a *App) serve(handle pageHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failure := handle(w, r); failure != nil {
			a.writePageError(w, failure)
		}
	})
}

// pageFor runs the handler for whoever the check lets in. A refusal becomes a
// redirect or an error page instead of JSON.
func (a *App) pageFor(check func(*http.Request) (auth.SessionUser, *apiError), handle pageUserHandler) http.Handler {
	return a.serve(func(w http.ResponseWriter, r *http.Request) *pageError {
		user, fault := check(r)
		if fault == nil {
			return handle(w, r, user)
		}
		if fault.isNotSignedIn() {
			return a.signInAgainFor(r)
		}
		return fromAPI(fault)
	})
}

func (a *App) signedInPage(handle pageUserHandler) http.Handler {
	return a.pageFor(a.currentUser, handle)
}

func (a *App) adminPage(handle pageUserHandler) http.Handler {
	return a.pageFor(a.adminUser, handle)
}

func (a *App) confirmedAdminPage(handle pageUserHandler) http.Handler {
	return a.pageFor(a.confirmedAdmin, handle)
}

// signInAgainFor: a first visit sends no cookie; only a cookie that no longer
// works means a session ended. Only a page that is read can be asked for again;
// a refused form post has no address to return to.
func (a *App) signInAgainFor(r *http.Request) *pageError {
	_, hadCookie := a.readToken(r)
	next := ""
	if address := r.URL.RequestURI(); r.Method == http.MethodGet && sameSitePath(address) {
		next = address
	}
	return signInAgain(next, hadCookie)
}

// Worst first: what needs a look comes before what is fine.
var linkFilters = [...]struct{ status, label string }{
	{"broken", "Broken"},
	{"redirected", "Moved"},
	{"unknown", "Not confirmed"},
	{"ok", "OK"},
	{"unchecked", "Not checked"},
}

// activeSection says which sidebar entry is current and what the list shows.
type activeSection struct {
	section views.Section
	tag     string
	status  string
	notice  string
}

// loadShell builds the page frame around a signed-in screen.
func (a *App) loadShell(ctx context.Context, user auth.SessionUser, active activeSection) (views.Shell, *pageError) {
	tags, err := a.bookmarks.TagCounts(ctx, user.UserID)
	if err != nil {
		return views.Shell{}, fromAPI(internal(err))
	}
	links, err := a.bookmarks.LinkCounts(ctx, user.UserID)
	if err != nil {
		return views.Shell{}, fromAPI(internal(err))
	}
	onList := active.section == views.SectionBookmarks
	// Off the list nothing is current: no status or tag is ever empty.
	currentStatus, currentTag := "", ""
	if onList {
		currentStatus = active.status
		if active.status == "" {
			currentTag = active.tag
		}
	}
	return views.Shell{
		Username:   user.Username,
		IsAdmin:    user.IsAdmin,
		Forwarded:  user.ViaProxy,
		Section:    active.section,
		AllCurrent: onList && active.tag == "" && active.status == "",
		Total:      totalOf(links),
		Statuses:   statusLinks(links, currentStatus),
		Tags:       tagLinks(tags, currentTag),
		Notice:     views.NoticeText(active.notice),
	}, nil
}

// totalOf is how many bookmarks the counts add up to: each one has exactly one status.
func totalOf(counts []bookmarks.LinkCount) int64 {
	var total int64
	for _, count := range counts {
		total += count.Count
	}
	return total
}

// statusLinks lists the statuses that have bookmarks.
func statusLinks(counts []bookmarks.LinkCount, current string) []views.FilterLink {
	var links []views.FilterLink
	for _, filter := range linkFilters {
		for _, count := range counts {
			if count.Status != filter.status {
				continue
			}
			links = append(links, views.FilterLink{
				Label:   filter.label,
				Count:   count.Count,
				Href:    views.WithQuery("/", [][2]string{{"status", filter.status}}),
				Current: current == filter.status,
			})
			break
		}
	}
	return links
}

// tagLinks lists every tag. A status filter replaces the tag filter in the
// sidebar, as it always did: the caller passes no current tag then.
func tagLinks(counts []bookmarks.TagCount, current string) []views.FilterLink {
	links := make([]views.FilterLink, len(counts))
	for i, entry := range counts {
		links[i] = views.FilterLink{
			Label:   entry.Tag,
			Count:   entry.Count,
			Href:    views.WithQuery("/", [][2]string{{"tag", entry.Tag}}),
			Current: current == entry.Tag,
		}
	}
	return links
}

// shellFor is the frame of a page that is not the list.
func (a *App) shellFor(ctx context.Context, user auth.SessionUser, section views.Section, notice string) (views.Shell, *pageError) {
	return a.loadShell(ctx, user, activeSection{section: section, notice: notice})
}

// pagePathID is pathID with the refusal as a page answer.
func pagePathID(r *http.Request) (int64, *pageError) {
	id, failure := pathID(r)
	if failure != nil {
		return 0, plainRefusal(failure)
	}
	return id, nil
}
