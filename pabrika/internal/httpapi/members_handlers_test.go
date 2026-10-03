package httpapi

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/RJGJ/Pabrika/internal/service"
)

const mp = "/api/v1/projects/WEB/members"

func TestMembersGoldenAndList(t *testing.T) {
	f := newRfx(t, Opts{})
	r := f.do(t, "GET", mp, nil, As(f.viewer))
	if r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	env := r.JSON(t)
	wantKeys(t, "envelope", env, "items", "next_cursor")
	if env["next_cursor"] != nil {
		t.Fatal("next_cursor must be null")
	}
	its := items(t, r)
	if len(its) != 3 {
		t.Fatalf("members: %s", r.Body)
	}
	for _, it := range its {
		wantKeys(t, "member", it, "user", "role", "created_at")
		wantKeys(t, "member.user", it["user"].(map[string]any), "id", "email", "display_name")
	}
	// Owners first, then display name: Owner, Editor, Viewer.
	var roles []string
	for _, it := range its {
		roles = append(roles, it["role"].(string))
	}
	if fmt.Sprint(roles) != "[owner editor viewer]" {
		t.Fatalf("order: %v", roles)
	}
	// Add a second owner "Aaron": owners first, then by name.
	aaron := f.h.Signup(t, "aaron@x.io")
	f.h.Env.AddMember(t, f.projID, aaron.User.ID, service.RoleOwner)
	its = items(t, f.do(t, "GET", mp, nil, As(f.owner)))
	var names []string
	for _, it := range its {
		names = append(names, it["user"].(map[string]any)["display_name"].(string))
	}
	if fmt.Sprint(names) != "[Aaron Owner Editor Viewer]" {
		t.Fatalf("owners-first order: %v", names)
	}
	// By ULID, by lowercase key, and with read token / limited token.
	for _, ref := range []string{f.projID, "web"} {
		if r := f.do(t, "GET", "/api/v1/projects/"+ref+"/members", nil, As(f.editor)); r.Code != 200 {
			t.Fatalf("%s: %d", ref, r.Code)
		}
	}
	for _, s := range []string{f.ownerRead, f.ownerLimited, f.ownerLimitedRead} {
		if r := f.do(t, "GET", mp, nil, Bearer(s)); r.Code != 200 {
			t.Fatalf("token list: %d %s", r.Code, r.Body)
		}
	}
}

func TestMembersListMatrix(t *testing.T) {
	call := func(f *rfx, o []ReqOpt) *Resp { return f.do(t, "GET", mp, nil, o...) }
	runMatrix(t, "list", call, []rfxRow{
		{whoAnon, 401, "unauthorized"},
		{whoOutside, 404, "not_found"},
		{whoViewer, 200, ""},
		{whoEditor, 200, ""},
		{whoOwner, 200, ""},
		{whoEdTok, 200, ""},
		{whoOwRead, 200, ""},
		{whoOwLim, 200, ""},
		{whoOwLimRead, 200, ""},
		{whoOwOthLim, 404, "not_found"},
	})
}

func TestMembersHiddenProjectIsByteIdenticalToRandomULID(t *testing.T) {
	f := newRfx(t, Opts{})
	uid := f.viewer.User.ID
	cases := []struct {
		method, suffix string
		body           any
	}{
		{"GET", "/members", nil},
		{"POST", "/members", map[string]any{"email": "viewer@x.io", "role": "editor"}},
		{"PATCH", "/members/" + uid, map[string]any{"role": "editor"}},
		{"DELETE", "/members/" + uid, nil},
	}
	for _, c := range cases {
		for _, who := range []struct {
			name string
			o    ReqOpt
		}{{"outsider", As(f.outside)}} {
			hidden := f.do(t, c.method, "/api/v1/projects/WEB"+c.suffix, c.body, who.o)
			rnd := f.do(t, c.method, "/api/v1/projects/"+randULID(f)+c.suffix, c.body, who.o)
			if hidden.Code != 404 || hidden.ErrCode(t) != "not_found" {
				t.Fatalf("%s %s: %d %s", c.method, c.suffix, hidden.Code, hidden.Body)
			}
			sameBytes(t, c.method+" "+c.suffix, hidden, rnd)
		}
	}
	// A project-limited token pointed at another project is also an identical 404 for the read.
	sameBytes(t, "limited elsewhere", f.do(t, "GET", mp, nil, Bearer(f.ownerOtherLimited)),
		f.do(t, "GET", "/api/v1/projects/"+randULID(f)+"/members", nil, Bearer(f.ownerOtherLimited)))
}

