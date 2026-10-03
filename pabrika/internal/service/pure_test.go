package service_test

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/RJGJ/Pabrika/internal/service"
)

func TestErrors(t *testing.T) {
	e := service.NewError(service.KindNotFound, "not_found", "nope")
	if !errors.Is(e, service.ErrNotFound) || errors.Is(e, service.ErrForbidden) {
		t.Fatal("sentinel matching by kind failed")
	}
	v := service.Validation(map[string]string{"title": "bad title", "due_date": "bad date"})
	if v.Message != "bad date" || v.Code != "validation_failed" || !errors.Is(v, service.ErrValidation) {
		t.Fatalf("%+v", v)
	}
	var wrapped error = v
	var se *service.Error
	if !errors.As(wrapped, &se) || se.Fields["title"] != "bad title" {
		t.Fatal("As")
	}
}

func TestRoleAndActor(t *testing.T) {
	if !service.RoleOwner.AtLeast(service.RoleEditor) || service.RoleViewer.AtLeast(service.RoleEditor) ||
		!service.RoleEditor.AtLeast(service.RoleEditor) || service.Role("x").AtLeast(service.RoleViewer) {
		t.Fatal("role ranking")
	}
	if service.Role("x").Valid() || !service.RoleViewer.Valid() {
		t.Fatal("role valid")
	}
	u := service.UserActor("U")
	if u.Type != service.ActorUser || u.ID != "U" || u.UserID != "U" || u.Scope != service.ScopeWrite || u.IsToken() {
		t.Fatalf("%+v", u)
	}
	k := service.TokenActor("T", "U", service.ScopeRead, "P")
	if !k.IsToken() || k.ID != "T" || k.UserID != "U" || k.ProjectID != "P" || k.Scope != service.ScopeRead {
		t.Fatalf("%+v", k)
	}
}

func TestOptionalJSON(t *testing.T) {
	type in struct {
		S service.Optional[string]   `json:"s"`
		P service.Optional[*string]  `json:"p"`
		L service.Optional[[]string] `json:"l"`
	}
	var a in
	if err := json.Unmarshal([]byte(`{}`), &a); err != nil {
		t.Fatal(err)
	}
	if a.S.Set || a.P.Set || a.L.Set {
		t.Fatal("absent must be unset")
	}
	var b in
	if err := json.Unmarshal([]byte(`{"s":null,"p":null,"l":null}`), &b); err != nil {
		t.Fatal(err)
	}
	if !b.S.Set || !b.S.Null || !b.P.Set || !b.P.Null || !b.L.Set || !b.L.Null {
		t.Fatalf("null must be Set+Null: %+v", b)
	}
	var c in
	if err := json.Unmarshal([]byte(`{"s":"x","p":"2026-01-02","l":["a","b"]}`), &c); err != nil {
		t.Fatal(err)
	}
	if !c.S.Set || c.S.Null || c.S.Value != "x" || c.P.Value == nil || *c.P.Value != "2026-01-02" || len(c.L.Value) != 2 {
		t.Fatalf("%+v", c)
	}
	var d in
	if err := json.Unmarshal([]byte(`{"l":[]}`), &d); err != nil || !d.L.Set || d.L.Null {
		t.Fatalf("empty list is a value, not null: %+v %v", d, err)
	}
	if err := json.Unmarshal([]byte(`{"s":5}`), &d); err == nil {
		t.Fatal("type mismatch should fail")
	}
	if got := service.Some(3); !got.Set || got.Null || got.Value != 3 {
		t.Fatal("Some")
	}
	if got := service.Null[int](); !got.Set || !got.Null {
		t.Fatal("Null")
	}
}

func TestExportedConstants(t *testing.T) {
	if !reflect.DeepEqual(service.Statuses, []service.Status{"backlog", "todo", "in_progress", "done"}) {
		t.Fatal("statuses")
	}
	if !reflect.DeepEqual(service.Priorities, []service.Priority{"low", "medium", "high", "urgent"}) {
		t.Fatal("priorities")
	}
	var colors []string
	for _, c := range service.LabelColors {
		colors = append(colors, string(c))
	}
	if strings.Join(colors, ",") != "gray,red,orange,amber,green,teal,blue,indigo,purple,pink" {
		t.Fatal("palette", colors)
	}
	got := []int{service.MaxTitle, service.MaxDescription, service.MaxCommentBody, service.MaxProjectName,
		service.MaxProjectDescription, service.MaxLabelName, service.MaxDisplayName, service.MaxLabelsPerTicket}
	want := []int{200, 20000, 20000, 100, 2000, 50, 100, 50}
	if !reflect.DeepEqual(got, want) || service.DueDateLayout != "2006-01-02" {
		t.Fatal("limits", got)
	}
	if service.Status("done").Rank() != 3 || service.Status("zzz").Valid() || !service.Priority("urgent").Valid() || service.LabelColor("mauve").Valid() {
		t.Fatal("enum helpers")
	}
}

