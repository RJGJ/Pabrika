package httpapi

import (
	"bytes"
	"encoding/json"

	"github.com/RJGJ/Pabrika/internal/service"
)

// field distinguishes the three states of a PATCH field: absent from the body, present as
// null, or present with a value. Use it in request DTOs; opt() converts to the service type.
//
//	type patchX struct { Name field[string] `json:"name"` }
//
// REST request DTOs live in this package (one per endpoint, with json tags); the service
// input structs carry their own tags and Validate() and may be decoded directly when the
// shapes match.
type field[T any] struct {
	Set   bool // key present in the body (null included)
	Null  bool // value was null
	Value T
}

// UnmarshalJSON implements json.Unmarshaler. It is only called when the key is present.
func (f *field[T]) UnmarshalJSON(b []byte) error {
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		*f = field[T]{Set: true, Null: true}
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*f = field[T]{Set: true, Value: v}
	return nil
}

// opt converts to the service layer's Optional.
func (f field[T]) opt() service.Optional[T] {
	return service.Optional[T]{Set: f.Set, Null: f.Null, Value: f.Value}
}
