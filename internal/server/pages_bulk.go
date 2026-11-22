package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/bookmarks"
	"github.com/henry40408/bkd/internal/linkcheck"
	"github.com/henry40408/bkd/internal/views"
)

// Without a script the page is one response, so a check that drags on would end as
// a gateway timeout. Proxies commonly give up after 60 seconds.
const noScriptCheckLimit = 45 * time.Second

const checkRetryAfter = 10 * time.Second

const staleSelectionMessage = "Some of those bookmarks no longer exist. Reload the list."

// bulkRequest is what the selection form sent: ticked ids (id-<n>), the action and
// where to return.
type bulkRequest struct {
	ids       []int64
	action    string
	tags      string
	confirmed bool
	view      views.ListView
}

func parseBulkRequest(fields formFields) bulkRequest {
	request := bulkRequest{view: views.ListView{Select: true, Page: 1}}
	for _, pair := range fields.pairs {
		name, value := pair[0], pair[1]
		switch name {
		case "action":
			request.action = value
		case "tags":
			request.tags = value
		case "confirm":
			request.confirmed = value == "yes"
		case "q":
			request.view.Q = value
		case "tag":
			request.view.Tag = value
		case "status":
			request.view.Status = value
		case "page":
			request.view.Page = parsePage(value)
		default:
			request.addID(name)
		}
	}
	return request
}

// parsePage falls back to the first page for anything that is not a number.
func parsePage(value string) uint32 {
	page, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 1
	}
	return min(max(uint32(page), 1), maxListPage)
}

// addID takes a field named id-<n>; any other name is ignored.
func (r *bulkRequest) addID(name string) {
	digits, found := strings.CutPrefix(name, "id-")
	if !found {
		return
	}
	if id, err := strconv.ParseInt(digits, 10, 64); err == nil {
		r.ids = append(r.ids, id)
	}
}

func (r bulkRequest) hiddenFields() []views.HiddenField {
	hidden := []views.HiddenField{
		{Name: "action", Value: "delete"},
		{Name: "confirm", Value: "yes"},
		{Name: "q", Value: r.view.Q},
		{Name: "tag", Value: r.view.Tag},
		{Name: "status", Value: r.view.Status},
		{Name: "page", Value: strconv.FormatUint(uint64(r.view.Page), 10)},
	}
	for _, id := range r.ids {
		hidden = append(hidden, views.HiddenField{Name: "id-" + strconv.FormatInt(id, 10), Value: "1"})
	}
	return hidden
}

func (a *App) pageBulk(w http.ResponseWriter, r *http.Request, user auth.SessionUser) *pageError {
	fields, failure := formOf(r)
	if failure != nil {
		return failure
	}
	request := parseBulkRequest(fields)
	if len(request.ids) == 0 {
		redirectWithNotice(w, request.view.Href(), "nothing-selected")
		return nil
	}
	if request.action == "delete" && !request.confirmed {
		return a.confirmBulkDelete(w, r, user, request)
	}
	notice, fault := a.runBulk(r.Context(), user, request)
	if fault == nil {
		redirectWithNotice(w, request.view.WithoutSelect().Href(), notice)
		return nil
	}
	return a.bulkRefused(w, r, user, request, fault)
}

// bulkRefused shows the list again in select mode, with the same rows ticked and
// the reason, so that a refused action does not cost the person their selection.
func (a *App) bulkRefused(w http.ResponseWriter, r *http.Request, user auth.SessionUser, request bulkRequest, fault *apiError) *pageError {
	status, message, other := formRefusal(fault)
	if other != nil {
		return other
	}
	bulk := views.BulkState{Selected: request.ids, Tags: request.tags, Error: message}
	return a.writeListPage(w, r, status, user, listContext{view: request.view, bulk: bulk})
}

func (a *App) confirmBulkDelete(w http.ResponseWriter, r *http.Request, user auth.SessionUser, request bulkRequest) *pageError {
	shell, failure := a.editShell(r.Context(), user)
	if failure != nil {
		return failure
	}
	count := len(request.ids)
	heading := "Delete " + strconv.Itoa(count) + " bookmark"
	if count != 1 {
		heading += "s"
	}
	writeHTML(w, http.StatusOK, views.ConfirmDeletePage(views.ConfirmDeleteView{
		Shell: shell, Heading: heading + "?", Detail: "This cannot be undone.",
		Action: "/bookmarks/bulk", Hidden: request.hiddenFields(), CancelHref: request.view.Href(),
	}))
	return nil
}