func fieldsOf(t *testing.T, err error) map[string]string {
	t.Helper()
	if err == nil {
		return nil
	}
	var se *service.Error
	if !errors.As(err, &se) || se.Kind != service.KindValidation {
		t.Fatalf("not a validation error: %v", err)
	}
	return se.Fields
}

func str(n int) string { return strings.Repeat("a", n) }

func TestValidate(t *testing.T) {
	ptr := func(s string) *string { return &s }
	tests := []struct {
		name string
		err  error
		want []string // field names expected in Fields (sorted); nil = valid
	}{
		{"project ok", service.CreateProjectInput{Key: " web ", Name: " Web "}.Validate(), nil},
		{"project bad key", service.CreateProjectInput{Key: "W", Name: "n"}.Validate(), []string{"key"}},
		{"project key digits", service.CreateProjectInput{Key: "WEB1", Name: "n"}.Validate(), []string{"key"}},
		{"project key long", service.CreateProjectInput{Key: "ABCDEFG", Name: "n"}.Validate(), []string{"key"}},
		{"project name empty", service.CreateProjectInput{Key: "WEB", Name: "   "}.Validate(), []string{"name"}},
		{"project name 100", service.CreateProjectInput{Key: "WEB", Name: str(100)}.Validate(), nil},
		{"project name 101", service.CreateProjectInput{Key: "WEB", Name: str(101)}.Validate(), []string{"name"}},
		{"project desc 2001", service.CreateProjectInput{Key: "WEB", Name: "n", Description: str(2001)}.Validate(), []string{"description"}},
		{"project update null", service.UpdateProjectInput{Name: service.Null[string](), Archived: service.Null[bool]()}.Validate(), []string{"archived", "name"}},
		{"project update ok", service.UpdateProjectInput{Archived: service.Some(true)}.Validate(), nil},
		{"label ok default color", service.LabelInput{Name: "bug"}.Validate(), nil},
		{"label bad color", service.LabelInput{Name: "bug", Color: "mauve"}.Validate(), []string{"color"}},
		{"label name 51", service.LabelInput{Name: str(51)}.Validate(), []string{"name"}},
		{"label update null color", service.UpdateLabelInput{Color: service.Null[service.LabelColor]()}.Validate(), []string{"color"}},
		{"label update bad color", service.UpdateLabelInput{Color: service.Some(service.LabelColor("x"))}.Validate(), []string{"color"}},
		{"ticket ok", service.CreateTicketInput{Title: "t"}.Validate(), nil},
		{"ticket title 200", service.CreateTicketInput{Title: str(200)}.Validate(), nil},
		{"ticket title 201", service.CreateTicketInput{Title: str(201)}.Validate(), []string{"title"}},
		{"ticket title multibyte 200 runes", service.CreateTicketInput{Title: strings.Repeat("é", 200)}.Validate(), nil},
		{"ticket desc 20000", service.CreateTicketInput{Title: "t", Description: str(20000)}.Validate(), nil},
		{"ticket desc 20001", service.CreateTicketInput{Title: "t", Description: str(20001)}.Validate(), []string{"description"}},
		{"ticket bad enums", service.CreateTicketInput{Title: "t", Status: "x", Priority: "y"}.Validate(), []string{"priority", "status"}},
		{"ticket bad date", service.CreateTicketInput{Title: "t", DueDate: ptr("2026-02-30")}.Validate(), []string{"due_date"}},
		{"ticket date shape", service.CreateTicketInput{Title: "t", DueDate: ptr("2026-2-3")}.Validate(), []string{"due_date"}},
		{"ticket date empty ok", service.CreateTicketInput{Title: "t", DueDate: ptr("")}.Validate(), nil},
		{"ticket too many labels", service.CreateTicketInput{Title: "t", LabelIDs: manyIDs(51)}.Validate(), []string{"labels"}},
		{"ticket 50 labels", service.CreateTicketInput{Title: "t", LabelIDs: manyIDs(50)}.Validate(), nil},
		{"ticket dup labels collapse", service.CreateTicketInput{Title: "t", LabelIDs: append(manyIDs(50), "id0")}.Validate(), nil},
		{"update nulls", service.UpdateTicketInput{Title: service.Null[string](), Description: service.Null[string](), Priority: service.Null[service.Priority]()}.Validate(), []string{"description", "priority", "title"}},
		{"update nullable ok", service.UpdateTicketInput{DueDate: service.Null[*string](), AssigneeID: service.Null[*string](), LabelIDs: service.Null[[]string]()}.Validate(), nil},
		{"update bad", service.UpdateTicketInput{Title: service.Some(""), Priority: service.Some(service.Priority("z")), DueDate: service.Some(ptr("nope"))}.Validate(), []string{"due_date", "priority", "title"}},
		{"move ok", service.MoveInput{Status: "todo"}.Validate(), nil},
		{"move no status", service.MoveInput{}.Validate(), []string{"status"}},
		{"move bad status", service.MoveInput{Status: "x"}.Validate(), []string{"status"}},
		{"move before+after", service.MoveInput{Status: "todo", Before: "a", After: "b"}.Validate(), []string{"after", "before"}},
		{"move bad place", service.MoveInput{Status: "todo", Place: "middle"}.Validate(), []string{"place"}},
		{"move place top", service.MoveInput{Status: "todo", Place: "top"}.Validate(), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := fieldsOf(t, tt.err)
			var got []string
			for k := range f {
				got = append(got, k)
			}
			sort.Strings(got)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("fields = %v, want %v", got, tt.want)
			}
		})
	}
	if f := fieldsOf(t, service.UpdateProjectInput{Name: service.Null[string]()}.Validate()); f["name"] != "must not be null" {
		t.Fatal(f)
	}
}

