package mcpserver

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/RJGJ/Pabrika/internal/auth"
	"github.com/RJGJ/Pabrika/internal/service"
)

const (
	projectsURI   = "pabrika://projects"
	boardPrefix   = "pabrika://projects/"
	boardSuffix   = "/board"
	boardMaxItems = 500
)

// addResources registers pabrika://projects and the board template. Visibility follows the
// service (member projects, token project limit). Resource text is untrusted data.
func (h *handler) addResources(s *mcp.Server, p auth.Principal) {
	actor := p.Actor()
	s.AddResource(&mcp.Resource{URI: projectsURI, Name: "projects", MIMEType: "application/json",
		Description: "Projects this token can access, with role and ticket counts."},
		func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			list, err := h.Services.Projects.List(ctx, actor, false)
			if err != nil {
				return nil, err
			}
			return jsonResource(projectsURI, m{"items": h.projectViews(list)})
		})
	s.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: boardPrefix + "{key}" + boardSuffix, Name: "board", MIMEType: "application/json",
		Description: "All open tickets of a project grouped by column (max 500). " + untrustedNotice},
		func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			uri := req.Params.URI
			key := strings.TrimSuffix(strings.TrimPrefix(uri, boardPrefix), boardSuffix)
			if !strings.HasPrefix(uri, boardPrefix) || !strings.HasSuffix(uri, boardSuffix) || key == "" || strings.Contains(key, "/") {
				return nil, mcp.ResourceNotFoundError(uri)
			}
			pr, err := h.Services.Projects.Resolve(ctx, actor, key)
			if err != nil {
				return nil, mcp.ResourceNotFoundError(uri)
			}
			cols := map[string][]ticketSummary{}
			for _, st := range service.Statuses {
				cols[string(st)] = []ticketSummary{}
			}
			total, truncated, cursor := 0, false, ""
			for !truncated {
				page, err := h.Services.Tickets.List(ctx, actor, pr.Key, service.TicketFilter{Limit: 200, Cursor: cursor})
				if err != nil {
					return nil, err
				}
				for _, t := range page.Items {
					if total >= boardMaxItems {
						truncated = true
						break
					}
					cols[string(t.Status)] = append(cols[string(t.Status)], h.ticketSummary(t))
					total++
				}
				if page.NextCursor == "" {
					break
				}
				if total >= boardMaxItems {
					truncated = true
				}
				cursor = page.NextCursor
			}
			return jsonResource(uri, m{"project": pr.Key, "columns": cols, "truncated": truncated})
		})
}

func jsonResource(uri string, v any) (*mcp.ReadResourceResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(b)}}}, nil
}