// runBulk does the action and returns the notice to show afterwards.
func (a *App) runBulk(ctx context.Context, user auth.SessionUser, request bulkRequest) (string, *apiError) {
	switch request.action {
	case "check":
		return a.runPosted(ctx, user, request.ids, a.linkCheckRun())
	case "read":
		return a.runPosted(ctx, user, request.ids, a.pageReadRun())
	}
	input, ok := bulkInput(request)
	if !ok {
		return "", badRequest("Choose an action.")
	}
	changed, fault := a.bulkBookmarks(ctx, user, input)
	if fault != nil && fault.status == http.StatusNotFound {
		return "", staleSelection()
	}
	if fault != nil {
		return "", fault
	}
	if input.Action == bookmarks.BulkRemoveTags && changed == 0 {
		return "", badRequest("None of the selected bookmarks has that tag.")
	}
	return "bulk-done", nil
}

func bulkInput(request bulkRequest) (bookmarks.BulkInput, bool) {
	input := bookmarks.BulkInput{IDs: request.ids, Tags: strings.Split(request.tags, ",")}
	switch request.action {
	case "delete":
		input.Action = bookmarks.BulkDelete
		input.Tags = nil
	case "add_tags":
		input.Action = bookmarks.BulkAddTags
	case "remove_tags":
		input.Action = bookmarks.BulkRemoveTags
	default:
		return bookmarks.BulkInput{}, false
	}
	return input, true
}

func staleSelection() *apiError {
	return &apiError{status: http.StatusNotFound, message: staleSelectionMessage}
}

// postedRun is what differs between a link check and a page read that came as a
// form post.
type postedRun struct {
	start func(context.Context, []linkcheck.Target) *linkcheck.RunningCheck
	// store saves one finished target.
	store func(context.Context, auth.SessionUser, linkcheck.Checked) error
	// incomplete is the notice when a target was skipped or the time ran out.
	incomplete string
}

func (a *App) linkCheckRun() postedRun {
	return postedRun{
		start: func(ctx context.Context, targets []linkcheck.Target) *linkcheck.RunningCheck {
			return a.linkChecker.Start(ctx, targets)
		},
		store: func(ctx context.Context, user auth.SessionUser, checked linkcheck.Checked) error {
			_, err := a.storeChecked(ctx, user, checked)
			return err
		},
		incomplete: "check-incomplete",
	}
}

func (a *App) pageReadRun() postedRun {
	return postedRun{
		start: func(ctx context.Context, targets []linkcheck.Target) *linkcheck.RunningCheck {
			return a.linkChecker.StartReads(ctx, targets, 0)
		},
		store: func(ctx context.Context, user auth.SessionUser, checked linkcheck.Checked) error {
			_, err := a.storeRead(ctx, user, checked)
			return err
		},
		incomplete: "read-incomplete",
	}
}

// runPosted waits for every check or read to finish and then shows the list again
// with the results, as there is no live progress without a script. The notice says
// whether some targets were left out: their site asked to be left alone, or the
// time ran out.
func (a *App) runPosted(ctx context.Context, user auth.SessionUser, ids []int64, run postedRun) (string, *apiError) {
	valid, err := linkcheck.CheckInput{IDs: ids}.Validate()
	if err != nil {
		return "", validationFault(err)
	}
	release, ok := a.activeChecks.Start(user.UserID)
	if !ok {
		return "", tooManyRequests(checkRetryAfter)
	}
	defer release()
	targets, found, err := a.linkStore.Targets(ctx, user.UserID, valid)
	if err != nil {
		return "", internal(err)
	}
	if !found {
		return "", staleSelection()
	}
	complete, err := a.collectRun(ctx, user, targets, run)
	if err != nil {
		return "", internal(err)
	}
	if !complete {
		return run.incomplete, nil
	}
	return "bulk-done", nil
}

// collectRun stores each result as it arrives. It returns false when a target
// was skipped or the time limit ended the wait.
func (a *App) collectRun(ctx context.Context, user auth.SessionUser, targets []linkcheck.Target, run postedRun) (bool, error) {
	ctx, endRun := a.endsWhenStopping(ctx)
	defer endRun()
	waitCtx, cancel := context.WithTimeout(ctx, a.checkLimit)
	defer cancel()
	running := run.start(waitCtx, targets)
	defer running.Cancel()
	complete := true
	for {
		select {
		case <-waitCtx.Done():
			return false, nil
		case event, open := <-running.Events:
			if !open {
				return complete, nil
			}
			if event.Skipped != nil {
				complete = false
				continue
			}
			if err := run.store(ctx, user, *event.Checked); err != nil {
				return false, err
			}
		}
	}
}