// ---- add ----

func TestMembersAdd(t *testing.T) {
	f := newRfx(t, Opts{})
	newbie := f.h.NewUser(t, "newbie@x.io", "Newbie")
	r := f.do(t, "POST", mp, map[string]any{"email": "newbie@x.io", "role": "editor"}, As(f.owner))
	if r.Code != 201 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	m := r.JSON(t)
	wantKeys(t, "member", m, "user", "role", "created_at")
	u := m["user"].(map[string]any)
	if u["id"] != newbie.ID || u["email"] != "newbie@x.io" || u["display_name"] != "Newbie" || m["role"] != "editor" {
		t.Fatalf("body: %s", r.Body)
	}
	// The new member sees the project with that role.
	nb := f.h.Login(t, newbie)
	if g := f.do(t, "GET", "/api/v1/projects/WEB", nil, As(nb)); g.Code != 200 || g.JSON(t)["role"] != "editor" {
		t.Fatalf("new member: %d %s", g.Code, g.Body)
	}
	// Already a member (also by different email case and whitespace is not special: same account).
	r = f.do(t, "POST", mp, map[string]any{"email": "newbie@x.io", "role": "viewer"}, As(f.owner))
	if r.Code != 409 || r.ErrCode(t) != "already_member" {
		t.Fatalf("dup: %d %s", r.Code, r.Body)
	}
	r = f.do(t, "POST", mp, map[string]any{"email": "editor@x.io", "role": "viewer"}, As(f.owner))
	if r.Code != 409 || r.ErrCode(t) != "already_member" {
		t.Fatalf("dup existing: %d %s", r.Code, r.Body)
	}
	// Adding by project ULID and on an archived project works (member management stays open).
	f.h.NewUser(t, "late@x.io", "Late")
	f.h.Env.Archive(t, f.projID)
	r = f.do(t, "POST", "/api/v1/projects/"+f.projID+"/members", map[string]any{"email": "late@x.io", "role": "owner"}, As(f.owner))
	if r.Code != 201 || r.JSON(t)["role"] != "owner" {
		t.Fatalf("archived add: %d %s", r.Code, r.Body)
	}
}