func manyIDs(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "id" + string(rune('0'+i/10)) + string(rune('0'+i%10))
	}
	out[0] = "id0"
	return out
}

func TestRefs(t *testing.T) {
	ulid := "01HZX3Q9V2K7M8N4P5R6S7T8W9"
	if !service.IsULID(ulid) || !service.IsULID(strings.ToLower(ulid)) {
		t.Fatal("ulid")
	}
	if service.IsULID(ulid[:25]) || service.IsULID("01HZX3Q9V2K7M8N4P5R6S7T8WU") /* U not Crockford */ {
		t.Fatal("bad ulid accepted")
	}
	id, key, err := service.ParseProjectRef(strings.ToLower(ulid))
	if err != nil || id != ulid || key != "" {
		t.Fatal(id, key, err)
	}
	id, key, err = service.ParseProjectRef(" web ")
	if err != nil || id != "" || key != "WEB" {
		t.Fatal(id, key, err)
	}
	for _, bad := range []string{"", "W", "TOOLONGKEY", "WEB-1", "we b", "12", "WEB!"} {
		if _, _, err := service.ParseProjectRef(bad); !errors.Is(err, service.ErrNotFound) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	tid, k, n, err := service.ParseTicketRef("web-12")
	if err != nil || tid != "" || k != "WEB" || n != 12 {
		t.Fatal(tid, k, n, err)
	}
	tid, _, _, err = service.ParseTicketRef(strings.ToLower(ulid))
	if err != nil || tid != ulid {
		t.Fatal(tid, err)
	}
	for _, bad := range []string{"", "WEB-0", "WEB-012", "WEB-", "WEB12", "W-1", "WEB-1x", "WEB-99999999999999999999", "-5"} {
		if _, _, _, err := service.ParseTicketRef(bad); !errors.Is(err, service.ErrNotFound) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if service.FormatRef("WEB", 12) != "WEB-12" {
		t.Fatal("FormatRef")
	}
}

func TestCursor(t *testing.T) {
	c := service.EncodeCursor(1, 1.0/3.0, "01H")
	keys, err := service.DecodeCursor(c, 3)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := keys.Int(0)
	f, _ := keys.Float(1)
	s, _ := keys.Str(2)
	if r != 1 || f != 1.0/3.0 || s != "01H" {
		t.Fatal(r, f, s)
	}
	for _, v := range []float64{1.0 / 3.0, 1e-7, -1024, 0.1 + 0.2, math.MaxFloat64, 5e-324} {
		k, err := service.DecodeCursor(service.EncodeCursor(v), 1)
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := k.Float(0); got != v {
			t.Errorf("float %v round-tripped to %v", v, got)
		}
	}
	bad := []string{"", "!!!", "e30", // {}
		service.EncodeCursor("a") + "x"}
	for _, b := range bad {
		if _, err := service.DecodeCursor(b, 1); !isInvalidCursor(err) {
			t.Errorf("%q: %v", b, err)
		}
	}
	if _, err := service.DecodeCursor(service.EncodeCursor("a", "b"), 3); !isInvalidCursor(err) {
		t.Error("wrong shape accepted")
	}
	wrongVersion := "eyJ2IjoyLCJrIjpbImEiXX0" // {"v":2,"k":["a"]}
	if _, err := service.DecodeCursor(wrongVersion, 1); !isInvalidCursor(err) {
		t.Error("wrong version accepted")
	}
	k, _ := service.DecodeCursor(service.EncodeCursor("a"), 1)
	if _, err := k.Float(0); !isInvalidCursor(err) {
		t.Error("type mismatch accepted")
	}
}

func isInvalidCursor(err error) bool {
	var se *service.Error
	return errors.As(err, &se) && se.Kind == service.KindBadRequest && se.Code == "invalid_cursor"
}

func TestPosition(t *testing.T) {
	if service.Bottom(nil) != service.Gap || service.Top(nil) != service.Gap {
		t.Fatal("empty column")
	}
	if service.Bottom([]float64{5, 1, 3}) != 5+service.Gap || service.Top([]float64{5, 1, 3}) != 1-service.Gap {
		t.Fatal("bottom/top")
	}
	if p, r := service.PlanMove(nil, 0); p != service.Gap || r {
		t.Fatal("empty plan")
	}
	if p, r := service.PlanMove([]float64{1024}, 0); p != 0 || r {
		t.Fatal("single top", p)
	}
	if p, r := service.PlanMove([]float64{1024}, 1); p != 2048 || r {
		t.Fatal("single bottom", p)
	}
	if p, r := service.PlanMove([]float64{1024, 2048}, 1); p != 1536 || r {
		t.Fatal("between", p)
	}
	// repeated top inserts go negative
	col := []float64{1024}
	for i := 0; i < 5; i++ {
		p, _ := service.PlanMove(col, 0)
		col = append([]float64{p}, col...)
	}
	if col[0] != 1024-5*1024 {
		t.Fatal("negative positions", col)
	}
	// gap below MinGap and equal positions need a renumber
	if _, r := service.PlanMove([]float64{1, 1 + 1e-7}, 1); !r {
		t.Fatal("tiny gap should renumber")
	}
	if _, r := service.PlanMove([]float64{7, 7}, 1); !r {
		t.Fatal("equal positions should renumber")
	}
	if _, r := service.PlanMove([]float64{9, 7}, 1); !r {
		t.Fatal("inverted positions should renumber")
	}
	// exactly MinGap is fine
	if _, r := service.PlanMove([]float64{1, 1 + 2e-6}, 1); r {
		t.Fatal("gap above MinGap should not renumber")
	}
	for i, p := range service.Renumber(4) {
		if p != service.Gap*float64(i+1) {
			t.Fatal("renumber", i, p)
		}
	}
	if len(service.Renumber(0)) != 0 {
		t.Fatal("renumber 0")
	}
}

func TestEventJSON(t *testing.T) {
	a := service.TokenActor("TOK", "USER", service.ScopeRead, "PROJ")
	ea := service.EventActorOf(a)
	if ea.Type != service.ActorAPIToken || ea.ID != "TOK" {
		t.Fatal(ea)
	}
	b, err := json.Marshal(service.Event{Type: service.EventTicketMoved, ProjectID: "P", TicketID: "T", Renumbered: true, Actor: ea})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for k := range m {
		switch k {
		case "type", "project_id", "ticket_id", "renumbered", "actor", "at":
		default:
			t.Errorf("unexpected key %q", k)
		}
	}
	actor := m["actor"].(map[string]any)
	if len(actor) != 2 || actor["type"] != "api_token" || actor["id"] != "TOK" {
		t.Fatalf("actor payload: %v", actor)
	}
	if strings.Contains(string(b), "USER") || strings.Contains(string(b), "PROJ") || strings.Contains(string(b), "read") {
		t.Fatalf("leaked actor detail: %s", b)
	}
	b, _ = json.Marshal(service.Event{Type: service.EventProjectUpdated, ProjectID: "P"})
	if strings.Contains(string(b), "ticket_id") || strings.Contains(string(b), "renumbered") {
		t.Fatalf("omitempty broken: %s", b)
	}
}
