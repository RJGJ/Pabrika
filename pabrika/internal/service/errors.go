package service

import (
	"errors"
	"sort"
)

// Kind classifies an Error so transports can map it mechanically.
type Kind int

const (
	KindValidation Kind = iota + 1 // 422
	KindBadRequest                 // 400
	KindForbidden                  // 403
	KindNotFound                   // 404
	KindConflict                   // 409
)

// Error is the one error type returned by the service layer.
type Error struct {
	Kind    Kind
	Code    string            // stable snake_case
	Message string            // human readable
	Fields  map[string]string // per-field messages, KindValidation only, keyed by JSON field name
}

func (e *Error) Error() string { return e.Message }

// Is matches the sentinels (ErrNotFound, ...) by Kind.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok {
		return false
	}
	return t.Code == "" && t.Kind == e.Kind
}

// Sentinels for errors.Is; they match any *Error of the same Kind.
var (
	ErrNotFound   = &Error{Kind: KindNotFound, Message: "not found"}
	ErrForbidden  = &Error{Kind: KindForbidden, Message: "forbidden"}
	ErrConflict   = &Error{Kind: KindConflict, Message: "conflict"}
	ErrValidation = &Error{Kind: KindValidation, Message: "validation failed"}
	ErrBadRequest = &Error{Kind: KindBadRequest, Message: "bad request"}
)

// NewError builds an Error.
func NewError(kind Kind, code, message string) *Error {
	return &Error{Kind: kind, Code: code, Message: message}
}

// Validation builds a KindValidation error; Message is the message of the lowest-sorted field.
func Validation(fields map[string]string) *Error {
	msg := "validation failed"
	if len(fields) > 0 {
		keys := make([]string, 0, len(fields))
		for k := range fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		msg = fields[keys[0]]
	}
	return &Error{Kind: KindValidation, Code: "validation_failed", Message: msg, Fields: fields}
}

// fieldError is shorthand for a single-field validation error.
func fieldError(field, msg string) *Error { return Validation(map[string]string{field: msg}) }

// Stable codes.
const (
	CodeNotFound          = "not_found"
	CodeForbidden         = "forbidden"
	CodeNotAuthor         = "not_author"
	CodeInsufficientScope = "insufficient_scope"
	CodeSessionRequired   = "session_required"
	CodeValidationFailed  = "validation_failed"
	CodeKeyTaken          = "key_taken"
	CodeAlreadyMember     = "already_member"
	CodeLastOwner         = "last_owner"
	CodeLabelExists       = "label_exists"
	CodeProjectArchived   = "project_archived"
	CodeUserNotFound      = "user_not_found"
	CodeAnchorInvalid     = "anchor_invalid"
	CodeInvalidCursor     = "invalid_cursor"
)

// errNotFound returns the one not-found error used for unknown AND not-visible resources,
// so probing reveals nothing (identical message everywhere).
func errNotFound() *Error { return NewError(KindNotFound, CodeNotFound, "Not found") }

func errForbidden(code, msg string) *Error { return NewError(KindForbidden, code, msg) }

var errNotImplemented = errors.New("not implemented")
