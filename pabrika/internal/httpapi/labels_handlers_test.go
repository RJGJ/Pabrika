package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/RJGJ/Pabrika/internal/service"
)

const lp = "/api/v1/projects/WEB/labels"

func (f *rfx) mkLabel(t testing.TB, name string) string {
	t.Helper()
	l, err := f.h.Env.Svc.Labels.Create(context.Background(), service.UserActor(f.owner.User.ID), "WEB", service.LabelInput{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return l.ID
}

func TestLabelsGoldenCreateList(t *testing.T) {
	f := newRfx(t, Opts{})
	r := f.do(t, "POST", lp, map[string]any{"name": "  Bug  "}, As(f.editor))
	if r.Code != 201 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	m := r.JSON(t)
	wantKeys(t, "label", m, "id", "project_id", "name", "color")
	if m["name"] != "Bug" || m["color"] != "gray" || m["project_id"] != f.projID {
		t.Fatalf("body: %s", r.Body)
	}
	if r = f.do(t, "POST", lp, map[string]any{"name": "api", "color": "teal"}, As(f.owner)); r.Code != 201 || r.JSON(t)["color"] != "teal" {
		t.Fatalf("colored: %d %s", r.Code, r.Body)
	}
	f.do(t, "POST", "/api/v1/projects/"+f.projID+"/labels", map[string]any{"name": "Zed"}, As(f.owner))
	r = f.do(t, "GET", lp, nil, As(f.viewer))
	env := r.JSON(t)
	wantKeys(t, "envelope", env, "items", "next_cursor")
	if env["next_cursor"] != nil {
		t.Fatal("next_cursor must be null")
	}
	var names []string
	for _, it := range items(t, r) {
		wantKeys(t, "item", it, "id", "project_id", "name", "color")
		names = append(names, it["name"].(string))
	}
	if fmt.Sprint(names) != "[api Bug Zed]" {
		t.Fatalf("order: %v", names)
	}
	// Empty list is [] and labels of another project do not leak.
	r = f.do(t, "GET", "/api/v1/projects/OTH/labels", nil, As(f.owner))
	if r.Code != 200 || !bytes.Contains(r.Body, []byte(`"items":[]`)) {
		t.Fatalf("empty: %d %s", r.Code, r.Body)
	}
}

func TestLabelsCreateValidation(t *testing.T) {
	f := newRfx(t, Opts{})
	f.mkLabel(t, "Bug")
	if r := f.do(t, "POST", lp, map[string]any{"name": "bUG"}, As(f.editor)); r.Code != 409 || r.ErrCode(t) != "label_exists" {
		t.Fatalf("dup: %d %s", r.Code, r.Body)
	}
	// Same name in another project is fine.
	if r := f.do(t, "POST", "/api/v1/projects/OTH/labels", map[string]any{"name": "Bug"}, As(f.owner)); r.Code != 201 {
		t.Fatalf("other project: %d %s", r.Code, r.Body)
	}
	field := func(r *Resp) map[string]any {
		return r.JSON(t)["error"].(map[string]any)["fields"].(map[string]any)
	}
	long := string(bytes.Repeat([]byte("x"), 51))
	for name, c := range map[string]struct {
		body  map[string]any
		field string
	}{
		"empty":     {map[string]any{"name": ""}, "name"},
		"blank":     {map[string]any{"name": "   "}, "name"},
		"missing":   {map[string]any{}, "name"},
		"51 chars":  {map[string]any{"name": long}, "name"},
		"bad color": {map[string]any{"name": "ok", "color": "magenta"}, "color"},
	} {
		r := f.do(t, "POST", lp, c.body, As(f.owner))
		if r.Code != 422 || r.ErrCode(t) != "validation_failed" {
			t.Fatalf("%s: %d %s", name, r.Code, r.Body)
		}
		if _, ok := field(r)[c.field]; !ok {
			t.Fatalf("%s: fields %v", name, field(r))
		}
	}
	if r := f.do(t, "POST", lp, map[string]any{"name": string(bytes.Repeat([]byte("x"), 50))}, As(f.owner)); r.Code != 201 {
		t.Fatalf("50 chars: %d", r.Code)
	}
	for _, color := range []string{"gray", "red", "orange", "amber", "green", "teal", "blue", "indigo", "purple", "pink"} {
		if r := f.do(t, "POST", lp, map[string]any{"name": "c-" + color, "color": color}, As(f.owner)); r.Code != 201 {
			t.Fatalf("color %s: %d %s", color, r.Code, r.Body)
		}
	}
	if r := f.do(t, "POST", lp, nil, As(f.owner), RawBody(`{"name":"q","x":1}`, "application/json")); r.Code != 400 {
		t.Fatalf("unknown field: %d", r.Code)
	}
	if r := f.do(t, "POST", lp, nil, As(f.owner)); r.Code != 400 {
		t.Fatalf("no body: %d", r.Code)
	}
	if r := f.do(t, "POST", lp, nil, As(f.owner), RawBody(`{"name":"q"}`, "text/plain")); r.Code != 415 {
		t.Fatalf("415: %d", r.Code)
	}
}

func TestLabelsPatchAndDelete(t *testing.T) {
	f := newRfx(t, Opts{})
	id := f.mkLabel(t, "Bug")
	other := f.mkLabel(t, "Other")
	p := "/api/v1/labels/" + id
	r := f.do(t, "PATCH", p, map[string]any{"name": " Defect ", "color": "red"}, As(f.editor))
	if r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	wantKeys(t, "label", r.JSON(t), "id", "project_id", "name", "color")
	if r.JSON(t)["name"] != "Defect" || r.JSON(t)["color"] == "gray" || r.JSON(t)["id"] != id {
		t.Fatalf("body: %s", r.Body)
	}
	// Partial: only color; {} is a no-op.
	if r = f.do(t, "PATCH", p, map[string]any{"color": "blue"}, As(f.owner)); r.Code != 200 || r.JSON(t)["name"] != "Defect" || r.JSON(t)["color"] != "blue" {
		t.Fatalf("color only: %d %s", r.Code, r.Body)
	}
	if r = f.do(t, "PATCH", p, map[string]any{}, As(f.owner)); r.Code != 200 || r.JSON(t)["name"] != "Defect" {
		t.Fatalf("no-op: %d %s", r.Code, r.Body)
	}
	// Renaming to the same name in different case is the same label (allowed); to another's is 409.
	if r = f.do(t, "PATCH", p, map[string]any{"name": "DEFECT"}, As(f.owner)); r.Code != 200 {
		t.Fatalf("case rename: %d %s", r.Code, r.Body)
	}
	if r = f.do(t, "PATCH", p, map[string]any{"name": "other"}, As(f.owner)); r.Code != 409 || r.ErrCode(t) != "label_exists" {
		t.Fatalf("rename clash: %d %s", r.Code, r.Body)
	}
	for name, body := range map[string]string{
		"name null": `{"name":null}`, "color null": `{"color":null}`, "empty": `{"name":""}`, "bad color": `{"color":"x"}`,
	} {
		if r = f.do(t, "PATCH", p, nil, As(f.owner), RawBody(body, "application/json")); r.Code != 422 {
			t.Fatalf("%s: %d %s", name, r.Code, r.Body)
		}
	}
	if r = f.do(t, "PATCH", p, nil, As(f.owner), RawBody(`{"project_id":"x"}`, "application/json")); r.Code != 400 {
		t.Fatalf("unknown: %d", r.Code)
	}
	// Unknown label ids are 404.
	for _, bad := range []string{randULID(f), "nope"} {
		if r = f.do(t, "PATCH", "/api/v1/labels/"+bad, map[string]any{"name": "x"}, As(f.owner)); r.Code != 404 {
			t.Fatalf("patch %s: %d", bad, r.Code)
		}
		if r = f.do(t, "DELETE", "/api/v1/labels/"+bad, nil, As(f.owner)); r.Code != 404 {
			t.Fatalf("delete %s: %d", bad, r.Code)
		}
	}
	// Delete removes it from tickets (cascade) and from the list.
	a := service.UserActor(f.owner.User.ID)
	tk, err := f.h.Env.Svc.Tickets.Create(context.Background(), a, "WEB", service.CreateTicketInput{Title: "t", LabelIDs: []string{id, other}})
	if err != nil || len(tk.Labels) != 2 {
		t.Fatalf("seed: %v %+v", err, tk.Labels)
	}
	if r = f.do(t, "DELETE", p, nil, As(f.editor)); r.Code != 204 || len(r.Body) != 0 {
		t.Fatalf("delete: %d %q", r.Code, r.Body)
	}
	got, _ := f.h.Env.Svc.Tickets.Get(context.Background(), a, tk.ID)
	if len(got.Labels) != 1 || got.Labels[0].ID != other {
		t.Fatalf("cascade: %+v", got.Labels)
	}
	if r = f.do(t, "DELETE", p, nil, As(f.editor)); r.Code != 404 {
		t.Fatalf("second delete: %d", r.Code)
	}
	if r = f.do(t, "GET", lp, nil, As(f.owner)); len(items(t, r)) != 1 {
		t.Fatalf("list: %s", r.Body)
	}
}

func TestLabelsListMatrix(t *testing.T) {
	call := func(f *rfx, o []ReqOpt) *Resp { return f.do(t, "GET", lp, nil, o...) }
	runMatrix(t, "list", call, []rfxRow{
		{whoAnon, 401, "unauthorized"},
		{whoOutside, 404, "not_found"},
		{whoViewer, 200, ""},
		{whoEditor, 200, ""},
		{whoOwner, 200, ""},
		{whoOwRead, 200, ""},
		{whoOwLim, 200, ""},
		{whoOwLimRead, 200, ""},
		{whoOwOthLim, 404, "not_found"},
	})
}

func TestLabelsCreateMatrix(t *testing.T) {
	call := func(f *rfx, o []ReqOpt) *Resp { return f.do(t, "POST", lp, map[string]any{"name": "n"}, o...) }
	runMatrix(t, "create", call, []rfxRow{
		{whoAnon, 401, "unauthorized"},
		{whoOutside, 404, "not_found"},
		{whoViewer, 403, "forbidden"},
		{whoEditor, 201, ""},
		{whoOwner, 201, ""},
		{whoEdTok, 201, ""},
		{whoOwTok, 201, ""},
		{whoOwRead, 403, "insufficient_scope"},
		{whoOwLim, 201, ""},
		{whoOwLimRead, 403, "insufficient_scope"},
		{whoOwOthLim, 404, "not_found"},
	})
}

func TestLabelsPatchDeleteMatrix(t *testing.T) {
	rows := func(ok int) []rfxRow {
		return []rfxRow{
			{whoAnon, 401, "unauthorized"},
			{whoOutside, 404, "not_found"},
			{whoViewer, 403, "forbidden"},
			{whoEditor, ok, ""},
			{whoOwner, ok, ""},
			{whoEdTok, ok, ""},
			{whoOwRead, 403, "insufficient_scope"},
			{whoOwLim, ok, ""},
			{whoOwLimRead, 403, "insufficient_scope"},
			{whoOwOthLim, 404, "not_found"},
		}
	}
	runMatrix(t, "patch", func(f *rfx, o []ReqOpt) *Resp {
		return f.do(t, "PATCH", "/api/v1/labels/"+f.mkLabel(t, "Bug"), map[string]any{"name": "New"}, o...)
	}, rows(200))
	runMatrix(t, "delete", func(f *rfx, o []ReqOpt) *Resp {
		return f.do(t, "DELETE", "/api/v1/labels/"+f.mkLabel(t, "Bug"), nil, o...)
	}, rows(204))
}

func TestLabelsHiddenIsByteIdenticalToRandomULID(t *testing.T) {
	f := newRfx(t, Opts{})
	id := f.mkLabel(t, "Bug")
	for _, c := range []struct {
		method, hidden, rnd string
		body                any
	}{
		{"GET", lp, "/api/v1/projects/" + randULID(f) + "/labels", nil},
		{"POST", lp, "/api/v1/projects/" + randULID(f) + "/labels", map[string]any{"name": "n"}},
		{"PATCH", "/api/v1/labels/" + id, "/api/v1/labels/" + randULID(f), map[string]any{"name": "n"}},
		{"DELETE", "/api/v1/labels/" + id, "/api/v1/labels/" + randULID(f), nil},
	} {
		for _, o := range []ReqOpt{As(f.outside), Bearer(f.outsiderWrite)} {
			h, r := f.do(t, c.method, c.hidden, c.body, o), f.do(t, c.method, c.rnd, c.body, o)
			if h.Code != 404 {
				t.Fatalf("%s %s: %d %s", c.method, c.hidden, h.Code, h.Body)
			}
			sameBytes(t, c.method+" "+c.hidden, h, r)
		}
	}
	// A token limited to another project sees this label as missing too.
	h := f.do(t, "DELETE", "/api/v1/labels/"+id, nil, Bearer(f.ownerOtherLimited))
	sameBytes(t, "limited elsewhere", h, f.do(t, "DELETE", "/api/v1/labels/"+randULID(f), nil, Bearer(f.ownerOtherLimited)))
	if g := f.do(t, "GET", lp, nil, As(f.owner)); len(items(t, g)) != 1 {
		t.Fatalf("label must survive: %s", g.Body)
	}
}

func TestLabelsArchivedProject(t *testing.T) {
	f := newRfx(t, Opts{})
	id := f.mkLabel(t, "Bug")
	f.h.Env.Archive(t, f.projID)
	if r := f.do(t, "GET", lp, nil, As(f.viewer)); r.Code != 200 || len(items(t, r)) != 1 {
		t.Fatalf("read: %d %s", r.Code, r.Body)
	}
	for name, r := range map[string]*Resp{
		"create": f.do(t, "POST", lp, map[string]any{"name": "n"}, As(f.owner)),
		"patch":  f.do(t, "PATCH", "/api/v1/labels/"+id, map[string]any{"name": "n"}, As(f.owner)),
		"delete": f.do(t, "DELETE", "/api/v1/labels/"+id, nil, As(f.owner)),
	} {
		if r.Code != 409 || r.ErrCode(t) != "project_archived" {
			t.Fatalf("%s: %d %s", name, r.Code, r.Body)
		}
	}
	// Role and membership still come first: a viewer gets 403, an outsider 404.
	if r := f.do(t, "POST", lp, map[string]any{"name": "n"}, As(f.viewer)); r.Code != 403 {
		t.Fatalf("viewer: %d", r.Code)
	}
	if r := f.do(t, "POST", lp, map[string]any{"name": "n"}, As(f.outside)); r.Code != 404 {
		t.Fatalf("outsider: %d", r.Code)
	}
	// Unarchiving restores writes.
	f.do(t, "PATCH", "/api/v1/projects/WEB", map[string]any{"archived": false}, As(f.owner))
	if r := f.do(t, "POST", lp, map[string]any{"name": "n"}, As(f.owner)); r.Code != 201 {
		t.Fatalf("after unarchive: %d", r.Code)
	}
}
