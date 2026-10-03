package service

import (
	"bytes"
	"encoding/json"
)

// Optional distinguishes "field omitted" from "set to null" from "set to a value" for PATCH-style updates.
//
// UnmarshalJSON: key absent -> zero Optional; `null` -> Set=true, Null=true, Value=zero; value -> Set=true.
// For non-nullable fields, Null is a validation error ("must not be null").
// For Optional[*string] (due_date, assignee) and Optional[[]string] (labels) Null means "clear".
type Optional[T any] struct {
	Set   bool
	Null  bool
	Value T
}

// Some returns an Optional holding v.
func Some[T any](v T) Optional[T] { return Optional[T]{Set: true, Value: v} }

// Null returns an Optional explicitly set to null.
func Null[T any]() Optional[T] { return Optional[T]{Set: true, Null: true} }

// UnmarshalJSON implements json.Unmarshaler.
func (o *Optional[T]) UnmarshalJSON(b []byte) error {
	var zero T
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		*o = Optional[T]{Set: true, Null: true, Value: zero}
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*o = Optional[T]{Set: true, Value: v}
	return nil
}

// MarshalJSON emits null for an unset or null Optional, else the value.
func (o Optional[T]) MarshalJSON() ([]byte, error) {
	if !o.Set || o.Null {
		return []byte("null"), nil
	}
	return json.Marshal(o.Value)
}
