package httpapi

import (
	"time"

	"github.com/RJGJ/Pabrika/internal/auth"
	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/store"
)

// Response shapes of phase 2 spec section 6 and the mappers that build them. Handlers never
// build these by hand: they call a mapper, so the wire format lives in one place and the golden
// tests (view_test.go, literal expected key sets) guard it. Every listed key is always present
// (nullable ones are explicit pointers without omitempty) unless the comment says "omitted".

// ts formats a timestamp in the one wire format (RFC 3339 UTC, millisecond precision).
func ts(t time.Time) string { return store.FormatTime(t) }

func tsp(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := ts(*t)
	return &s
}

// effectiveRole caps a member role to viewer for a read-scope token. The service already
// returns the effective role; capping again is idempotent and keeps the view layer safe.
func effectiveRole(p auth.Principal, role service.Role) service.Role {
	if p.Method == auth.MethodToken && p.Token != nil && p.Token.Scope == service.ScopeRead && role != "" {
		return service.RoleViewer
	}
	return role
}

// ---- users ----

// userView is the `user` shape (also member.user).
type userView struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
}

// authUserView is the `auth_user` shape: signup, login, /auth/me, PATCH /auth/me.
type authUserView struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

// authUserEnvelope is the body of signup, login and PATCH /auth/me: {"user": auth_user}.
type authUserEnvelope struct {
	User authUserView `json:"user"`
}

func mapUser(id, email, displayName string) userView {
	return userView{ID: id, Email: email, DisplayName: displayName}
}

func mapAuthUser(u auth.AuthUser) authUserView {
	return authUserView{ID: u.ID, Email: u.Email, DisplayName: u.DisplayName, CreatedAt: ts(u.CreatedAt)}
}

func mapServiceUser(u service.User) authUserView {
	return authUserView{ID: u.ID, Email: u.Email, DisplayName: u.DisplayName, CreatedAt: ts(u.CreatedAt)}
}

// meView is the body of GET /auth/me.
type meView struct {
	User authUserView `json:"user"`
	Auth meAuthView   `json:"auth"`
}

type meAuthView struct {
	Method string       `json:"method"`
	Token  *meTokenView `json:"token,omitempty"` // omitted when method is session
}

type meTokenView struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Scope      string  `json:"scope"`
	ProjectID  *string `json:"project_id"`
	ProjectKey *string `json:"project_key"`
}

func mapMe(p auth.Principal) meView {
	v := meView{User: mapAuthUser(p.User), Auth: meAuthView{Method: p.Method}}
	if p.Method == auth.MethodToken && p.Token != nil {
		t := &meTokenView{ID: p.Token.ID, Name: p.Token.Name, Scope: string(p.Token.Scope)}
		if p.Token.ProjectID != "" {
			id, key := p.Token.ProjectID, p.Token.ProjectKey
			t.ProjectID, t.ProjectKey = &id, &key
		}
		v.Auth.Token = t
	}
	return v
}

// ---- projects ----

// projectView is the `project` shape (lists, create, PATCH).
type projectView struct {
	ID          string  `json:"id"`
	Key         string  `json:"key"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	ArchivedAt  *string `json:"archived_at"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
	Role        string  `json:"role"` // the caller's effective role
}

// countsView always has all four keys.
type countsView struct {
	Backlog    int `json:"backlog"`
	Todo       int `json:"todo"`
	InProgress int `json:"in_progress"`
	Done       int `json:"done"`
}

// projectDetailView is the `project_detail` shape (GET /projects/{id} only).
type projectDetailView struct {
	projectView
	Counts countsView `json:"counts"`
}

func mapProject(p auth.Principal, pr service.Project, role service.Role) projectView {
	return projectView{
		ID: pr.ID, Key: pr.Key, Name: pr.Name, Description: pr.Description,
		ArchivedAt: tsp(pr.ArchivedAt), CreatedAt: ts(pr.CreatedAt), UpdatedAt: ts(pr.UpdatedAt),
		Role: string(effectiveRole(p, role)),
	}
}

func mapProjectSummary(p auth.Principal, s service.ProjectSummary) projectView {
	return mapProject(p, s.Project, s.Role)
}

