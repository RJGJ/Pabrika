package mcpserver

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/RJGJ/Pabrika/internal/service"
)

// toolErr builds an isError result with one text block.
func toolErr(format string, args ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}}}
}

const invalidCursorText = "Invalid cursor. Use the next_cursor value from the previous list_tickets result unchanged, or omit cursor to start from the first page."

// errOpts gives mapErr the context of the failing call.
type errOpts struct {
	project   string // resolved project key, for role and archived messages
	ownerOnly bool   // the operation needs the owner role (update_project)
	notFound  string // text for ErrNotFound when not limited ("" = "Not found.")
}

// limitedKey returns the token's project key when it is project-limited.
func (c *call) limitedKey() string {
	if c.p.Token != nil && c.p.Token.ProjectID != "" {
		k := c.p.Token.ProjectKey
		if k == "" {
			k = "?"
		}
		return k
	}
	return ""
}

func (c *call) tokenID() string {
	if c.p.Token != nil {
		return c.p.Token.ID
	}
	return ""
}

// notFound is the not-found text: for a project-limited token always the "limited to" form
// (same text for existing and nonexistent targets, so nothing leaks).
func (c *call) notFound(normal string) *mcp.CallToolResult {
	if k := c.limitedKey(); k != "" {
		return toolErr("Not found. This token is limited to project %s; use project %s and its tickets (%s-<number>).", k, k, k)
	}
	return toolErr("%s", normal)
}

// mapErr turns a service error into the actionable tool error of spec section 8.
func (c *call) mapErr(err error, o errOpts) *mcp.CallToolResult {
	var se *service.Error
	if !errors.As(err, &se) {
		c.h.Logger.Error("mcp tool failed", "tool", c.tool, "token", c.tokenID(), "err", err)
		return toolErr("Internal error. Try again; if it persists, check the server logs.")
	}
	proj := o.project
	if proj == "" {
		proj = "this project"
	}
	switch se.Kind {
	case service.KindNotFound:
		if o.notFound == "" {
			o.notFound = "Not found."
		}
		return c.notFound(o.notFound)
	case service.KindForbidden:
		switch se.Code {
		case service.CodeInsufficientScope:
			return toolErr("This token has read-only scope. Create a write token in Settings to make changes.")
		case service.CodeNotAuthor:
			return toolErr("You can only edit comments written by this token.")
		}
		need := "Editors and owners can do this."
		if o.ownerOnly {
			need = "Only owners can update a project."
		}
		if o.project != "" {
			if d, gerr := c.h.Services.Projects.Get(c.ctx, c.actor, o.project); gerr == nil {
				return toolErr("Your role in %s is %s. %s", o.project, d.Role, need)
			}
		}
		return toolErr("Your role in %s is too low for this action.", proj)
	case service.KindConflict:
		switch se.Code {
		case service.CodeProjectArchived:
			return toolErr("Project %s is archived and read-only. Ask an owner to unarchive it (update_project with archived: false) before making changes.", proj)
		case service.CodeKeyTaken:
			return toolErr("That project key is already taken. Choose another key.")
		case service.CodeLabelExists:
			return toolErr("A label with that name already exists in %s.", proj)
		}
		return toolErr("%s", se.Message)
	case service.KindBadRequest:
		if se.Code == service.CodeInvalidCursor {
			return toolErr("%s", invalidCursorText)
		}
		return toolErr("%s", se.Message)
	case service.KindValidation:
		if len(se.Fields) > 0 {
			keys := make([]string, 0, len(se.Fields))
			for k := range se.Fields {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			parts := make([]string, len(keys))
			for i, k := range keys {
				parts[i] = k + ": " + se.Fields[k]
			}
			return toolErr("%s", strings.Join(parts, "; ")+".")
		}
		return toolErr("%s", se.Message)
	}
	c.h.Logger.Error("mcp tool failed", "tool", c.tool, "token", c.tokenID(), "err", err)
	return toolErr("Internal error. Try again; if it persists, check the server logs.")
}

func isCode(err error, code string) bool {
	var se *service.Error
	return errors.As(err, &se) && se.Code == code
}

func isNotFound(err error) bool { return errors.Is(err, service.ErrNotFound) }

// availableList renders "a, b, c ... and N more" with at most 20 entries of 80 characters.
func availableList(items []string) string {
	const max = 20
	n := len(items)
	if n > max {
		items = items[:max]
	}
	out := make([]string, len(items))
	for i, s := range items {
		out[i] = clip(s, 80)
	}
	s := strings.Join(out, ", ")
	if n > max {
		s += fmt.Sprintf(" ... and %d more", n-max)
	}
	return s
}
