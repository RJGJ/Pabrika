package mcpserver

import (
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/RJGJ/Pabrika/internal/service"
)

var ticketRefRe = regexp.MustCompile(`^([A-Za-z]{2,6})-([1-9][0-9]*)$`)

// resolveProject resolves a project key or id through the service (membership and token
// limit are enforced there).
func (c *call) resolveProject(ref string) (service.ProjectRef, *mcp.CallToolResult) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return service.ProjectRef{}, toolErr("project is required.")
	}
	pr, err := c.h.Services.Projects.Resolve(c.ctx, c.actor, ref)
	if err == nil {
		return pr, nil
	}
	if isNotFound(err) {
		return service.ProjectRef{}, c.projectNotFound(ref)
	}
	return service.ProjectRef{}, c.mapErr(err, errOpts{})
}

func (c *call) projectNotFound(ref string) *mcp.CallToolResult {
	if k := c.limitedKey(); k != "" {
		return c.notFound("")
	}
	list, err := c.h.Services.Projects.List(c.ctx, c.actor, true)
	if err != nil || len(list) == 0 {
		return toolErr("No project with key %s. No projects available to this token.", clip(ref, 80))
	}
	keys := make([]string, len(list))
	for i, p := range list {
		keys[i] = p.Key
	}
	return toolErr("No project with key %s. Available keys: %s.", clip(ref, 80), availableList(keys))
}

// resolveTicket resolves a reference (WEB-12) or id.
func (c *call) resolveTicket(ref string) (service.TicketRef, *mcp.CallToolResult) {
	return c.resolveTicketArg("ticket", ref)
}

func (c *call) resolveTicketArg(arg, ref string) (service.TicketRef, *mcp.CallToolResult) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return service.TicketRef{}, toolErr("%s is required.", arg)
	}
	isID := service.IsULID(ref)
	mm := ticketRefRe.FindStringSubmatch(ref)
	if !isID && mm == nil {
		return service.TicketRef{}, toolErr("Invalid %s %q. Use a reference like WEB-12 or a ticket id.", arg, clip(ref, 80))
	}
	tr, err := c.h.Services.Tickets.Resolve(c.ctx, c.actor, ref)
	if err == nil {
		return tr, nil
	}
	if !isNotFound(err) {
		return service.TicketRef{}, c.mapErr(err, errOpts{})
	}
	if isID {
		return service.TicketRef{}, c.notFound("No ticket with id " + ref + ".")
	}
	key := strings.ToUpper(mm[1])
	if pr, perr := c.h.Services.Projects.Resolve(c.ctx, c.actor, key); perr == nil {
		return service.TicketRef{}, toolErr("No ticket %s-%s in project %s. It may not exist or may already be deleted.", key, mm[2], pr.Key)
	}
	return service.TicketRef{}, c.projectNotFound(key)
}

// resolveComment resolves a comment id.
func (c *call) resolveComment(id string) (service.CommentRef, *mcp.CallToolResult) {
	id = strings.TrimSpace(id)
	if id == "" {
		return service.CommentRef{}, toolErr("comment is required.")
	}
	cr, err := c.h.Services.Comments.Resolve(c.ctx, c.actor, id)
	if err == nil {
		return cr, nil
	}
	if isNotFound(err) {
		return service.CommentRef{}, c.notFound("No comment with id " + clip(id, 80) + ". Comment ids come from get_ticket or add_comment.")
	}
	return service.CommentRef{}, c.mapErr(err, errOpts{})
}

// resolveEmail returns the user id of the member with this email (case-insensitive).
func (c *call) resolveEmail(projectKey, email string) (string, *mcp.CallToolResult) {
	members, err := c.h.Services.Members.List(c.ctx, c.actor, projectKey)
	if err != nil {
		return "", c.mapErr(err, errOpts{project: projectKey})
	}
	want := strings.ToLower(strings.TrimSpace(email))
	emails := make([]string, 0, len(members))
	for _, mb := range members {
		if strings.ToLower(mb.Email) == want {
			return mb.UserID, nil
		}
		emails = append(emails, mb.Email)
	}
	return "", toolErr("No member with email %s in project %s. Members: %s.", clip(email, 80), projectKey, availableList(emails))
}

// resolveLabels maps label names (case-insensitive, duplicates collapsed) to ids.
func (c *call) resolveLabels(projectKey string, names []string) ([]string, *mcp.CallToolResult) {
	labels, err := c.h.Services.Labels.List(c.ctx, c.actor, projectKey)
	if err != nil {
		return nil, c.mapErr(err, errOpts{project: projectKey})
	}
	byName := make(map[string]string, len(labels))
	all := make([]string, 0, len(labels))
	for _, l := range labels {
		byName[strings.ToLower(l.Name)] = l.ID
		all = append(all, l.Name)
	}
	ids := []string{}
	seen := map[string]bool{}
	var unknown []string
	for _, n := range names {
		key := strings.ToLower(strings.TrimSpace(n))
		id, ok := byName[key]
		switch {
		case !ok:
			unknown = append(unknown, `"`+clip(n, 80)+`"`)
		case !seen[id]:
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(unknown) > 0 {
		who := "label " + unknown[0]
		if len(unknown) > 1 {
			who = "labels " + strings.Join(unknown, ", ")
		}
		return nil, toolErr("No %s in project %s. Labels: %s. Create one with create_label.", who, projectKey, availableList(all))
	}
	return ids, nil
}
