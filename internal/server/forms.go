package server

import (
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// formFields is a decoded application/x-www-form-urlencoded body. It keeps the
// order the browser sent the fields in, which the bulk form needs: its hidden
// fields are written back in the order the rows were ticked.
type formFields struct {
	pairs [][2]string
}

// readForm decodes a form post the way axum's Form extractor does: 415 for another
// content type, 413 over the limit.
func readForm(r *http.Request) (formFields, *textError) {
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "application/x-www-form-urlencoded" {
		return formFields{}, &textError{
			http.StatusUnsupportedMediaType,
			"Form requests must have `Content-Type: application/x-www-form-urlencoded`",
		}
	}
	body, failure := readBody(r)
	if failure != nil {
		return formFields{}, failure
	}
	return parseForm(string(body)), nil
}

func parseForm(body string) formFields {
	var fields formFields
	for part := range strings.SplitSeq(body, "&") {
		if part == "" {
			continue
		}
		name, value, _ := strings.Cut(part, "=")
		fields.pairs = append(fields.pairs, [2]string{unescapeForm(name), unescapeForm(value)})
	}
	return fields
}

// unescapeForm leaves a broken %-escape as typed, like the form parser axum uses.
func unescapeForm(raw string) string {
	decoded, err := url.QueryUnescape(raw)
	if err != nil {
		return strings.ReplaceAll(raw, "+", " ")
	}
	return decoded
}

func (f formFields) lookup(name string) (string, bool) {
	for _, pair := range f.pairs {
		if pair[0] == name {
			return pair[1], true
		}
	}
	return "", false
}

// text is a field that may be absent.
func (f formFields) text(name string) string {
	value, _ := f.lookup(name)
	return value
}

func missingFormField(name string) *textError {
	return &textError{
		http.StatusUnprocessableEntity,
		"Failed to deserialize form body: missing field `" + name + "`",
	}
}

// require reads the named fields in the order the Rust form struct declared them,
// so the first one missing is the one reported.
func (f formFields) require(names ...string) ([]string, *textError) {
	values := make([]string, len(names))
	for i, name := range names {
		value, present := f.lookup(name)
		if !present {
			return nil, missingFormField(name)
		}
		values[i] = value
	}
	return values, nil
}

// integer reads a required field that must be a whole number.
func (f formFields) integer(name string) (int64, *textError) {
	value, present := f.lookup(name)
	if !present {
		return 0, missingFormField(name)
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, &textError{
			http.StatusUnprocessableEntity,
			"Failed to deserialize form body: " + name + ": " + integerWording(err),
		}
	}
	return parsed, nil
}

// boolean reads a required field that must say true or false.
func (f formFields) boolean(name string) (bool, *textError) {
	value, present := f.lookup(name)
	if !present {
		return false, missingFormField(name)
	}
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, &textError{
		http.StatusUnprocessableEntity,
		"Failed to deserialize form body: " + name + ": provided string was not `true` or `false`",
	}
}
