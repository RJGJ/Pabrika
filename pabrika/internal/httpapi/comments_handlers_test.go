package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/RJGJ/Pabrika/internal/service"
)

func cmPost(t *testing.T, w *tkWorld, ticket, body string, opts ...ReqOpt) map[string]any {
	t.Helper()
	r := w.h.Do(t, "POST", "/api/v1/tickets/"+ticket+"/comments", map[string]any{"body": body}, opts...)
	if r.Code != 201 {
		t.Fatalf("add comment: %d %s", r.Code, r.Body)
	}
	return r.JSON(t)
}

func TestComments_ShapesAndAuthors(t *testing.T) {
	w := tkSetup(t)
	w.mk(t, map[string]any{"title": "A"})

	c := cmPost(t, w, "WEB-1", "  hello  ", As(w.editor))
	wantKeys(t, "comment", c, "id", "ticket_id", "author", "body", "created_at", "edited_at")
	wantType(t, "comment", c, "edited_at", "null")
	author := c["author"].(map[string]any)
	wantKeys(t, "user author", author, "type", "id", "name", "bot")
	if c["body"] != "hello" || author["type"] != "user" || author["bot"] != false || author["name"] != "Editor" || author["id"] != w.editor.User.ID {
		t.Errorf("user comment: %v", c)
	}

	bc := cmPost(t, w, "WEB-1", "from bot", Bearer(w.ownerTok))
	ba := bc["author"].(map[string]any)
	wantKeys(t, "token author", ba, "type", "id", "name", "bot", "owner_name")
	if ba["type"] != "api_token" || ba["bot"] != true || ba["owner_name"] != "Owner" || ba["name"] == "" {
		t.Errorf("token comment author: %v", ba)
	}

	// ticket comment_count follows
	tk := w.h.Do(t, "GET", "/api/v1/tickets/WEB-1", nil, As(w.viewer)).JSON(t)
	if tk["comment_count"].(float64) != 2 {
		t.Errorf("comment_count %v", tk["comment_count"])
	}
	// ticket reachable by ULID too
	if r := w.h.Do(t, "POST", "/api/v1/tickets/"+tk["id"].(string)+"/comments", map[string]any{"body": "x"}, As(w.owner)); r.Code != 201 {
		t.Errorf("by ulid: %d", r.Code)
	}

	// edit sets edited_at
	r := w.h.Do(t, "PATCH", "/api/v1/comments/"+c["id"].(string), map[string]any{"body": "edited"}, As(w.editor))
	ej := r.JSON(t)
	if r.Code != 200 || ej["body"] != "edited" || ej["edited_at"] == nil {
		t.Errorf("edit: %d %s", r.Code, r.Body)
	}
	wantKeys(t, "edited", ej, "id", "ticket_id", "author", "body", "created_at", "edited_at")
}

func TestComments_AddValidation(t *testing.T) {
	w := tkSetup(t)
	w.mk(t, map[string]any{"title": "A"})
	path := "/api/v1/tickets/WEB-1/comments"
	tkFieldErr(t, w.h.Do(t, "POST", path, map[string]any{"body": "   "}, As(w.owner)), "body")
	tkFieldErr(t, w.h.Do(t, "POST", path, map[string]any{}, As(w.owner)), "body")
	tkFieldErr(t, w.h.Do(t, "POST", path, map[string]any{"body": strings.Repeat("a", 20001)}, As(w.owner)), "body")
	if r := w.h.Do(t, "POST", path, map[string]any{"body": strings.Repeat("a", 20000)}, As(w.owner)); r.Code != 201 {
		t.Errorf("20000 chars should pass: %d", r.Code)
	}
	tkWant(t, w.h.Do(t, "POST", path, map[string]any{"body": 5}, As(w.owner)), 400, "bad_request")
	tkWant(t, w.h.Do(t, "POST", path, map[string]any{"body": "a", "x": 1}, As(w.owner)), 400, "bad_request")
	tkWant(t, w.h.Do(t, "POST", path, nil, As(w.owner)), 400, "bad_request")
	tkWant(t, w.h.Do(t, "POST", path, "x", As(w.owner), RawBody("body=a", "text/plain")), 415, "unsupported_media_type")
	tkWant(t, w.h.Do(t, "POST", "/api/v1/tickets/WEB-9/comments", map[string]any{"body": "a"}, As(w.owner)), 404, "not_found")
}