func TestMembersAddValidation(t *testing.T) {
	f := newRfx(t, Opts{})
	f.h.NewUser(t, "real@x.io", "Real")
	field := func(r *Resp, name string) bool {
		e, _ := r.JSON(t)["error"].(map[string]any)
		fs, _ := e["fields"].(map[string]any)
		_, ok := fs[name]
		return ok
	}
	r := f.do(t, "POST", mp, map[string]any{"email": "ghost@x.io", "role": "viewer"}, As(f.owner))
	if r.Code != 422 || r.ErrCode(t) != "validation_failed" || !field(r, "email") {
		t.Fatalf("unknown email: %d %s", r.Code, r.Body)
	}
	if msg := r.JSON(t)["error"].(map[string]any)["fields"].(map[string]any)["email"]; msg != "No account with this email" {
		t.Fatalf("message: %v", msg)
	}
	for name, body := range map[string]map[string]any{
		"bad role":     {"email": "real@x.io", "role": "admin"},
		"empty role":   {"email": "real@x.io", "role": ""},
		"missing role": {"email": "real@x.io"},
	} {
		r := f.do(t, "POST", mp, body, As(f.owner))
		if r.Code != 422 || !field(r, "role") {
			t.Fatalf("%s: %d %s", name, r.Code, r.Body)
		}
	}
	r = f.do(t, "POST", mp, map[string]any{"role": "viewer"}, As(f.owner))
	if r.Code != 422 || !field(r, "email") {
		t.Fatalf("missing email: %d %s", r.Code, r.Body)
	}
	for name, o := range map[string]ReqOpt{
		"unknown field": RawBody(`{"email":"real@x.io","role":"viewer","x":1}`, "application/json"),
		"malformed":     RawBody(`{`, "application/json"),
		"no body":       RawBody(``, "application/json"),
	} {
		if r := f.do(t, "POST", mp, nil, As(f.owner), o); r.Code != 400 {
			t.Fatalf("%s: %d %s", name, r.Code, r.Body)
		}
	}
	if r := f.do(t, "POST", mp, nil, As(f.owner), RawBody(`{"email":"real@x.io","role":"viewer"}`, "text/plain")); r.Code != 415 {
		t.Fatalf("415: %d", r.Code)
	}
	// Nothing was added by any of the above.
	if n := len(items(t, f.do(t, "GET", mp, nil, As(f.owner)))); n != 3 {
		t.Fatalf("members changed: %d", n)
	}
}

func TestMembersAddMatrix(t *testing.T) {
	body := map[string]any{"email": "target@x.io", "role": "viewer"}
	call := func(f *rfx, o []ReqOpt) *Resp {
		f.h.NewUser(t, "target@x.io", "Target")
		return f.do(t, "POST", mp, body, o...)
	}
	runMatrix(t, "add", call, []rfxRow{
		{whoAnon, 401, "unauthorized"},
		{whoOutside, 404, "not_found"},
		{whoViewer, 403, "forbidden"},
		{whoEditor, 403, "forbidden"},
		{whoOwner, 201, ""},
		{whoEdTok, 403, "session_required"},
		{whoOwTok, 403, "session_required"},
		{whoOwRead, 403, "session_required"},
		{whoOwLim, 403, "session_required"},
		{whoOwOthLim, 403, "session_required"},
	})
}

// ---- change role ----

func TestMembersSetRole(t *testing.T) {
	f := newRfx(t, Opts{})
	p := mp + "/" + f.viewer.User.ID
	r := f.do(t, "PATCH", p, map[string]any{"role": "editor"}, As(f.owner))
	if r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	wantKeys(t, "member", r.JSON(t), "user", "role", "created_at")
	if r.JSON(t)["role"] != "editor" || r.JSON(t)["user"].(map[string]any)["id"] != f.viewer.User.ID {
		t.Fatalf("body: %s", r.Body)
	}
	// The promoted user can write now (create a label), proving the change took effect.
	if g := f.do(t, "POST", "/api/v1/projects/WEB/labels", map[string]any{"name": "x"}, As(f.viewer)); g.Code != 201 {
		t.Fatalf("promoted editor: %d %s", g.Code, g.Body)
	}
	// Same role again is a 200 no-op; demote works.
	if r = f.do(t, "PATCH", p, map[string]any{"role": "editor"}, As(f.owner)); r.Code != 200 {
		t.Fatalf("no-op: %d", r.Code)
	}
	if r = f.do(t, "PATCH", p, map[string]any{"role": "viewer"}, As(f.owner)); r.Code != 200 || r.JSON(t)["role"] != "viewer" {
		t.Fatalf("demote: %d %s", r.Code, r.Body)
	}
	// Target that is not a member of this project (or unknown, or a non-ULID) is 404.
	for _, uid := range []string{f.outside.User.ID, randULID(f), "nope"} {
		r = f.do(t, "PATCH", mp+"/"+uid, map[string]any{"role": "editor"}, As(f.owner))
		if r.Code != 404 || r.ErrCode(t) != "not_found" {
			t.Fatalf("target %s: %d %s", uid, r.Code, r.Body)
		}
	}
	// Validation.
	for name, body := range map[string]string{"bad role": `{"role":"boss"}`, "missing": `{}`, "null": `{"role":null}`} {
		r = f.do(t, "PATCH", p, nil, As(f.owner), RawBody(body, "application/json"))
		if r.Code != 422 {
			t.Fatalf("%s: %d %s", name, r.Code, r.Body)
		}
	}
	if r = f.do(t, "PATCH", p, nil, As(f.owner), RawBody(`{"role":"owner","x":1}`, "application/json")); r.Code != 400 {
		t.Fatalf("unknown field: %d", r.Code)
	}
	// Works on an archived project.
	f.h.Env.Archive(t, f.projID)
	if r = f.do(t, "PATCH", p, map[string]any{"role": "editor"}, As(f.owner)); r.Code != 200 {
		t.Fatalf("archived: %d %s", r.Code, r.Body)
	}
}

