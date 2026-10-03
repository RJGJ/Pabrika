package service_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/store/db"
)

func (e *tkEnv) addComment(t *testing.T, a service.Actor, ref, body string) service.Comment {
	t.Helper()
	c, err := e.Svc.Comments.Add(ctx, a, ref, body)
	if err != nil {
		t.Fatalf("add comment: %v", err)
	}
	return c
}

func TestCommentSvcAddListLatest(t *testing.T) {
	e := newTk(t)
	tk := e.create(t, "t", "")
	tokenID := e.NewToken(t, e.M.Users["owner"].ID, service.ScopeWrite, "")
	agent := e.TokenActor(tokenID)

	e.Pub.Reset()
	c1 := e.addComment(t, e.Owner, tk.Ref, "  first  ")
	if c1.Body != "first" || c1.TicketRef != "WEB-1" || c1.TicketID != tk.ID || c1.EditedAt != nil {
		t.Fatalf("%+v", c1)
	}
	if c1.Author.Type != service.ActorUser || c1.Author.ID != e.Owner.ID || c1.Author.Name != "Owner WEB" || c1.Author.OwnerName != "" {
		t.Fatalf("author %+v", c1.Author)
	}
	c2 := e.addComment(t, agent, tk.Ref, "second")
	if c2.Author.Type != service.ActorAPIToken || c2.Author.ID != tokenID || !strings.HasPrefix(c2.Author.Name, "token ") || c2.Author.OwnerName != "Owner WEB" {
		t.Fatalf("token author %+v", c2.Author)
	}
	ev := e.Pub.Events()
	if len(ev) != 2 || ev[0].Type != service.EventCommentAdded || ev[0].CommentID != c1.ID || ev[0].TicketID != tk.ID || ev[1].Actor.Type != service.ActorAPIToken || ev[1].Actor.ID != tokenID {
		t.Fatalf("events %+v", ev)
	}

	// created in the same millisecond: monotonic ids keep creation order
	var ids []string
	ids = append(ids, c1.ID, c2.ID)
	for i := 3; i <= 7; i++ {
		ids = append(ids, e.addComment(t, e.Editor, tk.Ref, fmt.Sprintf("c%d", i)).ID)
	}
	page, err := e.Svc.Comments.List(ctx, e.Viewer, tk.Ref, 0, "")
	if err != nil || len(page.Items) != 7 || page.NextCursor != "" {
		t.Fatalf("%+v %v", page, err)
	}
	for i, c := range page.Items {
		if c.ID != ids[i] {
			t.Fatalf("order differs at %d", i)
		}
	}

	// cursor pagination
	var got []string
	cur := ""
	for {
		p, err := e.Svc.Comments.List(ctx, e.Viewer, tk.Ref, 3, cur)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range p.Items {
			got = append(got, c.ID)
		}
		if p.NextCursor == "" {
			break
		}
		cur = p.NextCursor
	}
	if !eqStrings(got, ids) {
		t.Fatalf("paged %v want %v", got, ids)
	}
	_, err = e.Svc.Comments.List(ctx, e.Viewer, tk.Ref, 3, "garbage")
	wantSvcErr(t, err, service.KindBadRequest, service.CodeInvalidCursor)

	// Latest: newest n returned oldest-first, truncation flag
	latest, trunc, err := e.Svc.Comments.Latest(ctx, e.Viewer, tk.Ref, 3)
	if err != nil || !trunc || len(latest) != 3 || latest[0].ID != ids[4] || latest[2].ID != ids[6] {
		t.Fatalf("%+v %v %v", latest, trunc, err)
	}
	latest, trunc, err = e.Svc.Comments.Latest(ctx, e.Viewer, tk.Ref, 7)
	if err != nil || trunc || len(latest) != 7 || latest[0].ID != ids[0] {
		t.Fatalf("%d %v %v", len(latest), trunc, err)
	}
	latest, _, _ = e.Svc.Comments.Latest(ctx, e.Viewer, tk.Ref, 0) // clamped to 1
	if len(latest) != 1 || latest[0].ID != ids[6] {
		t.Fatalf("clamp low: %+v", latest)
	}
	latest, trunc, _ = e.Svc.Comments.Latest(ctx, e.Viewer, tk.Ref, 9999)
	if len(latest) != 7 || trunc {
		t.Fatal("clamp high")
	}

	// the comment count is on every returned ticket
	g, _ := e.Svc.Tickets.Get(ctx, e.Viewer, tk.Ref)
	if g.CommentCount != 7 {
		t.Fatalf("count %d", g.CommentCount)
	}
	l, _ := e.Svc.Tickets.List(ctx, e.Viewer, "WEB", service.TicketFilter{})
	if l.Items[0].CommentCount != 7 {
		t.Fatalf("list count %d", l.Items[0].CommentCount)
	}
	u, _ := e.Svc.Tickets.Update(ctx, e.Owner, tk.Ref, service.UpdateTicketInput{Title: service.Some("renamed")})
	if u.CommentCount != 7 {
		t.Fatalf("update count %d", u.CommentCount)
	}
}

