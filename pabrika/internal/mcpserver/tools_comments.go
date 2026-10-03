package mcpserver

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/RJGJ/Pabrika/internal/service"
)

func commentTools() []*toolDef {
	addSchema := objSchema(m{
		"ticket": ticketProp,
		"body":   strProp("Markdown comment body.", service.MaxCommentBody),
	}, "ticket", "body")
	editSchema := objSchema(m{
		"comment": strProp("Comment id, from get_ticket or add_comment.", 0),
		"body":    strProp("The new full markdown body.", service.MaxCommentBody),
	}, "comment", "body")
	return []*toolDef{
		{
			name: "add_comment", title: "Add comment", write: true, schema: addSchema,
			desc: "Add a markdown comment to a ticket. It is shown as written by this token's name. Returns the comment id. Not idempotent." + notIdempotent,
			run: func(c *call, args []byte) *mcp.CallToolResult {
				var in struct {
					Ticket string `json:"ticket"`
					Body   string `json:"body"`
				}
				if e := decodeArgs(args, &in, argNames(addSchema)); e != nil {
					return e
				}
				tr, e := c.resolveTicket(in.Ticket)
				if e != nil {
					return e
				}
				if e := firstErr(required("body", in.Body), tooLong("body", in.Body, service.MaxCommentBody)); e != nil {
					return e
				}
				cm, err := c.h.Services.Comments.Add(c.ctx, c.actor, tr.ID, in.Body)
				if err != nil {
					return c.mapErr(err, errOpts{project: tr.Key})
				}
				return ok(m{"id": cm.ID, "ticket": service.FormatRef(tr.Key, tr.Number), "author": cm.Author.Name, "created_at": ts(cm.CreatedAt)})
			},
		},
		{
			name: "update_comment", title: "Update comment", write: true, idempotent: true, schema: editSchema,
			desc: "Edit the body of a comment that you wrote earlier with add_comment. Pass the comment id (shown in get_ticket and in the add_comment result) and the new full body in markdown. " +
				"You can only edit comments written by this same token; comments by people or other tokens cannot be changed.",
			run: func(c *call, args []byte) *mcp.CallToolResult {
				var in struct {
					Comment string `json:"comment"`
					Body    string `json:"body"`
				}
				if e := decodeArgs(args, &in, argNames(editSchema)); e != nil {
					return e
				}
				cr, e := c.resolveComment(in.Comment)
				if e != nil {
					return e
				}
				if e := firstErr(required("body", in.Body), tooLong("body", in.Body, service.MaxCommentBody)); e != nil {
					return e
				}
				cm, err := c.h.Services.Comments.Edit(c.ctx, c.actor, cr.ID, in.Body)
				if err != nil {
					return c.mapErr(err, errOpts{project: cr.ProjectKey})
				}
				updated := cm.CreatedAt
				if cm.EditedAt != nil {
					updated = *cm.EditedAt
				}
				return ok(m{"id": cr.ID, "ticket": cr.TicketRef, "author": cm.Author.Name, "updated_at": ts(updated)})
			},
		},
	}
}
