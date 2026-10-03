package mcpserver

import (
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/RJGJ/Pabrika/internal/service"
)

func projectTools() []*toolDef {
	listProjectsSchema := objSchema(m{"include_archived": boolProp("Include archived projects (default false).")})
	membersSchema := objSchema(m{"project": projectProp}, "project")
	labelsSchema := objSchema(m{"project": projectProp}, "project")
	return []*toolDef{
		{
			name: "list_projects", title: "List projects", schema: listProjectsSchema,
			desc: "List the projects this token can access, with your role and ticket counts per status. Call this first to learn the project keys.",
			run: func(c *call, args []byte) *mcp.CallToolResult {
				var in struct {
					IncludeArchived bool `json:"include_archived"`
				}
				if e := decodeArgs(args, &in, argNames(listProjectsSchema)); e != nil {
					return e
				}
				list, err := c.h.Services.Projects.List(c.ctx, c.actor, in.IncludeArchived)
				if err != nil {
					return c.mapErr(err, errOpts{})
				}
				return ok(m{"items": c.h.projectViews(list)})
			},
		},
		{
			name: "list_members", title: "List project members", schema: membersSchema,
			desc: "List the members of a project with their email, display name and role. Use these emails for the assignee field.",
			run: func(c *call, args []byte) *mcp.CallToolResult {
				var in struct {
					Project string `json:"project"`
				}
				if e := decodeArgs(args, &in, argNames(membersSchema)); e != nil {
					return e
				}
				pr, e := c.resolveProject(in.Project)
				if e != nil {
					return e
				}
				ms, err := c.h.Services.Members.List(c.ctx, c.actor, pr.Key)
				if err != nil {
					return c.mapErr(err, errOpts{project: pr.Key})
				}
				items := make([]m, len(ms))
				for i, mb := range ms {
					items[i] = m{"email": mb.Email, "display_name": mb.DisplayName, "role": mb.Role}
				}
				return ok(m{"items": items})
			},
		},
		{
			name: "list_labels", title: "List project labels", schema: labelsSchema,
			desc: "List the labels of a project. Use these names in the labels field of tickets.",
			run: func(c *call, args []byte) *mcp.CallToolResult {
				var in struct {
					Project string `json:"project"`
				}
				if e := decodeArgs(args, &in, argNames(labelsSchema)); e != nil {
					return e
				}
				pr, e := c.resolveProject(in.Project)
				if e != nil {
					return e
				}
				ls, err := c.h.Services.Labels.List(c.ctx, c.actor, pr.Key)
				if err != nil {
					return c.mapErr(err, errOpts{project: pr.Key})
				}
				items := make([]m, len(ls))
				for i, l := range ls {
					items[i] = m{"name": l.Name, "color": l.Color}
				}
				return ok(m{"items": items})
			},
		},
	}
}

func (h *handler) projectViews(list []service.ProjectSummary) []projectView {
	items := make([]projectView, len(list))
	for i, p := range list {
		items[i] = h.projectView(p.Project, p.Role, p.TicketCounts)
	}
	return items
}