func TestCommentSvcBodyValidation(t *testing.T) {
	e := newTk(t)
	tk := e.create(t, "t", "")
	for name, body := range map[string]string{"empty": "", "blank": " \n\t ", "long": strings.Repeat("a", 20001)} {
		_, err := e.Svc.Comments.Add(ctx, e.Owner, tk.Ref, body)
		if se := wantSvcErr(t, err, service.KindValidation, ""); se.Fields["body"] == "" {
			t.Fatalf("%s: %v", name, se.Fields)
		}
	}
	c := e.addComment(t, e.Owner, tk.Ref, strings.Repeat("é", 20000))
	_, err := e.Svc.Comments.Edit(ctx, e.Owner, c.ID, "")
	wantSvcErr(t, err, service.KindValidation, "")
	if _, err := e.Svc.Comments.Edit(ctx, e.Owner, c.ID, strings.Repeat("é", 20001)); err == nil {
		t.Fatal("over-long edit accepted")
	}
}

func TestCommentSvcEdit(t *testing.T) {
	e := newTk(t)
	tk := e.create(t, "t", "")
	c := e.addComment(t, e.Editor, tk.Ref, "hello")
	e.Pub.Reset()

	// identical body (after trimming) is a no-op: no edited_at, no event
	e.Clock.Advance(time.Minute)
	same, err := e.Svc.Comments.Edit(ctx, e.Editor, c.ID, "  hello ")
	if err != nil || same.EditedAt != nil || len(e.Pub.Events()) != 0 {
		t.Fatalf("%+v %v %v", same, err, e.Pub.Events())
	}
	edited, err := e.Svc.Comments.Edit(ctx, e.Editor, c.ID, "hello world")
	if err != nil || edited.Body != "hello world" || edited.EditedAt == nil || !edited.EditedAt.Equal(e.Clock.Now()) || edited.TicketRef != "WEB-1" {
		t.Fatalf("%+v %v", edited, err)
	}
	ev := e.Pub.Events()
	if len(ev) != 1 || ev[0].Type != service.EventCommentChanged || ev[0].CommentID != c.ID || ev[0].TicketID != tk.ID {
		t.Fatalf("events %+v", ev)
	}
	// comment writes do not create activity rows
	acts, _ := e.Svc.Activity.List(ctx, e.Owner, tk.Ref, 0, "")
	if len(acts.Items) != 1 || acts.Items[0].Action != "created" {
		t.Fatalf("activity %+v", acts.Items)
	}
}