func TestMembersSetRoleLastOwner(t *testing.T) {
	f := newRfx(t, Opts{})
	self := mp + "/" + f.owner.User.ID
	r := f.do(t, "PATCH", self, map[string]any{"role": "editor"}, As(f.owner))
	if r.Code != 409 || r.ErrCode(t) != "last_owner" {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	// Confirm nothing changed, then with a second owner the demotion is fine.
	if g := f.do(t, "GET", "/api/v1/projects/WEB", nil, As(f.owner)); g.JSON(t)["role"] != "owner" {
		t.Fatalf("role changed: %s", g.Body)
	}
	if r = f.do(t, "PATCH", mp+"/"+f.editor.User.ID, map[string]any{"role": "owner"}, As(f.owner)); r.Code != 200 {
		t.Fatalf("promote: %d", r.Code)
	}
	if r = f.do(t, "PATCH", self, map[string]any{"role": "viewer"}, As(f.owner)); r.Code != 200 {
		t.Fatalf("demote self with 2 owners: %d %s", r.Code, r.Body)
	}
	// Now the former editor is the last owner.
	r = f.do(t, "PATCH", mp+"/"+f.editor.User.ID, map[string]any{"role": "viewer"}, As(f.editor))
	if r.Code != 409 || r.ErrCode(t) != "last_owner" {
		t.Fatalf("last owner again: %d %s", r.Code, r.Body)
	}
}

func TestMembersSetRoleMatrix(t *testing.T) {
	call := func(f *rfx, o []ReqOpt) *Resp {
		return f.do(t, "PATCH", mp+"/"+f.viewer.User.ID, map[string]any{"role": "editor"}, o...)
	}
	runMatrix(t, "set-role", call, []rfxRow{
		{whoAnon, 401, "unauthorized"},
		{whoOutside, 404, "not_found"},
		{whoViewer, 403, "forbidden"},
		{whoEditor, 403, "forbidden"},
		{whoOwner, 200, ""},
		{whoEdTok, 403, "session_required"},
		{whoOwTok, 403, "session_required"},
		{whoOwRead, 403, "session_required"},
		{whoOwLim, 403, "session_required"},
		{whoOwOthLim, 403, "session_required"},
	})
}

// ---- remove / leave ----

func TestMembersRemoveByOwner(t *testing.T) {
	f := newRfx(t, Opts{})
	// An assignment of the removed user is cleared.
	ctx := context.Background()
	oa := service.UserActor(f.owner.User.ID)
	lbl, err := f.h.Env.Svc.Labels.Create(ctx, oa, "WEB", service.LabelInput{Name: "l"})
	_ = lbl
	if err != nil {
		t.Fatal(err)
	}
	uid := f.editor.User.ID
	tk, err := f.h.Env.Svc.Tickets.Create(ctx, oa, "WEB", service.CreateTicketInput{Title: "t", AssigneeID: &uid})
	if err != nil || tk.Assignee == nil {
		t.Fatalf("seed ticket: %v %+v", err, tk)
	}
	r := f.do(t, "DELETE", mp+"/"+uid, nil, As(f.owner))
	if r.Code != 204 || len(r.Body) != 0 {
		t.Fatalf("%d %q", r.Code, r.Body)
	}
	if g := f.do(t, "GET", "/api/v1/projects/WEB", nil, As(f.editor)); g.Code != 404 {
		t.Fatalf("removed member still sees project: %d", g.Code)
	}
	after, err := f.h.Env.Svc.Tickets.Get(ctx, oa, tk.ID)
	if err != nil || after.Assignee != nil {
		t.Fatalf("assignee not cleared: %v %+v", err, after.Assignee)
	}
	// Removing again: the target is no longer a member.
	if r = f.do(t, "DELETE", mp+"/"+uid, nil, As(f.owner)); r.Code != 404 {
		t.Fatalf("again: %d %s", r.Code, r.Body)
	}
	// Unknown or non-member targets are 404.
	for _, id := range []string{randULID(f), f.outside.User.ID} {
		if r = f.do(t, "DELETE", mp+"/"+id, nil, As(f.owner)); r.Code != 404 {
			t.Fatalf("target %s: %d", id, r.Code)
		}
	}
	// Works on an archived project, by ULID.
	f.h.Env.Archive(t, f.projID)
	if r = f.do(t, "DELETE", "/api/v1/projects/"+f.projID+"/members/"+f.viewer.User.ID, nil, As(f.owner)); r.Code != 204 {
		t.Fatalf("archived remove: %d %s", r.Code, r.Body)
	}
}

func TestMembersLeave(t *testing.T) {
	for _, c := range []struct {
		name string
		cl   func(f *rfx) *Client
	}{
		{"viewer", func(f *rfx) *Client { return f.viewer }},
		{"editor", func(f *rfx) *Client { return f.editor }},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newRfx(t, Opts{})
			cl := c.cl(f)
			if r := f.do(t, "DELETE", mp+"/"+cl.User.ID, nil, As(cl)); r.Code != 204 {
				t.Fatalf("leave: %d %s", r.Code, r.Body)
			}
			if g := f.do(t, "GET", "/api/v1/projects/WEB", nil, As(cl)); g.Code != 404 {
				t.Fatalf("after leave: %d", g.Code)
			}
			if n := len(items(t, f.do(t, "GET", mp, nil, As(f.owner)))); n != 2 {
				t.Fatalf("members: %d", n)
			}
		})
	}
	t.Run("non-owner cannot remove others", func(t *testing.T) {
		f := newRfx(t, Opts{})
		for _, cl := range []*Client{f.viewer, f.editor} {
			for _, target := range []*Client{f.owner, f.viewer, f.editor} {
				if target == cl {
					continue
				}
				r := f.do(t, "DELETE", mp+"/"+target.User.ID, nil, As(cl))
				if r.Code != 403 || r.ErrCode(t) != "forbidden" {
					t.Fatalf("%s removing %s: %d %s", cl.User.Email, target.User.Email, r.Code, r.Body)
				}
			}
		}
		if n := len(items(t, f.do(t, "GET", mp, nil, As(f.owner)))); n != 3 {
			t.Fatalf("members changed: %d", n)
		}
	})
	t.Run("last owner", func(t *testing.T) {
		f := newRfx(t, Opts{})
		for _, id := range []string{f.owner.User.ID} {
			r := f.do(t, "DELETE", mp+"/"+id, nil, As(f.owner))
			if r.Code != 409 || r.ErrCode(t) != "last_owner" {
				t.Fatalf("self leave: %d %s", r.Code, r.Body)
			}
		}
		// With a second owner the first may leave; the second then cannot.
		f.h.Env.AddMember(t, f.projID, f.outside.User.ID, service.RoleOwner)
		if r := f.do(t, "DELETE", mp+"/"+f.owner.User.ID, nil, As(f.owner)); r.Code != 204 {
			t.Fatalf("leave with 2 owners: %d %s", r.Code, r.Body)
		}
		r := f.do(t, "DELETE", mp+"/"+f.outside.User.ID, nil, As(f.outside))
		if r.Code != 409 || r.ErrCode(t) != "last_owner" {
			t.Fatalf("new last owner: %d %s", r.Code, r.Body)
		}
	})
	t.Run("non-member is 404 even for self", func(t *testing.T) {
		f := newRfx(t, Opts{})
		if r := f.do(t, "DELETE", mp+"/"+f.outside.User.ID, nil, As(f.outside)); r.Code != 404 {
			t.Fatalf("%d %s", r.Code, r.Body)
		}
	})
}