func writeProjectTools() []*toolDef {
	createSchema := objSchema(m{
		"name":        strProp("Project name.", service.MaxProjectName),
		"key":         m{"type": "string", "description": "2-6 letters, uppercased; used in ticket references.", "pattern": "^[A-Za-z]{2,6}$"},
		"description": strProp("Project description.", service.MaxProjectDescription),
	}, "name", "key")
	updateSchema := objSchema(m{
		"project":     projectProp,
		"name":        strProp("New name.", service.MaxProjectName),
		"description": strProp("New description.", service.MaxProjectDescription),
		"archived":    boolProp("true archives the project, false unarchives it."),
	}, "project")
	labelSchema := objSchema(m{
		"project": projectProp,
		"name":    strProp("Label name, unique per project (case-insensitive).", service.MaxLabelName),
		"color":   enumProp("Label color (default gray).", colorNames()),
	}, "project", "name")
	return []*toolDef{
		{
			name: "create_project", title: "Create project", write: true, schema: createSchema,
			desc: "Create a project. You become its owner. The key is 2-6 uppercase letters and is used in ticket references like WEB-12; it cannot be changed later. Not idempotent.",
			run: func(c *call, args []byte) *mcp.CallToolResult {
				var in struct {
					Name        string `json:"name"`
					Key         string `json:"key"`
					Description string `json:"description"`
				}
				if e := decodeArgs(args, &in, argNames(createSchema)); e != nil {
					return e
				}
				if k := c.limitedKey(); k != "" {
					return toolErr("This token is limited to project %s and cannot create projects.", k)
				}
				key := service.NormalizeKey(in.Key)
				if e := firstErr(required("name", in.Name), tooLong("name", in.Name, service.MaxProjectName),
					tooLong("description", in.Description, service.MaxProjectDescription)); e != nil {
					return e
				}
				if !isKey(key) {
					return toolErr("Invalid key %q. Use 2-6 letters.", clip(in.Key, 80))
				}
				p, err := c.h.Services.Projects.Create(c.ctx, c.actor, service.CreateProjectInput{Key: key, Name: in.Name, Description: in.Description})
				if err != nil {
					if isCode(err, service.CodeKeyTaken) {
						return toolErr("Key %s is already taken. Choose another key.", key)
					}
					return c.mapErr(err, errOpts{})
				}
				return ok(c.h.projectView(p, service.RoleOwner, nil))
			},
		},
		{
			name: "update_project", title: "Update project", write: true, idempotent: true, schema: updateSchema,
			desc: "Rename a project, edit its description, or archive/unarchive it. Projects cannot be deleted over MCP.",
			run: func(c *call, args []byte) *mcp.CallToolResult {
				var in struct {
					Project     string                   `json:"project"`
					Name        service.Optional[string] `json:"name"`
					Description service.Optional[string] `json:"description"`
					Archived    service.Optional[bool]   `json:"archived"`
				}
				if e := decodeArgs(args, &in, argNames(updateSchema)); e != nil {
					return e
				}
				pr, e := c.resolveProject(in.Project)
				if e != nil {
					return e
				}
				if !in.Name.Set && !in.Description.Set && !in.Archived.Set {
					return toolErr("Pass at least one of name, description, archived.")
				}
				if in.Name.Null || in.Description.Null || in.Archived.Null {
					return toolErr("name, description and archived must not be null.")
				}
				if e := firstErr(tooLong("name", in.Name.Value, service.MaxProjectName),
					tooLong("description", in.Description.Value, service.MaxProjectDescription)); e != nil {
					return e
				}
				_, err := c.h.Services.Projects.Update(c.ctx, c.actor, pr.Key, service.UpdateProjectInput{
					Name: in.Name, Description: in.Description, Archived: in.Archived})
				if err != nil {
					return c.mapErr(err, errOpts{project: pr.Key, ownerOnly: true})
				}
				d, err := c.h.Services.Projects.Get(c.ctx, c.actor, pr.Key)
				if err != nil {
					return c.mapErr(err, errOpts{project: pr.Key})
				}
				return ok(c.h.projectView(d.Project, d.Role, d.TicketCounts))
			},
		},
		{
			name: "create_label", title: "Create label", write: true, schema: labelSchema,
			desc: "Create a label in a project. Label names are unique per project, case-insensitive." + notIdempotent,
			run: func(c *call, args []byte) *mcp.CallToolResult {
				var in struct {
					Project string `json:"project"`
					Name    string `json:"name"`
					Color   string `json:"color"`
				}
				if e := decodeArgs(args, &in, argNames(labelSchema)); e != nil {
					return e
				}
				pr, e := c.resolveProject(in.Project)
				if e != nil {
					return e
				}
				color := service.LabelColor(strings.ToLower(strings.TrimSpace(in.Color)))
				if color == "" {
					color = "gray"
				}
				if !color.Valid() {
					return toolErr("Invalid color %q. Valid colors: %s.", clip(in.Color, 80), strings.Join(colorNames(), ", "))
				}
				if e := firstErr(required("name", in.Name), tooLong("name", in.Name, service.MaxLabelName)); e != nil {
					return e
				}
				l, err := c.h.Services.Labels.Create(c.ctx, c.actor, pr.Key, service.LabelInput{Name: in.Name, Color: color})
				if err != nil {
					if isCode(err, service.CodeLabelExists) {
						return toolErr("Label %q already exists in %s.", clip(in.Name, 80), pr.Key)
					}
					return c.mapErr(err, errOpts{project: pr.Key})
				}
				return ok(m{"name": l.Name, "color": l.Color})
			},
		},
	}
}

func isKey(k string) bool {
	if len(k) < 2 || len(k) > 6 {
		return false
	}
	for _, r := range k {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}
