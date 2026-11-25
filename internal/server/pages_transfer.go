package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/importer"
	"github.com/henry40408/bkd/internal/views"
)

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
	report, fault := a.importBookmarks(r.Context(), user, strings.ToValidUTF8(string(file), "\uFFFD"))
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
