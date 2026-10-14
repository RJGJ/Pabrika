package httpapi

import (
	"sort"
	"testing"
)

// The set of routes a comment-scope token may call is exactly these two. Adding a route here is
// a security decision: it must also be flagged with routeComment and get matrix rows.
func TestCommentOKRoutesAreExactlyTwo(t *testing.T) {
	h := setupWithHub(t)
	var got []string
	for _, rt := range apiRoutes(h.Server) {
		if rt.CommentOK {
			got = append(got, rt.Pattern)
		}
	}
	sort.Strings(got)
	want := []string{"PATCH /api/v1/comments/{id}", "POST /api/v1/tickets/{id}/comments"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("CommentOK routes %v, want %v", got, want)
	}
}

func TestCommentTokenEndToEnd(t *testing.T) {
	w := tkSetup(t)
	w.mk(t, map[string]any{"title": "A"})
	r := w.h.Do(t, "POST", "/api/v1/tokens", map[string]any{"name": "reviewer", "scope": "comment"}, As(w.owner))
	if r.Code != 201 || r.JSON(t)["token"].(map[string]any)["scope"] != "comment" {
		t.Fatalf("create comment token: %d %s", r.Code, r.Body)
	}
	secret := r.JSON(t)["secret"].(string)
	tok := Bearer(secret)

	// reads work, and the role shown is capped at viewer
	if r := w.h.Do(t, "GET", "/api/v1/tickets/WEB-1", nil, tok); r.Code != 200 {
		t.Fatalf("get: %d %s", r.Code, r.Body)
	}
	pr := w.h.Do(t, "GET", "/api/v1/projects/WEB", nil, tok)
	if pr.Code != 200 || pr.JSON(t)["role"] != "viewer" {
		t.Fatalf("project: %d %s", pr.Code, pr.Body)
	}
	me := w.h.Do(t, "GET", "/api/v1/auth/me", nil, tok)
	if me.Code != 200 || me.JSON(t)["auth"].(map[string]any)["token"].(map[string]any)["scope"] != "comment" {
		t.Fatalf("me: %d %s", me.Code, me.Body)
	}
	// comment, then edit that comment
	c := w.h.Do(t, "POST", "/api/v1/tickets/WEB-1/comments", map[string]any{"body": "looks good"}, tok)
	if c.Code != 201 {
		t.Fatalf("comment: %d %s", c.Code, c.Body)
	}
	id := c.JSON(t)["id"].(string)
	if e := w.h.Do(t, "PATCH", "/api/v1/comments/"+id, map[string]any{"body": "looks great"}, tok); e.Code != 200 {
		t.Fatalf("edit own: %d %s", e.Code, e.Body)
	}
	// nothing else
	tkWant(t, w.h.Do(t, "PATCH", "/api/v1/tickets/WEB-1", map[string]any{"title": "x"}, tok), 403, "insufficient_scope")
	tkWant(t, w.h.Do(t, "POST", "/api/v1/tickets/WEB-1/move", map[string]any{"status": "done"}, tok), 403, "insufficient_scope")
	tkWant(t, w.h.Do(t, "PATCH", "/api/v1/tickets/WEB-1/labels", map[string]any{"remove": []string{"01ARZ3NDEKTSV4RRFFQ69G5FAV"}}, tok), 403, "insufficient_scope")
	tkWant(t, w.h.Do(t, "DELETE", "/api/v1/tickets/WEB-1", nil, tok), 403, "insufficient_scope")
	tkWant(t, w.h.Do(t, "DELETE", "/api/v1/comments/"+id, nil, tok), 403, "insufficient_scope")
	tkWant(t, w.h.Do(t, "POST", "/api/v1/projects/WEB/tickets", map[string]any{"title": "x"}, tok), 403, "insufficient_scope")
	tkWant(t, w.h.Do(t, "POST", "/api/v1/projects", map[string]any{"key": "NEW", "name": "n"}, tok), 403, "insufficient_scope")
	tkWant(t, w.h.Do(t, "POST", "/api/v1/tokens", map[string]any{"name": "x", "scope": "write"}, tok), 403, "session_required")
	// the comment really is the token's: another member's comment is not editable
	other := w.h.Do(t, "POST", "/api/v1/tickets/WEB-1/comments", map[string]any{"body": "human"}, As(w.owner)).JSON(t)["id"].(string)
	if r := w.h.Do(t, "PATCH", "/api/v1/comments/"+other, map[string]any{"body": "x"}, tok); r.Code != 403 {
		t.Fatalf("edit other: %d %s", r.Code, r.Body)
	}
	// scope validation on creation
	tkFieldErr(t, w.h.Do(t, "POST", "/api/v1/tokens", map[string]any{"name": "x", "scope": "admin"}, As(w.owner)), "scope")
}
