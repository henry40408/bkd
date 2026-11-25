package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// field names a JSON field and whether the request carried it.
type field struct {
	name    string
	present bool
}

// requireFields gives the 422 for the first required field that is absent or null.
func requireFields(fields ...field) *textError {
	for _, f := range fields {
		if !f.present {
			return missingField(f.name)
		}
	}
	return nil
}

// pathID reads a numeric path segment. The text of a refusal follows axum's.
func pathID(r *http.Request) (int64, *textError) {
	raw := r.PathValue("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, &textError{http.StatusBadRequest, "Invalid URL: Cannot parse `" + raw + "` to a `i64`"}
	}
	return id, nil
}

// optionalUint32 reads a query parameter that may be absent. A bad value is a
// 400 naming the parameter.
func optionalUint32(r *http.Request, name string) (*uint32, *textError) {
	values, present := r.URL.Query()[name]
	if !present {
		return nil, nil
	}
	parsed, err := strconv.ParseUint(strings.TrimPrefix(values[0], "+"), 10, 32)
	if err != nil {
		return nil, &textError{
			http.StatusBadRequest,
			"Failed to deserialize query string: " + name + ": " + integerWording(err),
		}
	}
	value := uint32(parsed)
	return &value, nil
}

// integerWording words a failed integer parse for the 400 text. The syntax case
// keeps the phrase the query-invalid sample pins byte for byte.
func integerWording(err error) string {
	if errors.Is(err, strconv.ErrRange) {
		return "number out of range"
	}
	return "invalid digit found in string"
}