func TestMembersRemoveMatrix(t *testing.T) {
	call := func(f *rfx, o []ReqOpt) *Resp { return f.do(t, "DELETE", mp+"/"+f.viewer.User.ID, nil, o...) }
	runMatrix(t, "remove-other", call, []rfxRow{
		{whoAnon, 401, "unauthorized"},
		{whoOutside, 404, "not_found"},
		{whoViewer, 204, ""}, // the viewer is the target: this is a leave
		{whoEditor, 403, "forbidden"},
		{whoOwner, 204, ""},
		{whoEdTok, 403, "session_required"},
		{whoOwTok, 403, "session_required"},
		{whoOwRead, 403, "session_required"},
		{whoOwLim, 403, "session_required"},
		{whoOwOthLim, 403, "session_required"},
	})
}

// ---- concurrency ----

// Two owners demote/remove each other at the same moment: the project must keep an owner.
func TestMembersConcurrentLastOwnerRace(t *testing.T) {
	for i := 0; i < 5; i++ {
		f := newRfx(t, Opts{File: true})
		f.h.Env.AddMember(t, f.projID, f.outside.User.ID, service.RoleOwner)
		a, b := f.owner, f.outside
		var wg sync.WaitGroup
		codes := make([]int, 2)
		start := make(chan struct{})
		op := func(idx int, actor, target *Client, remove bool) {
			defer wg.Done()
			<-start
			if remove {
				codes[idx] = f.do(t, "DELETE", mp+"/"+target.User.ID, nil, As(actor)).Code
			} else {
				codes[idx] = f.do(t, "PATCH", mp+"/"+target.User.ID, map[string]any{"role": "viewer"}, As(actor)).Code
			}
		}
		wg.Add(2)
		go op(0, a, b, i%2 == 0)
		go op(1, b, a, i%2 == 0)
		close(start)
		wg.Wait()
		owners := 0
		for _, it := range items(t, f.do(t, "GET", mp, nil, As(f.viewer))) {
			if it["role"] == "owner" {
				owners++
			}
		}
		if owners < 1 {
			t.Fatalf("round %d: project lost every owner (codes %v)", i, codes)
		}
		for _, c := range codes {
			if c != 200 && c != 204 && c != 404 && c != 403 && c != 409 {
				t.Fatalf("round %d: unexpected status %d", i, c)
			}
		}
	}
}

// Two simultaneous adds of the same user: exactly one 201, the other 409 already_member.
func TestMembersConcurrentDuplicateAdd(t *testing.T) {
	f := newRfx(t, Opts{File: true})
	f.h.NewUser(t, "dup@x.io", "Dup")
	var wg sync.WaitGroup
	codes := make([]int, 4)
	start := make(chan struct{})
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			codes[i] = f.do(t, "POST", mp, map[string]any{"email": "dup@x.io", "role": "viewer"}, As(f.owner)).Code
		}(i)
	}
	close(start)
	wg.Wait()
	created, conflict := 0, 0
	for _, c := range codes {
		switch c {
		case 201:
			created++
		case 409:
			conflict++
		}
	}
	if created != 1 || conflict != len(codes)-1 {
		t.Fatalf("codes: %v", codes)
	}
}
