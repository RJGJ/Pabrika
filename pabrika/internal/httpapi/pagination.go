package httpapi

import (
	"net/http"
	"net/url"
	"strconv"
)

// Pagination defaults and bounds.
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// parseLimit reads the `limit` query parameter: absent or empty is DefaultLimit, above MaxLimit
// is clamped, a non-integer or a value below 1 is a validation problem on `limit`.
func parseLimit(q url.Values) (limit int, fields map[string]string) {
	v := q.Get("limit")
	if v == "" {
		return DefaultLimit, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0, map[string]string{"limit": "must be an integer of 1 or more"}
	}
	if n > MaxLimit {
		n = MaxLimit
	}
	return n, nil
}

// queryOnce returns the single value of a query parameter. dup is true when the parameter was
// given more than once (callers answer 422 for that field).
func queryOnce(q url.Values, name string) (val string, dup bool) {
	vs := q[name]
	switch len(vs) {
	case 0:
		return "", false
	case 1:
		return vs[0], false
	}
	return "", true
}

// pageParams parses limit and cursor together. The cursor is opaque: the service produces and
// parses it, an empty value means the first page. ok is false when the response was written.
func pageParams(w http.ResponseWriter, r *http.Request) (limit int, cursor string, ok bool) {
	q := r.URL.Query()
	limit, fields := parseLimit(q)
	if fields != nil {
		writeValidation(w, fields)
		return 0, "", false
	}
	return limit, q.Get("cursor"), true
}

// listEnvelope is the shape of every list response.
type listEnvelope[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// writeList writes {"items": [...], "next_cursor": string|null}. An empty next means last page.
func writeList[T any](w http.ResponseWriter, items []T, next string) {
	if items == nil {
		items = []T{}
	}
	env := listEnvelope[T]{Items: items}
	if next != "" {
		env.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, env)
}
