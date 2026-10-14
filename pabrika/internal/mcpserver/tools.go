package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/RJGJ/Pabrika/internal/auth"
	"github.com/RJGJ/Pabrika/internal/service"
)

// call is the per-invocation context handed to a tool.
type call struct {
	ctx   context.Context
	h     *handler
	p     auth.Principal
	actor service.Actor
	tool  string
}

// toolDef is one tool: metadata, a schema built once at init, and its handler.
type toolDef struct {
	name, title, desc       string
	scope                   toolScope // lowest token scope that sees the tool
	destructive, idempotent bool
	schema                  map[string]any
	run                     func(c *call, args []byte) *mcp.CallToolResult
}

// toolScope is the lowest token scope that may see (and call) a tool.
type toolScope int

const (
	scopeRead    toolScope = iota // every token
	scopeComment                  // comment and write tokens
	scopeWrite                    // write tokens only
)

// mutates reports whether the tool changes data (anything above read scope).
func (d *toolDef) mutates() bool { return d.scope != scopeRead }

func (d *toolDef) mcpTool() *mcp.Tool {
	f := false
	ann := &mcp.ToolAnnotations{Title: d.title, ReadOnlyHint: !d.mutates(), OpenWorldHint: &f}
	if d.mutates() {
		ann.IdempotentHint = d.idempotent
		dh := d.destructive
		ann.DestructiveHint = &dh
	}
	return &mcp.Tool{Name: d.name, Title: d.title, Description: d.desc, InputSchema: d.schema, Annotations: ann}
}

// allTools is the full surface: 7 read tools, then 2 comment-scope tools and 9 write-scope tools (18).
var allTools = buildTools()

func buildTools() []*toolDef {
	var defs []*toolDef
	defs = append(defs, projectTools()...)
	defs = append(defs, ticketReadTools()...)
	defs = append(defs, decisionReadTools()...)
	defs = append(defs, writeProjectTools()...)
	defs = append(defs, ticketWriteTools()...)
	defs = append(defs, commentTools()...)
	return defs
}

const notIdempotent = " If a call times out, check with list_tickets / get_ticket before repeating it."

// decode is a helper for handlers: decode args into dst using the tool's schema property names.
func (d *toolDef) decode(args []byte, dst any) *mcp.CallToolResult {
	return decodeArgs(args, dst, argNames(d.schema))
}