func mapProjectDetail(p auth.Principal, d service.ProjectDetail) projectDetailView {
	return projectDetailView{
		projectView: mapProject(p, d.Project, d.Role),
		Counts: countsView{
			Backlog:    d.TicketCounts[service.StatusBacklog],
			Todo:       d.TicketCounts[service.StatusTodo],
			InProgress: d.TicketCounts[service.StatusInProgress],
			Done:       d.TicketCounts[service.StatusDone],
		},
	}
}

// ---- members and labels ----

// memberView is the `member` shape.
type memberView struct {
	User      userView `json:"user"`
	Role      string   `json:"role"`
	CreatedAt string   `json:"created_at"`
}

func mapMember(m service.Member) memberView {
	return memberView{User: mapUser(m.UserID, m.Email, m.DisplayName), Role: string(m.Role), CreatedAt: ts(m.CreatedAt)}
}

func mapMembers(ms []service.Member) []memberView {
	out := make([]memberView, 0, len(ms))
	for _, m := range ms {
		out = append(out, mapMember(m))
	}
	return out
}

// labelView is the `label` shape.
type labelView struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
}

func mapLabel(l service.Label) labelView {
	return labelView{ID: l.ID, ProjectID: l.ProjectID, Name: l.Name, Color: string(l.Color)}
}

func mapLabels(ls []service.Label) []labelView {
	out := make([]labelView, 0, len(ls))
	for _, l := range ls {
		out = append(out, mapLabel(l))
	}
	return out
}

// ---- tickets ----

// assigneeView is a ticket's assignee object.
type assigneeView struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}

// ticketView is the `ticket` shape. Description is a pointer so the list item (nil, omitted)
// differs from the full shape (always present, even when empty). Use ticketListItem and
// ticketFull, never build it by hand.
type ticketView struct {
	ID           string        `json:"id"`
	Ref          string        `json:"ref"`
	ProjectID    string        `json:"project_id"`
	ProjectKey   string        `json:"project_key"`
	Number       int64         `json:"number"`
	Title        string        `json:"title"`
	Description  *string       `json:"description,omitempty"` // omitted on list items
	Status       string        `json:"status"`
	Priority     string        `json:"priority"`
	Assignee     *assigneeView `json:"assignee"`
	Labels       []labelView   `json:"labels"`
	Position     float64       `json:"position"`
	DueDate      *string       `json:"due_date"`
	CommentCount int           `json:"comment_count"`
	CreatedAt    string        `json:"created_at"`
	UpdatedAt    string        `json:"updated_at"`
}

func mapTicket(t service.Ticket, full bool) ticketView {
	v := ticketView{
		ID: t.ID, Ref: t.Ref, ProjectID: t.ProjectID, ProjectKey: t.ProjectKey, Number: t.Number,
		Title: t.Title, Status: string(t.Status), Priority: string(t.Priority),
		Labels: mapLabels(t.Labels), Position: t.Position, DueDate: t.DueDate,
		CommentCount: t.CommentCount, CreatedAt: ts(t.CreatedAt), UpdatedAt: ts(t.UpdatedAt),
	}
	if t.Assignee != nil {
		v.Assignee = &assigneeView{ID: t.Assignee.ID, DisplayName: t.Assignee.DisplayName, Email: t.Assignee.Email}
	}
	if full {
		d := t.Description
		v.Description = &d
	}
	return v
}

// ticketListItem is the list shape (no description).
func ticketListItem(t service.Ticket) ticketView { return mapTicket(t, false) }

// ticketFull is the shape of create, GET, PATCH and move (with description).
func ticketFull(t service.Ticket) ticketView { return mapTicket(t, true) }

func mapTicketList(items []service.Ticket) []ticketView {
	out := make([]ticketView, 0, len(items))
	for _, t := range items {
		out = append(out, ticketListItem(t))
	}
	return out
}

// moveView is the response of POST /tickets/{id}/move: the full ticket plus `renumbered`.
type moveView struct {
	ticketView
	Renumbered bool `json:"renumbered"`
}

func mapMove(r service.MoveResult) moveView {
	return moveView{ticketView: ticketFull(r.Ticket), Renumbered: r.Renumbered}
}

