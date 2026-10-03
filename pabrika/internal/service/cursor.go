package service

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

const cursorVersion = 1

type cursorBody struct {
	V int               `json:"v"`
	K []json.RawMessage `json:"k"`
}

func errInvalidCursor() *Error {
	return NewError(KindBadRequest, CodeInvalidCursor, "Invalid cursor")
}

// EncodeCursor builds the opaque cursor: base64url (no padding) of {"v":1,"k":[...]}.
// Keys: tickets [statusRank, position, id]; comments and activity [created_at, id].
func EncodeCursor(keys ...any) string {
	raw := make([]json.RawMessage, len(keys))
	for i, k := range keys {
		b, err := json.Marshal(k)
		if err != nil {
			panic(fmt.Sprintf("service: cursor key %d: %v", i, err))
		}
		raw[i] = b
	}
	b, _ := json.Marshal(cursorBody{V: cursorVersion, K: raw})
	return base64.RawURLEncoding.EncodeToString(b)
}

// CursorKeys are the decoded keys of a cursor.
type CursorKeys []json.RawMessage

// DecodeCursor parses a cursor that must carry exactly n keys. Any malformed,
// wrongly shaped or wrong-version cursor yields ErrBadRequest `invalid_cursor`.
func DecodeCursor(s string, n int) (CursorKeys, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, errInvalidCursor()
	}
	var body cursorBody
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || body.V != cursorVersion || len(body.K) != n || dec.More() {
		return nil, errInvalidCursor()
	}
	return CursorKeys(body.K), nil
}

// Str returns key i as a string.
func (c CursorKeys) Str(i int) (string, error) {
	var v string
	if err := json.Unmarshal(c[i], &v); err != nil {
		return "", errInvalidCursor()
	}
	return v, nil
}

// Float returns key i as a float64 (exact round trip of what EncodeCursor wrote).
func (c CursorKeys) Float(i int) (float64, error) {
	var v float64
	if err := json.Unmarshal(c[i], &v); err != nil {
		return 0, errInvalidCursor()
	}
	return v, nil
}

// Int returns key i as an int64.
func (c CursorKeys) Int(i int) (int64, error) {
	var v int64
	if err := json.Unmarshal(c[i], &v); err != nil {
		return 0, errInvalidCursor()
	}
	return v, nil
}