func TestCommentSvcEditPermissions(t *testing.T) {
	e := newTk(t)
	tk := e.create(t, "t", "")
	owner, editor := e.M.Users["owner"], e.M.Users["editor"]
	editorTok := e.TokenActor(e.NewToken(t, editor.ID, service.ScopeWrite, ""))
	otherTok := e.TokenActor(e.NewToken(t, editor.ID, service.ScopeWrite, ""))
	readTok := e.TokenActor(e.NewToken(t, editor.ID, service.ScopeRead, ""))
	ownerTok := e.TokenActor(e.NewToken(t, owner.ID, service.ScopeWrite, ""))
	outsider := e.UserActor(e.M.Users["non-member"])

	byEditor := e.addComment(t, e.Editor, tk.Ref, "by editor")
	byAgent := e.addComment(t, editorTok, tk.Ref, "by agent")
	byOwner := e.addComment(t, e.Owner, tk.Ref, "by owner")

	edit := func(a service.Actor, id string) error {
		_, err := e.Svc.Comments.Edit(ctx, a, id, "edited "+id[len(id)-4:])
		return err
	}
	if err := edit(e.Editor, byEditor.ID); err != nil {
		t.Fatal(err)
	}
	if err := edit(editorTok, byAgent.ID); err != nil {
		t.Fatalf("token edits its own comment: %v", err)
	}
	notAuthor := func(err error) { t.Helper(); wantSvcErr(t, err, service.KindForbidden, service.CodeNotAuthor) }
	notAuthor(edit(e.Owner, byEditor.ID)) // an owner cannot edit another's comment
	notAuthor(edit(e.Editor, byOwner.ID))
	notAuthor(edit(e.Editor, byAgent.ID))   // a human cannot edit a token's comment, even its own token
	notAuthor(edit(editorTok, byEditor.ID)) // and vice versa
	notAuthor(edit(otherTok, byAgent.ID))   // only the very same token
	notAuthor(edit(ownerTok, byOwner.ID))   // same owner user but a different actor

	wantSvcErr(t, edit(readTok, byEditor.ID), service.KindForbidden, service.CodeInsufficientScope)
	wantSvcErr(t, edit(outsider, byEditor.ID), service.KindNotFound, "")
	wantSvcErr(t, edit(e.Viewer, byEditor.ID), service.KindForbidden, service.CodeForbidden)
	wantSvcErr(t, edit(e.Editor, "01ARZ3NDEKTSV4RRFFQ69G5FAV"), service.KindNotFound, "")
	wantSvcErr(t, edit(e.Editor, "nope"), service.KindNotFound, "")

	// demoted author (now a viewer): forbidden, role is checked before authorship
	e.demote(t, e.P.ID, editor.ID, service.RoleViewer)
	wantSvcErr(t, edit(e.Editor, byEditor.ID), service.KindForbidden, service.CodeForbidden)
	wantSvcErr(t, edit(editorTok, byAgent.ID), service.KindForbidden, service.CodeForbidden)
	wantSvcErr(t, e.Svc.Comments.Delete(ctx, e.Editor, byEditor.ID), service.KindForbidden, service.CodeForbidden)
	e.demote(t, e.P.ID, editor.ID, service.RoleEditor)

	// archived project
	e.Archive(t, e.P.ID)
	wantSvcErr(t, edit(e.Editor, byEditor.ID), service.KindConflict, service.CodeProjectArchived)
	wantSvcErr(t, e.Svc.Comments.Delete(ctx, e.Editor, byEditor.ID), service.KindConflict, service.CodeProjectArchived)
	wantSvcErr(t, e.Svc.Comments.Delete(ctx, e.Owner, byEditor.ID), service.KindConflict, service.CodeProjectArchived)
	_, err := e.Svc.Comments.Add(ctx, e.Editor, tk.Ref, "x")
	wantSvcErr(t, err, service.KindConflict, service.CodeProjectArchived)
	if _, err := e.Svc.Comments.List(ctx, e.Viewer, tk.Ref, 0, ""); err != nil {
		t.Fatal(err)
	}
}

