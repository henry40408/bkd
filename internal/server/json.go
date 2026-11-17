package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
)

// writeJSON writes the compact form serde_json produced, without the newline an
// Encoder appends and without escaping < > & (bookmark text is shown as typed).
func writeJSON(w http.ResponseWriter, status int, value any) {
	body, err := marshalJSON(value, "")
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// marshalJSON indents by two spaces when indent is not empty, like serde_json's
// to_string_pretty.
func marshalJSON(value any, indent string) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", indent)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

// textError is a plain-text rejection like the ones axum's extractors send. The
// statuses match the Rust server; the wording follows it where that is cheap.
type textError struct {
	status  int
	message string
}

func (e textError) write(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(e.status)
	_, _ = io.WriteString(w, e.message)
}

// readBody reads the whole body, refusing one over the limit set by limitBody.
func readBody(r *http.Request) ([]byte, *textError) {
	body, err := io.ReadAll(r.Body)
	if err == nil {
		return body, nil
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return nil, &textError{http.StatusRequestEntityTooLarge, "Failed to buffer the request body: length limit exceeded"}
	}
	return nil, &textError{http.StatusBadRequest, "Failed to buffer the request body: " + err.Error()}
}

func isJSONContentType(r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return false
	}
	return mediaType == "application/json" ||
		(strings.HasPrefix(mediaType, "application/") && strings.HasSuffix(mediaType, "+json"))
}

// decodeJSON reads a JSON body the way axum's Json extractor does: 415 for the
// wrong content type, 413 over the limit, 400 for bad syntax, 422 for a document
// that does not fit the type. Fields the Rust type required must be pointers in
// target, because encoding/json does not report a missing field.
func decodeJSON(r *http.Request, target any) *textError {
	if !isJSONContentType(r) {
		return &textError{http.StatusUnsupportedMediaType, "Expected request with `Content-Type: application/json`"}
	}
	body, failure := readBody(r)
	if failure != nil {
		return failure
	}
	if !json.Valid(body) {
		return &textError{http.StatusBadRequest, "Failed to parse the request body as JSON: invalid JSON"}
	}
	if err := json.Unmarshal(body, target); err != nil {
		return &textError{http.StatusUnprocessableEntity, "Failed to deserialize the JSON body into the target type: " + err.Error()}
	}
	return nil
}

// missingField is the 422 for a required field that is absent or null.
func missingField(name string) *textError {
	return &textError{
		http.StatusUnprocessableEntity,
		"Failed to deserialize the JSON body into the target type: missing field `" + name + "`",
	}
}