func TestComments_ListPagination(t *testing.T) {
	w := tkSetup(t)
	w.mk(t, map[string]any{"title": "A"})
	for i := 1; i <= 5; i++ {
		cmPost(t, w, "WEB-1", fmt.Sprintf("c%d", i), As(w.editor))
		w.h.Env.Clock.Advance(1_000_000_000)
	}
	var bodies []string
	cursor := ""
	pages := 0
	for {
		r := w.h.Do(t, "GET", "/api/v1/tickets/WEB-1/comments?limit=2&cursor="+url.QueryEscape(cursor), nil, As(w.viewer))
		for _, it := range tkItems(t, r) {
			bodies = append(bodies, it["body"].(string))
		}
		pages++
		nc, _ := r.JSON(t)["next_cursor"].(string)
		if nc == "" || pages > 5 {
			break
		}
		cursor = nc
	}
	if strings.Join(bodies, ",") != "c1,c2,c3,c4,c5" || pages != 3 {
		t.Errorf("oldest first across pages: %v (%d pages)", bodies, pages)
	}
	r := w.h.Do(t, "GET", "/api/v1/tickets/WEB-1/comments", nil, As(w.viewer))
	wantKeys(t, "list", r.JSON(t), "items", "next_cursor")
	wantType(t, "list", r.JSON(t), "next_cursor", "null")
	tkFieldErr(t, w.h.Do(t, "GET", "/api/v1/tickets/WEB-1/comments?limit=0", nil, As(w.viewer)), "limit")
	tkFieldErr(t, w.h.Do(t, "GET", "/api/v1/tickets/WEB-1/comments?limit=x", nil, As(w.viewer)), "limit")
	tkWant(t, w.h.Do(t, "GET", "/api/v1/tickets/WEB-1/comments?cursor=%25%25x", nil, As(w.viewer)), 400, "invalid_cursor")
	if r := w.h.Do(t, "GET", "/api/v1/tickets/WEB-1/comments?limit=9999", nil, As(w.viewer)); len(tkItems(t, r)) != 5 {
		t.Errorf("clamped limit: %s", r.Body)
	}
}

func TestComments_EditRules(t *testing.T) {
	w := tkSetup(t)
	w.mk(t, map[string]any{"title": "A"})
	edit := func(id, body string, opts ...ReqOpt) *Resp {
		return w.h.Do(t, "PATCH", "/api/v1/comments/"+id, map[string]any{"body": body}, opts...)
	}
	byEditor := cmPost(t, w, "WEB-1", "mine", As(w.editor))["id"].(string)
	byToken := cmPost(t, w, "WEB-1", "bot", Bearer(w.ownerTok))["id"].(string)

	tkWant(t, edit(byEditor, "x", As(w.owner)), 403, "forbidden")     // owner is not the author
	tkWant(t, edit(byToken, "x", As(w.owner)), 403, "forbidden")      // user cannot edit a token's comment
	tkWant(t, edit(byToken, "x", Bearer(w.limOwn)), 403, "forbidden") // another token of the same owner
	tkWant(t, edit(byEditor, "x", As(w.viewer)), 403, "forbidden")
	tkWant(t, edit(byEditor, "x", As(w.outsider)), 404, "not_found")
	tkWant(t, edit(byEditor, "x"), 401, "unauthorized")
	if r := edit(byToken, "bot v2", Bearer(w.ownerTok)); r.Code != 200 || r.JSON(t)["body"] != "bot v2" {
		t.Errorf("token edits own: %d %s", r.Code, r.Body)
	}
	tkFieldErr(t, edit(byEditor, " ", As(w.editor)), "body")
	tkFieldErr(t, edit(byEditor, strings.Repeat("a", 20001), As(w.editor)), "body")
	tkWant(t, w.h.Do(t, "PATCH", "/api/v1/comments/"+byEditor, map[string]any{"body": "a", "x": 1}, As(w.editor)), 400, "bad_request")
	tkWant(t, edit("01HZZZZZZZZZZZZZZZZZZZZZZZ", "x", As(w.editor)), 404, "not_found")

	// a read-scope token cannot edit even its own (owner's read token wrote nothing, so use a write
	// comment by the owner and the owner's read token: different actor, scope check comes first)
	tkWant(t, edit(byToken, "x", Bearer(w.readTok)), 403, "insufficient_scope")

	// demoted author can no longer edit; removed member gets 404
	if _, err := w.h.Env.Svc.Members.SetRole(context.Background(), service.UserActor(w.owner.User.ID), "WEB", w.editor.User.ID, service.RoleViewer); err != nil {
		t.Fatal(err)
	}
	tkWant(t, edit(byEditor, "x", As(w.editor)), 403, "forbidden")
	tkWant(t, w.h.Do(t, "DELETE", "/api/v1/comments/"+byEditor, nil, As(w.editor)), 403, "forbidden")
	if err := w.h.Env.Svc.Members.Remove(context.Background(), service.UserActor(w.owner.User.ID), "WEB", w.editor.User.ID); err != nil {
		t.Fatal(err)
	}
	tkWant(t, edit(byEditor, "x", As(w.editor)), 404, "not_found")
}

