package mcpserver

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/RJGJ/Pabrika/internal/service"
)

// ok builds a success result: structuredContent and one compact JSON text block, identical.
func ok(out any) *mcp.CallToolResult {
	b, err := json.Marshal(out)
	if err != nil {
		return toolErr("Internal error. Try again; if it persists, check the server logs.")
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(b)}},
		StructuredContent: json.RawMessage(b),
	}
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func tsPtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := ts(*t)
	return &s
}

func (h *handler) base() string { return strings.TrimRight(h.BaseURL, "/") }

func (h *handler) projectURL(key string) string { return h.base() + "/p/" + key }

func (h *handler) ticketURL(key string, n int64) string {
	return h.base() + "/p/" + key + "/t/" + strconv.FormatInt(n, 10)
}

type projectView struct {
	Key         string         `json:"key"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Archived    bool           `json:"archived"`
	Role        service.Role   `json:"role"`
	Counts      map[string]int `json:"counts"`
	URL         string         `json:"url"`
}

func (h *handler) projectView(p service.Project, role service.Role, counts map[service.Status]int) projectView {
	cs := make(map[string]int, len(service.Statuses))
	for _, s := range service.Statuses {
		cs[string(s)] = counts[s]
	}
	return projectView{Key: p.Key, Name: p.Name, Description: p.Description, Archived: p.ArchivedAt != nil,
		Role: role, Counts: cs, URL: h.projectURL(p.Key)}
}

type ticketSummary struct {
	Ref      string           `json:"ref"`
	Title    string           `json:"title"`
	Status   service.Status   `json:"status"`
	Priority service.Priority `json:"priority"`
	Position float64          `json:"position"`
	Assignee *string          `json:"assignee"`
	Labels   []string         `json:"labels"`
	DueDate  *string          `json:"due_date"`
	URL      string           `json:"url"`
}

func (h *handler) ticketSummary(t service.Ticket) ticketSummary {
	var assignee *string
	if t.Assignee != nil {
		e := t.Assignee.Email
		assignee = &e
	}
	labels := make([]string, len(t.Labels))
	for i, l := range t.Labels {
		labels[i] = l.Name
	}
	return ticketSummary{Ref: t.Ref, Title: t.Title, Status: t.Status, Priority: t.Priority, Position: t.Position,
		Assignee: assignee, Labels: labels, DueDate: t.DueDate, URL: h.ticketURL(t.ProjectKey, t.Number)}
}

type moveView struct {
	ticketSummary
	Renumbered bool `json:"renumbered,omitempty"`
}

type commentView struct {
	ID            string  `json:"id"`
	Author        string  `json:"author"`
	AuthorKind    string  `json:"author_kind"`
	Body          string  `json:"body"`
	BodyTruncated bool    `json:"body_truncated,omitempty"`
	CreatedAt     string  `json:"created_at"`
	EditedAt      *string `json:"edited_at"`
}

type ticketDetail struct {
	ticketSummary
	Project           string        `json:"project"`
	Description       string        `json:"description"`
	CreatedAt         string        `json:"created_at"`
	UpdatedAt         string        `json:"updated_at"`
	CommentCount      int           `json:"comment_count"`
	Comments          []commentView `json:"comments"`
	CommentsTruncated bool          `json:"comments_truncated"`
}

const (
	maxCommentChars = 8000
	maxTotalChars   = 40000
	commentsLimit   = 50
)

// commentViews applies the size budgets: each body cut at 8,000 characters, and older comments
// dropped once the returned bodies exceed 40,000 characters in total. Input is oldest-first.
func commentViews(cs []service.Comment) (out []commentView, dropped bool) {
	total := 0
	start := len(cs)
	views := make([]commentView, len(cs))
	for i, c := range cs {
		body, cut := c.Body, false
		if r := []rune(body); len(r) > maxCommentChars {
			body, cut = string(r[:maxCommentChars]), true
		}
		kind := string(c.Author.Type)
		views[i] = commentView{ID: c.ID, Author: c.Author.Name, AuthorKind: kind, Body: body, BodyTruncated: cut,
			CreatedAt: ts(c.CreatedAt), EditedAt: tsPtr(c.EditedAt)}
	}
	for i := len(cs) - 1; i >= 0; i-- {
		n := runeLen(views[i].Body)
		if total+n > maxTotalChars {
			dropped = true
			break
		}
		total += n
		start = i
	}
	return views[start:], dropped
}
