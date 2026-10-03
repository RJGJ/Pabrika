package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"

	"github.com/RJGJ/Pabrika/internal/service"
)

// Wire error codes that the service layer does not produce.
const (
	CodeBadRequest       = "bad_request"
	CodeBodyTooLarge     = "body_too_large"
	CodeUnauthorized     = "unauthorized"
	CodeInvalidCreds     = "invalid_credentials"
	CodeOriginMismatch   = "origin_mismatch"
	CodeNotFound         = "not_found"
	CodeMethodNotAllowed = "method_not_allowed"
	CodeUnsupportedMedia = "unsupported_media_type"
	CodeValidationFailed = "validation_failed"
	CodeRateLimited      = "rate_limited"
	CodeInternal         = "internal"
	CodeUnavailable      = "unavailable"
)

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// writeJSON writes v as the JSON response body with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternal, "Internal error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// noContent writes a 204 with no body.
func noContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

// WriteError writes the standard error shape {"error": {"code", "message"}}. It is exported
// because /mcp (phase 4) and the event stream (phase 3) use it too.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorBody{Error: errorDetail{Code: code, Message: message}})
}

// writeFields writes an error carrying per-field messages.
func writeFields(w http.ResponseWriter, status int, code, message string, fields map[string]string) {
	writeJSON(w, status, errorBody{Error: errorDetail{Code: code, Message: message, Fields: fields}})
}

// writeValidation writes a 422 validation_failed with the given fields (the message is the
// lowest-sorted field's).
func writeValidation(w http.ResponseWriter, fields map[string]string) {
	e := service.Validation(fields)
	writeFields(w, http.StatusUnprocessableEntity, CodeValidationFailed, e.Message, fields)
}

// apiError is a fully mapped error ready to be written.
type apiError struct {
	Status  int
	Code    string
	Message string
	Fields  map[string]string
}

func (e apiError) write(w http.ResponseWriter) {
	if e.Fields != nil {
		writeFields(w, e.Status, e.Code, e.Message, e.Fields)
		return
	}
	WriteError(w, e.Status, e.Code, e.Message)
}

// mapError turns a service error into its HTTP form. ok is false for anything that is not a
// *service.Error (the caller must treat it as a 500 and log it; the text is never echoed).
//
// Kind decides the status (validation 422, bad request 400, forbidden 403, not found 404,
// conflict 409) and Code is the wire code, except: every KindValidation error is
// validation_failed (user_not_found and anchor_invalid keep their Fields, which the service
// already keys by the request field), and not_author is sent as forbidden.
func mapError(err error) (e apiError, ok bool) {
	var se *service.Error
	if !errors.As(err, &se) {
		return apiError{Status: http.StatusInternalServerError, Code: CodeInternal, Message: "Internal error"}, false
	}
	e = apiError{Code: se.Code, Message: se.Message}
	switch se.Kind {
	case service.KindValidation:
		e.Status, e.Code, e.Fields = http.StatusUnprocessableEntity, CodeValidationFailed, se.Fields
		if e.Fields == nil {
			e.Fields = map[string]string{}
		}
	case service.KindBadRequest:
		e.Status = http.StatusBadRequest
		if e.Code == "" {
			e.Code = CodeBadRequest
		}
	case service.KindForbidden:
		e.Status = http.StatusForbidden
		if e.Code == "" || e.Code == service.CodeNotAuthor {
			e.Code = service.CodeForbidden
		}
	case service.KindNotFound:
		e.Status = http.StatusNotFound
		if e.Code == "" {
			e.Code = CodeNotFound
		}
	case service.KindConflict:
		e.Status = http.StatusConflict
		if e.Code == "" {
			e.Code = "conflict"
		}
	default:
		return apiError{Status: http.StatusInternalServerError, Code: CodeInternal, Message: "Internal error"}, false
	}
	if e.Message == "" {
		e.Message = e.Code
	}
	return e, true
}

// fail maps err and writes it. Unknown errors are logged with the request id and sent as a
// generic 500.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	e, ok := mapError(err)
	if !ok {
		s.log.Error("request failed", "err", err, "request_id", w.Header().Get("X-Request-Id"), "path", r.URL.Path)
	}
	e.write(w)
}

// ---- request decoding ----

// bodyPresent reports whether the request carries a body: Content-Length > 0, or an unknown
// length (chunked).
func bodyPresent(r *http.Request) bool {
	if r.Body == nil || r.Body == http.NoBody {
		return false
	}
	return r.ContentLength != 0
}

// isJSONContentType accepts application/json with optional parameters.
func isJSONContentType(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	return err == nil && mt == "application/json"
}

// checkMediaType returns a 415 error when a body is present without a JSON Content-Type.
func checkMediaType(r *http.Request) *apiError {
	if bodyPresent(r) && !isJSONContentType(r.Header.Get("Content-Type")) {
		return &apiError{Status: http.StatusUnsupportedMediaType, Code: CodeUnsupportedMedia, Message: "Content-Type must be application/json"}
	}
	return nil
}

var unknownFieldRe = regexp.MustCompile(`unknown field "([^"]*)"`)

// decodeOpts tunes decodeJSON.
type decodeOpts struct {
	// Optional: an absent body is not an error and dst is left untouched.
	Optional bool
	// Hints maps an unknown request field to the message returned for it, e.g.
	// {"status": "status cannot be changed here; use /move"}.
	Hints map[string]string
}

// decodeJSON reads exactly one JSON object from the body into dst and reports success. On
// failure it has already written the response:
//
//	missing or non-JSON Content-Type on a present body  415 unsupported_media_type
//	empty body (unless opts.Optional)                    400 bad_request
//	malformed JSON, non-object, wrong type, trailing data, unknown field   400 bad_request
//	body over the 1 MiB cap                              400 body_too_large
//
// dst is a pointer to a struct with json tags; unknown fields are rejected.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, opts ...decodeOpts) bool {
	var o decodeOpts
	if len(opts) > 0 {
		o = opts[0]
	}
	if e := checkMediaType(r); e != nil {
		e.write(w)
		return false
	}
	missing := func() bool {
		if o.Optional {
			return true
		}
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "Request body is required")
		return false
	}
	if !bodyPresent(r) {
		return missing()
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			WriteError(w, http.StatusBadRequest, CodeBodyTooLarge, "Request body is too large")
			return false
		}
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "Could not read request body")
		return false
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return missing()
	}
	if raw[0] != '{' {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "Request body must be a JSON object")
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeDecodeError(w, err, o)
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "Request body must contain a single JSON object")
		return false
	}
	return true
}

func writeDecodeError(w http.ResponseWriter, err error, o decodeOpts) {
	var ute *json.UnmarshalTypeError
	switch {
	case errors.As(err, &ute):
		msg := "Invalid JSON body"
		if ute.Field != "" {
			msg = fmt.Sprintf("Invalid value for field %q", ute.Field)
		}
		WriteError(w, http.StatusBadRequest, CodeBadRequest, msg)
	case strings.HasPrefix(err.Error(), "json: unknown field"):
		msg := "Unknown field in request body"
		if m := unknownFieldRe.FindStringSubmatch(err.Error()); m != nil {
			msg = fmt.Sprintf("Unknown field %q", m[1])
			if hint, ok := o.Hints[m[1]]; ok {
				msg = hint
			}
		}
		WriteError(w, http.StatusBadRequest, CodeBadRequest, msg)
	default:
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "Malformed JSON body")
	}
}