// ---- comments and activity ----

// commentAuthorView is the `comment_author` shape (also activity.actor).
type commentAuthorView struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Bot       bool   `json:"bot"`
	OwnerName string `json:"owner_name,omitempty"` // omitted when type is user
}

// The service names an author whose user or token row is gone with these literals.
const (
	svcDeletedUser  = "deleted user"
	svcDeletedToken = "deleted token"
)

func mapAuthor(a service.CommentAuthor) commentAuthorView {
	v := commentAuthorView{Type: string(a.Type), ID: a.ID, Name: a.Name, Bot: a.Type == service.ActorAPIToken}
	if a.Type == service.ActorAPIToken {
		v.OwnerName = a.OwnerName
	}
	// An author that cannot be resolved renders as "Unknown", never as a bot.
	if a.Name == "" || a.Name == svcDeletedUser || a.Name == svcDeletedToken {
		v.Name, v.Bot, v.OwnerName = "Unknown", false, ""
	}
	return v
}

// commentView is the `comment` shape.
type commentView struct {
	ID        string            `json:"id"`
	TicketID  string            `json:"ticket_id"`
	Author    commentAuthorView `json:"author"`
	Body      string            `json:"body"`
	CreatedAt string            `json:"created_at"`
	EditedAt  *string           `json:"edited_at"`
}

func mapComment(c service.Comment) commentView {
	return commentView{
		ID: c.ID, TicketID: c.TicketID, Author: mapAuthor(c.Author), Body: c.Body,
		CreatedAt: ts(c.CreatedAt), EditedAt: tsp(c.EditedAt),
	}
}

func mapComments(cs []service.Comment) []commentView {
	out := make([]commentView, 0, len(cs))
	for _, c := range cs {
		out = append(out, mapComment(c))
	}
	return out
}

// activityView is the `activity` shape. Changes is {field: [old, new]} with Phase 1's keys.
type activityView struct {
	ID        string            `json:"id"`
	TicketID  string            `json:"ticket_id"`
	Actor     commentAuthorView `json:"actor"`
	Action    string            `json:"action"`
	Changes   map[string][2]any `json:"changes"`
	CreatedAt string            `json:"created_at"`
}

func mapActivity(a service.Activity) activityView {
	ch := a.Changes
	if ch == nil {
		ch = map[string][2]any{}
	}
	return activityView{
		ID: a.ID, TicketID: a.TicketID, Actor: mapAuthor(a.Actor), Action: a.Action,
		Changes: ch, CreatedAt: ts(a.CreatedAt),
	}
}

func mapActivities(as []service.Activity) []activityView {
	out := make([]activityView, 0, len(as))
	for _, a := range as {
		out = append(out, mapActivity(a))
	}
	return out
}

// ---- tokens ----

// tokenView is the `token` shape. It never carries the secret or its hash.
type tokenView struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Prefix     string            `json:"token_prefix"`
	Scope      string            `json:"scope"`
	Project    *tokenProjectView `json:"project"`
	LastUsedAt *string           `json:"last_used_at"`
	RevokedAt  *string           `json:"revoked_at"`
	CreatedAt  string            `json:"created_at"`
}

type tokenProjectView struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

// createdTokenView is the response of POST /tokens: {"token": token, "secret": "pb_..."}.
type createdTokenView struct {
	Token  tokenView `json:"token"`
	Secret string    `json:"secret"`
}

func mapToken(t service.Token) tokenView {
	v := tokenView{
		ID: t.ID, Name: t.Name, Prefix: t.Prefix, Scope: string(t.Scope),
		LastUsedAt: tsp(t.LastUsedAt), RevokedAt: tsp(t.RevokedAt), CreatedAt: ts(t.CreatedAt),
	}
	if t.Project != nil {
		v.Project = &tokenProjectView{ID: t.Project.ID, Key: t.Project.Key}
	}
	return v
}

func mapTokens(items []service.Token) []tokenView {
	out := make([]tokenView, 0, len(items))
	for _, t := range items {
		out = append(out, mapToken(t))
	}
	return out
}

func mapCreatedToken(t service.Token, secret string) createdTokenView {
	return createdTokenView{Token: mapToken(t), Secret: secret}
}