func TestComments_DeleteRules(t *testing.T) {
	w := tkSetup(t)
	w.mk(t, map[string]any{"title": "A"})
	del := func(id string, opts ...ReqOpt) *Resp {
		return w.h.Do(t, "DELETE", "/api/v1/comments/"+id, nil, opts...)
	}
	mine := func() string { return cmPost(t, w, "WEB-1", "c", As(w.editor))["id"].(string) }

	id := mine()
	tkWant(t, del(id, As(w.viewer)), 403, "forbidden")
	tkWant(t, del(id, As(w.outsider)), 404, "not_found")
	tkWant(t, del(id), 401, "unauthorized")
	tkWant(t, del(id, Bearer(w.readTok)), 403, "insufficient_scope")
	// another editor is neither author nor owner
	other := w.h.Signup(t, "editor2@x.io")
	w.h.Env.AddMember(t, w.proj.ID, other.User.ID, service.RoleEditor)
	tkWant(t, del(id, As(other)), 403, "forbidden")
	// author deletes
	if r := del(id, As(w.editor)); r.Code != 204 || len(r.Body) != 0 {
		t.Errorf("author delete: %d %q", r.Code, r.Body)
	}
	tkWant(t, del(id, As(w.editor)), 404, "not_found")
	// owner deletes someone else's, and a token's
	if r := del(mine(), As(w.owner)); r.Code != 204 {
		t.Errorf("owner delete: %d", r.Code)
	}
	botID := cmPost(t, w, "WEB-1", "bot", Bearer(w.ownerTok))["id"].(string)
	if r := del(botID, As(w.owner)); r.Code != 204 {
		t.Errorf("owner deletes bot comment: %d", r.Code)
	}
	// a token deletes its own comment
	botID = cmPost(t, w, "WEB-1", "bot", Bearer(w.ownerTok))["id"].(string)
	if r := del(botID, Bearer(w.ownerTok)); r.Code != 204 {
		t.Errorf("token deletes own: %d", r.Code)
	}
	// deleted comments disappear from list and count
	if r := w.h.Do(t, "GET", "/api/v1/tickets/WEB-1/comments", nil, As(w.owner)); len(tkItems(t, r)) != 0 {
		t.Errorf("deleted comments listed: %s", r.Body)
	}
	if tk := w.h.Do(t, "GET", "/api/v1/tickets/WEB-1", nil, As(w.owner)).JSON(t); tk["comment_count"].(float64) != 0 {
		t.Errorf("comment_count %v", tk["comment_count"])
	}
	// editing a deleted comment is 404
	tkWant(t, w.h.Do(t, "PATCH", "/api/v1/comments/"+id, map[string]any{"body": "x"}, As(w.editor)), 404, "not_found")
}

func TestComments_Archived(t *testing.T) {
	w := tkSetup(t)
	w.mk(t, map[string]any{"title": "A"})
	id := cmPost(t, w, "WEB-1", "c", As(w.editor))["id"].(string)
	w.h.Env.Archive(t, w.proj.ID)
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/v1/tickets/WEB-1/comments", map[string]any{"body": "x"}},
		{"PATCH", "/api/v1/comments/" + id, map[string]any{"body": "x"}},
		{"DELETE", "/api/v1/comments/" + id, nil},
	} {
		tkWant(t, w.h.Do(t, c.method, c.path, c.body, As(w.editor)), 409, "project_archived")
	}
	if r := w.h.Do(t, "GET", "/api/v1/tickets/WEB-1/comments", nil, As(w.viewer)); r.Code != 200 || len(tkItems(t, r)) != 1 {
		t.Errorf("archived read: %d %s", r.Code, r.Body)
	}
}

