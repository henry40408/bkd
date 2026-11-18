package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/henry40408/bkd/internal/search"
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

// filterOf reads the q, tag and status parameters every bookmark query takes.
func filterOf(r *http.Request) search.Filter {
	query := r.URL.Query()
	return search.NewFilter(query.Get("q"), query.Get("tag")).WithStatus(query.Get("status"))
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
			"Failed to deserialize query string: " + name + ": " + integerWording(values[0], err),
		}
	}
	value := uint32(parsed)
	return &value, nil
}

// integerWording words a failed integer parse the way Rust's parser does, which
// the 400 and 422 texts quote.
func integerWording(raw string, err error) string {
	switch {
	case raw == "":
		return "cannot parse integer from empty string"
	case errors.Is(err, strconv.ErrRange) && strings.HasPrefix(raw, "-"):
		return "number too small to fit in target type"
	case errors.Is(err, strconv.ErrRange):
		return "number too large to fit in target type"
	}
	return "invalid digit found in string"
}