func (e *tkEnv) demote(t *testing.T, projectID, userID string, role service.Role) {
	t.Helper()
	err := e.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.CommentSeedSetRole(ctx, db.CommentSeedSetRoleParams{Role: string(role), ProjectID: projectID, UserID: userID})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCommentSvcDelete(t *testing.T) {
	e := newTk(t)
	tk := e.create(t, "t", "")
	editor := e.M.Users["editor"]
	editorTok := e.TokenActor(e.NewToken(t, editor.ID, service.ScopeWrite, ""))
	readTok := e.TokenActor(e.NewToken(t, e.M.Users["owner"].ID, service.ScopeRead, ""))

	mine := e.addComment(t, e.Editor, tk.Ref, "mine")
	agents := e.addComment(t, editorTok, tk.Ref, "agent says")
	theirs := e.addComment(t, e.Owner, tk.Ref, "owner's")
	third := e.addComment(t, e.Editor, tk.Ref, "third")

	wantSvcErr(t, e.Svc.Comments.Delete(ctx, e.Editor, theirs.ID), service.KindForbidden, service.CodeNotAuthor)
	wantSvcErr(t, e.Svc.Comments.Delete(ctx, e.Viewer, mine.ID), service.KindForbidden, service.CodeForbidden)
	wantSvcErr(t, e.Svc.Comments.Delete(ctx, readTok, mine.ID), service.KindForbidden, service.CodeInsufficientScope)

	e.Pub.Reset()
	if err := e.Svc.Comments.Delete(ctx, e.Editor, mine.ID); err != nil { // the author
		t.Fatal(err)
	}
	if err := e.Svc.Comments.Delete(ctx, e.Owner, agents.ID); err != nil { // an owner deletes an agent's comment
		t.Fatal(err)
	}
	ev := e.Pub.Events()
	if len(ev) != 2 || ev[0].Type != service.EventCommentChanged || ev[0].CommentID != mine.ID || ev[1].CommentID != agents.ID {
		t.Fatalf("events %+v", ev)
	}
	// deleted comments are gone for edit, delete, resolve and list; the row is retained
	wantSvcErr(t, e.Svc.Comments.Delete(ctx, e.Editor, mine.ID), service.KindNotFound, "")
	_, err := e.Svc.Comments.Edit(ctx, e.Editor, mine.ID, "zombie")
	wantSvcErr(t, err, service.KindNotFound, "")
	_, err = e.Svc.Comments.Resolve(ctx, e.Owner, mine.ID)
	wantSvcErr(t, err, service.KindNotFound, "")
	page, _ := e.Svc.Comments.List(ctx, e.Owner, tk.Ref, 0, "")
	if len(page.Items) != 2 || page.Items[0].ID != theirs.ID || page.Items[1].ID != third.ID {
		t.Fatalf("list %+v", page.Items)
	}
	g, _ := e.Svc.Tickets.Get(ctx, e.Owner, tk.Ref)
	if g.CommentCount != 2 {
		t.Fatalf("count %d", g.CommentCount)
	}
	raw, err := e.Store.Read().CommentSeedGetRaw(ctx, mine.ID)
	if err != nil || raw.DeletedAt == nil {
		t.Fatalf("row not retained: %+v %v", raw, err)
	}

	// comments of a soft-deleted ticket: edit and delete are not found
	if err := e.Svc.Tickets.Delete(ctx, e.Owner, tk.Ref); err != nil {
		t.Fatal(err)
	}
	_, err = e.Svc.Comments.Edit(ctx, e.Editor, third.ID, "x")
	wantSvcErr(t, err, service.KindNotFound, "")
	wantSvcErr(t, e.Svc.Comments.Delete(ctx, e.Owner, third.ID), service.KindNotFound, "")
}

func TestCommentSvcDeletedAuthorNames(t *testing.T) {
	e := newTk(t)
	tk := e.create(t, "t", "")
	author := e.NewUser(t, "gone@example.com", "Gone Person")
	e.AddMember(t, e.P.ID, author.ID, service.RoleEditor)
	tokOwner := e.NewUser(t, "gone2@example.com", "Token Owner")
	e.AddMember(t, e.P.ID, tokOwner.ID, service.RoleEditor)
	tokenID := e.NewToken(t, tokOwner.ID, service.ScopeWrite, "")
	e.addComment(t, e.UserActor(author), tk.Ref, "from a user")
	e.addComment(t, e.TokenActor(tokenID), tk.Ref, "from a token")

	err := e.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.CommentSeedDeleteUser(ctx, author.ID); err != nil {
			return err
		}
		return q.CommentSeedDeleteToken(ctx, tokenID)
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := e.Svc.Comments.List(ctx, e.Owner, tk.Ref, 0, "")
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("%+v %v", page, err)
	}
	if a := page.Items[0].Author; a.Name != "deleted user" || a.Type != service.ActorUser || a.ID != author.ID {
		t.Fatalf("user author %+v", a)
	}
	if a := page.Items[1].Author; a.Name != "deleted token" || a.Type != service.ActorAPIToken || a.ID != tokenID || a.OwnerName != "" {
		t.Fatalf("token author %+v", a)
	}
	acts, err := e.Svc.Activity.List(ctx, e.Owner, tk.Ref, 0, "")
	if err != nil || len(acts.Items) != 1 {
		t.Fatalf("%+v %v", acts, err)
	}
}

func TestCommentSvcResolve(t *testing.T) {
	e := newTk(t)
	e.create(t, "one", "")
	tk := e.create(t, "two", "")
	c := e.addComment(t, e.Editor, tk.Ref, "hi")
	r, err := e.Svc.Comments.Resolve(ctx, e.Viewer, strings.ToLower(c.ID))
	if err != nil || r.ID != c.ID || r.TicketID != tk.ID || r.TicketRef != "WEB-2" || r.ProjectID != e.P.ID || r.ProjectKey != "WEB" {
		t.Fatalf("%+v %v", r, err)
	}
	_, err = e.Svc.Comments.Resolve(ctx, e.UserActor(e.M.Users["non-member"]), c.ID)
	wantSvcErr(t, err, service.KindNotFound, "")
	owner := e.M.Users["owner"]
	limited := e.TokenActor(e.NewToken(t, owner.ID, service.ScopeWrite, e.Env.NewProject(t, owner, "OTH").ID))
	_, err = e.Svc.Comments.Resolve(ctx, limited, c.ID)
	wantSvcErr(t, err, service.KindNotFound, "")
}