func TestComments_Matrix(t *testing.T) {
	w := tkSetup(t)
	w.mk(t, map[string]any{"title": "A"})
	whos := []struct {
		name string
		opts func() []ReqOpt
	}{
		{"anonymous", func() []ReqOpt { return nil }},
		{"non-member", func() []ReqOpt { return []ReqOpt{As(w.outsider)} }},
		{"viewer", func() []ReqOpt { return []ReqOpt{As(w.viewer)} }},
		{"editor", func() []ReqOpt { return []ReqOpt{As(w.editor)} }},
		{"owner", func() []ReqOpt { return []ReqOpt{As(w.owner)} }},
		{"read token", func() []ReqOpt { return []ReqOpt{Bearer(w.readTok)} }},
		{"write token", func() []ReqOpt { return []ReqOpt{Bearer(w.ownerTok)} }},
		{"limited own", func() []ReqOpt { return []ReqOpt{Bearer(w.limOwn)} }},
		{"limited other", func() []ReqOpt { return []ReqOpt{Bearer(w.limOt)} }},
	}
	type outcome struct {
		code int
		err  string
	}
	// list/add: role-based. Edit/delete target a comment written by the editor, so the only
	// users who may edit are the editor; who may delete: the editor and the owner.
	table := map[string]map[string]outcome{
		"list":   {"anonymous": {401, "unauthorized"}, "non-member": {404, "not_found"}, "viewer": {200, ""}, "editor": {200, ""}, "owner": {200, ""}, "read token": {200, ""}, "write token": {200, ""}, "limited own": {200, ""}, "limited other": {404, "not_found"}},
		"add":    {"anonymous": {401, "unauthorized"}, "non-member": {404, "not_found"}, "viewer": {403, "forbidden"}, "editor": {201, ""}, "owner": {201, ""}, "read token": {403, "insufficient_scope"}, "write token": {201, ""}, "limited own": {201, ""}, "limited other": {404, "not_found"}},
		"edit":   {"anonymous": {401, "unauthorized"}, "non-member": {404, "not_found"}, "viewer": {403, "forbidden"}, "editor": {200, ""}, "owner": {403, "forbidden"}, "read token": {403, "insufficient_scope"}, "write token": {403, "forbidden"}, "limited own": {403, "forbidden"}, "limited other": {404, "not_found"}},
		"delete": {"anonymous": {401, "unauthorized"}, "non-member": {404, "not_found"}, "viewer": {403, "forbidden"}, "editor": {204, ""}, "owner": {204, ""}, "read token": {403, "insufficient_scope"}, "write token": {204, ""}, "limited own": {204, ""}, "limited other": {404, "not_found"}},
	}
	for _, wh := range whos {
		for op, want := range table {
			t.Run(wh.name+"/"+op, func(t *testing.T) {
				id := cmPost(t, w, "WEB-1", "target", As(w.editor))["id"].(string)
				var r *Resp
				switch op {
				case "list":
					r = w.h.Do(t, "GET", "/api/v1/tickets/WEB-1/comments", nil, wh.opts()...)
				case "add":
					r = w.h.Do(t, "POST", "/api/v1/tickets/WEB-1/comments", map[string]any{"body": "n"}, wh.opts()...)
				case "edit":
					r = w.h.Do(t, "PATCH", "/api/v1/comments/"+id, map[string]any{"body": "n"}, wh.opts()...)
				case "delete":
					r = w.h.Do(t, "DELETE", "/api/v1/comments/"+id, nil, wh.opts()...)
				}
				o := want[wh.name]
				if r.Code != o.code || r.ErrCode(t) != o.err {
					t.Fatalf("got %d %q (%s), want %d %q", r.Code, r.ErrCode(t), r.Body, o.code, o.err)
				}
			})
		}
	}
}

func TestComments_NonMemberLooksLikeMissing(t *testing.T) {
	w := tkSetup(t)
	w.mk(t, map[string]any{"title": "A"})
	id := cmPost(t, w, "WEB-1", "c", As(w.owner))["id"].(string)
	ghost := "01HZZZZZZZZZZZZZZZZZZZZZZZ"
	for _, c := range []struct {
		method, real, ghost string
		body                any
	}{
		{"GET", "/api/v1/tickets/WEB-1/comments", "/api/v1/tickets/" + ghost + "/comments", nil},
		{"POST", "/api/v1/tickets/WEB-1/comments", "/api/v1/tickets/" + ghost + "/comments", map[string]any{"body": "x"}},
		{"PATCH", "/api/v1/comments/" + id, "/api/v1/comments/" + ghost, map[string]any{"body": "x"}},
		{"DELETE", "/api/v1/comments/" + id, "/api/v1/comments/" + ghost, nil},
	} {
		a := w.h.Do(t, c.method, c.real, c.body, As(w.outsider))
		b := w.h.Do(t, c.method, c.ghost, c.body, As(w.outsider))
		if a.Code != 404 || b.Code != 404 || !bytes.Equal(a.Body, b.Body) {
			t.Errorf("%s %s: %d %s vs %d %s", c.method, c.real, a.Code, a.Body, b.Code, b.Body)
		}
		// the limited-elsewhere token is indistinguishable too
		if tk := w.h.Do(t, c.method, c.real, c.body, Bearer(w.limOt)); tk.Code != 404 || !bytes.Equal(tk.Body, b.Body) {
			t.Errorf("%s %s limited token: %d %s", c.method, c.real, tk.Code, tk.Body)
		}
	}
}
