package mcpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// decodeArgs decodes raw tool arguments into dst (a pointer to a struct with json tags).
// Unknown or mistyped arguments are tool errors (never protocol errors), so a model can
// correct itself. valid lists the argument names of the tool for the message.
func decodeArgs(raw []byte, dst any, valid []string) *mcp.CallToolResult {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	err := dec.Decode(dst)
	if err == nil {
		if dec.More() {
			return toolErr("Invalid arguments: unexpected trailing data.")
		}
		return nil
	}
	sorted := append([]string(nil), valid...)
	sort.Strings(sorted)
	msg := err.Error()
	var ute *json.UnmarshalTypeError
	var syn *json.SyntaxError
	switch {
	case strings.HasPrefix(msg, "json: unknown field "):
		name := strings.Trim(strings.TrimPrefix(msg, "json: unknown field "), `"`)
		hint := ""
		if name == "status" {
			hint = " To change status use move_ticket."
		}
		return toolErr("Unknown argument %q.%s Valid arguments: %s.", clip(name, 80), hint, strings.Join(sorted, ", "))
	case errors.As(err, &ute):
		field := ute.Field
		if i := strings.LastIndex(field, "."); i >= 0 {
			field = field[i+1:]
		}
		if field == "" {
			field = "an argument"
		}
		return toolErr("Argument %s has the wrong type: expected %s.", field, ute.Type.String())
	case errors.As(err, &syn):
		return toolErr("Arguments are not valid JSON.")
	}
	var ute2 *json.UnmarshalTypeError
	if errors.As(err, &ute2) {
		return toolErr("Invalid argument types.")
	}
	return toolErr("Invalid arguments. Valid arguments: %s.", strings.Join(sorted, ", "))
}

// clip truncates s to n runes for echoing user input in messages.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "..."
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// tooLong builds the friendly length message, or nil when within max.
func tooLong(field, v string, max int) *mcp.CallToolResult {
	if n := runeLen(v); n > max {
		return toolErr("%s is too long (%d > %d).", field, n, max)
	}
	return nil
}

func required(field, v string) *mcp.CallToolResult {
	if strings.TrimSpace(v) == "" {
		return toolErr("%s is required.", field)
	}
	return nil
}

// firstErr returns the first non-nil result.
func firstErr(rs ...*mcp.CallToolResult) *mcp.CallToolResult {
	for _, r := range rs {
		if r != nil {
			return r
		}
	}
	return nil
}

func argNames(schema map[string]any) []string {
	props, _ := schema["properties"].(map[string]any)
	out := make([]string, 0, len(props))
	for k := range props {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var _ = fmt.Sprintf
