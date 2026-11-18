package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/importer"
	"github.com/henry40408/bkd/internal/views"
)

const unknownBrowser = "Unknown browser"

// accountView lists the person's browsers. A user the proxy signed in has no
// password and no sessions here.
func (a *App) accountView(r *http.Request, user auth.SessionUser, notice string) (views.AccountView, *pageError) {
	if fault := refuseProxyUser(user); fault != nil {
		return views.AccountView{}, fromAPI(fault)
	}
	now := auth.UnixNow()
	infos, err := a.sessions.ListForUser(r.Context(), user.UserID, a.callerOf(r).token, now)
	if err != nil {
		return views.AccountView{}, fromAPI(internal(err))
	}
	shell, failure := a.shellFor(r.Context(), user, views.SectionAccount, notice)
	if failure != nil {
		return views.AccountView{}, failure
	}
	rows := make([]views.SessionRow, len(infos))
	for i, info := range infos {
		agent := info.UserAgent
		if agent == "" {
			agent = unknownBrowser
		}
		rows[i] = views.SessionRow{
			ID: info.ID, Agent: agent, IP: info.IP, LastUsedAt: info.LastUsedAt, IsCurrent: info.IsCurrent,
		}
	}
	return views.AccountView{Shell: shell, Now: now, Sessions: rows}, nil
}

func (a *App) pageAccount(w http.ResponseWriter, r *http.Request, user auth.SessionUser) *pageError {
	view, failure := a.accountView(r, user, r.URL.Query().Get("notice"))
	if failure != nil {
		return failure
	}
	writeHTML(w, http.StatusOK, views.AccountPage(view))
	return nil
}

func (a *App) writeAccountError(w http.ResponseWriter, r *http.Request, user auth.SessionUser, refusal formError) *pageError {
	view, failure := a.accountView(r, user, "")
	if failure != nil {
		return failure
	}
	view.Error = refusal.message
	writeHTML(w, refusal.status, views.AccountPage(view))
	return nil
}

// formError is a refusal shown on the form again.
type formError struct {
	status  int
	message string
}

func (a *App) pageChangePassword(w http.ResponseWriter, r *http.Request, user auth.SessionUser) *pageError {
	_, values, failure := requiredFields(r, "current", "new", "again")
	if failure != nil {
		return failure
	}
	current, next, again := values[0], values[1], values[2]
	if next != again {
		return a.writeAccountError(w, r, user, formError{http.StatusBadRequest, "The two new passwords are different."})
	}
	token, fault := a.changePassword(r, a.callerOf(r), user, passwordChange{Current: current, New: next})
	if fault == nil {
		w.Header().Set("Set-Cookie", a.sessionCookie(token))
		redirectWithNotice(w, "/account", "password-changed")
		return nil
	}
	status, message, other := formRefusal(fault)
	if other != nil {
		return other
	}
	return a.writeAccountError(w, r, user, formError{status, message})
}

func (a *App) pageRevokeSession(w http.ResponseWriter, r *http.Request, user auth.SessionUser) *pageError {
	if fault := a.revokeSession(r, user, r.PathValue("id")); fault != nil {
		return fromAPI(fault)
	}
	redirectWithNotice(w, "/account", "session-ended")
	return nil
}

func (a *App) pageRevokeOtherSessions(w http.ResponseWriter, r *http.Request, user auth.SessionUser) *pageError {
	if fault := a.revokeOtherSessions(r, a.callerOf(r), user); fault != nil {
		return fromAPI(fault)
	}
	redirectWithNotice(w, "/account", "others-ended")
	return nil
}

func (a *App) pageSettings(w http.ResponseWriter, r *http.Request, user auth.SessionUser) *pageError {
	shell, failure := a.shellFor(r.Context(), user, views.SectionSettings, r.URL.Query().Get("notice"))
	if failure != nil {
		return failure
	}
	enabled, fault := a.linkCheckEnabled(r.Context(), user)
	if fault != nil {
		return fromAPI(fault)
	}
	writeHTML(w, http.StatusOK, views.SettingsPage(views.SettingsView{
		Shell: shell, LinkCheckEnabled: enabled, ServerChecksLinks: a.backgroundOn,
	}))
	return nil
}

func (a *App) pageSaveSettings(w http.ResponseWriter, r *http.Request, user auth.SessionUser) *pageError {
	fields, failure := formOf(r)
	if failure != nil {
		return failure
	}
	// A ticked checkbox sends a value; an unticked one sends nothing.
	_, enabled := fields.lookup("link_check_enabled")
	if fault := a.setLinkCheckEnabled(r.Context(), user, enabled); fault != nil {
		return fromAPI(fault)
	}
	redirectWithNotice(w, "/settings", "settings-saved")
	return nil
}

func (a *App) writeTransferPage(ctx context.Context, w http.ResponseWriter, status int, user auth.SessionUser, view views.TransferView) *pageError {
	shell, failure := a.shellFor(ctx, user, views.SectionTransfer, "")
	if failure != nil {
		return failure
	}
	view.Shell = shell
	writeHTML(w, status, views.TransferPage(view))
	return nil
}

func (a *App) pageTransfer(w http.ResponseWriter, r *http.Request, user auth.SessionUser) *pageError {
	return a.writeTransferPage(r.Context(), w, http.StatusOK, user, views.TransferView{})
}

const uploadUnreadable = "The upload could not be read."

func (a *App) pageImport(w http.ResponseWriter, r *http.Request, user auth.SessionUser) *pageError {
	file, failure := uploadedFile(r)
	if failure != nil {
		return failure
	}
	if len(file) == 0 {
		view := views.TransferView{Error: "Choose a file first."}
		return a.writeTransferPage(r.Context(), w, http.StatusBadRequest, user, view)
	}
	report, fault := a.importBookmarks(r.Context(), user, decodeLossy(file))
	if fault != nil {
		return fromAPI(fault)
	}
	return a.writeTransferPage(r.Context(), w, http.StatusOK, user, views.TransferView{Report: reportView(report)})
}

func reportView(report importer.Report) *views.ImportReport {
	examples := make([]views.InvalidExample, len(report.InvalidExamples))
	for i, example := range report.InvalidExamples {
		examples[i] = views.InvalidExample{URL: example.URL, Reason: example.Reason}
	}
	return &views.ImportReport{
		Added: int64(report.Added), Duplicates: int64(report.Duplicates),
		Invalid: int64(report.Invalid), Examples: examples,
	}
}

// uploadedFile is the content of the first field named file; empty when there is
// none or it holds nothing.
func uploadedFile(r *http.Request) ([]byte, *pageError) {
	mediaType, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "multipart/form-data" {
		return nil, plainRefusal(&textError{
			http.StatusUnsupportedMediaType,
			"Multipart requests must have `Content-Type: multipart/form-data`",
		})
	}
	boundary := params["boundary"]
	if boundary == "" {
		return nil, plainRefusal(&textError{http.StatusBadRequest, "Invalid `boundary` for `multipart/form-data` request"})
	}
	// A body that ends without the closing delimiter is cut short, which Go's
	// reader reports as a plain end; read it all to tell the two apart.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, pageBadRequest(uploadUnreadable)
	}
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) && bytes.Contains(body, []byte("--"+boundary+"--")) {
			return nil, nil
		}
		if err != nil {
			return nil, pageBadRequest(uploadUnreadable)
		}
		if part.FormName() != "file" {
			continue
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return nil, pageBadRequest(uploadUnreadable)
		}
		return data, nil
	}
}
