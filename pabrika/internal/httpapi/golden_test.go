package httpapi

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/RJGJ/Pabrika/internal/service"
)

// WP8 track 3: golden key sets for every shape of phase 2 spec section 6, checked against REAL
// responses (view_test.go checks the mappers directly). The expected key sets and JSON types
// are literals here, never derived from the view structs, so a renamed, added or dropped field
// fails. The normalised responses are also written to testdata/shapes/*.json for the web app
// to diff its fixtures against; run `go test ./internal/httpapi -run TestGoldenShapes -update`
// after an intended shape change.

var updateShapes = flag.Bool("update", false, "rewrite internal/httpapi/testdata/shapes/*.json")

const shapesDir = "testdata/shapes"

var (
	reULID   = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)
	reTime   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)
	reDate   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	reRef    = regexp.MustCompile(`^[A-Z]{2,6}-\d+$`)
	reSecret = regexp.MustCompile(`^pb_[0-9a-f]{64}$`)
	rePrefix = regexp.MustCompile(`^pb_[0-9a-f]{5}$`)
	reEmail  = regexp.MustCompile(`^[^@\s]+@[^@\s]+$`)
)

// spec maps a key to its exact JSON kind. string kinds carry a format: ulid, time, date, ref,
// secret, prefix, email; plain: string, number, int, bool, array, object, null.
type spec map[string]string

func with(base spec, kv ...string) spec {
	out := spec{}
	for k, v := range base {
		out[k] = v
	}
	for i := 0; i < len(kv); i += 2 {
		if kv[i+1] == "-" {
			delete(out, kv[i])
		} else {
			out[kv[i]] = kv[i+1]
		}
	}
	return out
}

func kindOf(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case float64:
		if x == float64(int64(x)) {
			return "int" // also a valid "number"
		}
		return "number"
	case bool:
		return "bool"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "?"
}

func kindMatches(want string, v any) bool {
	got := kindOf(v)
	s, isStr := v.(string)
	switch want {
	case "number":
		return got == "int" || got == "number"
	case "int", "bool", "array", "object", "null", "string":
		return got == want
	case "ulid":
		return isStr && reULID.MatchString(s)
	case "time":
		return isStr && reTime.MatchString(s)
	case "date":
		return isStr && reDate.MatchString(s)
	case "ref":
		return isStr && reRef.MatchString(s)
	case "secret":
		return isStr && reSecret.MatchString(s)
	case "prefix":
		return isStr && rePrefix.MatchString(s)
	case "email":
		return isStr && reEmail.MatchString(s)
	}
	panic("unknown kind " + want)
}

// check asserts the exact key set and the kind of every value of m.
func check(t *testing.T, name string, m map[string]any, want spec) {
	t.Helper()
	var wk, gk []string
	for k := range want {
		wk = append(wk, k)
	}
	for k := range m {
		gk = append(gk, k)
	}
	sort.Strings(wk)
	sort.Strings(gk)
	if strings.Join(wk, ",") != strings.Join(gk, ",") {
		t.Errorf("%s keys:\n got  %v\n want %v", name, gk, wk)
	}
	for _, k := range wk {
		v, ok := m[k]
		if !ok {
			continue
		}
		if !kindMatches(want[k], v) {
			t.Errorf("%s.%s = %#v, want %s", name, k, v, want[k])
		}
	}
}

func obj(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("not an object: %#v", v)
	}
	return m
}

// ---- literal expectations (spec section 6) ----

var (
	specUser     = spec{"id": "ulid", "email": "email", "display_name": "string"}
	specAuthUser = with(specUser, "created_at", "time")
	specProject  = spec{"id": "ulid", "key": "string", "name": "string", "description": "string",
		"archived_at": "null", "created_at": "time", "updated_at": "time", "role": "string"}
	specCounts   = spec{"backlog": "int", "todo": "int", "in_progress": "int", "done": "int"}
	specMember   = spec{"user": "object", "role": "string", "created_at": "time"}
	specLabel    = spec{"id": "ulid", "project_id": "ulid", "name": "string", "color": "string"}
	specAssignee = spec{"id": "ulid", "display_name": "string", "email": "email"}
	specTicket   = spec{"id": "ulid", "ref": "ref", "project_id": "ulid", "project_key": "string", "number": "int",
		"title": "string", "description": "string", "status": "string", "priority": "string",
		"assignee": "null", "labels": "array", "position": "number", "due_date": "null",
		"comment_count": "int", "created_at": "time", "updated_at": "time"}
	specTicketItem = with(specTicket, "description", "-")
	specAuthorUser = spec{"type": "string", "id": "ulid", "name": "string", "bot": "bool"}
	specAuthorBot  = with(specAuthorUser, "owner_name", "string")
	specComment    = spec{"id": "ulid", "ticket_id": "ulid", "author": "object", "body": "string",
		"created_at": "time", "edited_at": "null"}
	specActivity = spec{"id": "ulid", "ticket_id": "ulid", "actor": "object", "action": "string",
		"changes": "object", "created_at": "time"}
	specToken = spec{"id": "ulid", "name": "string", "token_prefix": "prefix", "scope": "string",
		"project": "null", "last_used_at": "null", "revoked_at": "null", "created_at": "time"}
	specMeToken = spec{"id": "ulid", "name": "string", "scope": "string", "project_id": "null", "project_key": "null"}
)

// ---- normalisation of the fixtures ----

func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = normalize(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalize(e)
		}
		return out
	case string:
		switch {
		case reULID.MatchString(x):
			return "01JTESTTESTTESTTESTTESTTES"
		case reTime.MatchString(x):
			return "2026-01-01T00:00:00.000Z"
		case reSecret.MatchString(x):
			return "pb_" + strings.Repeat("0", 64)
		case rePrefix.MatchString(x):
			return "pb_00000"
		}
	}
	return v
}

// fixtures collects the samples in the order they are produced.
type fixtures struct {
	names []string
	data  map[string]any
}

func (f *fixtures) add(name string, v any) {
	if f.data == nil {
		f.data = map[string]any{}
	}
	if _, dup := f.data[name]; dup {
		panic("duplicate fixture " + name)
	}
	f.names = append(f.names, name)
	f.data[name] = v
}

func (f *fixtures) sync(t *testing.T) {
	t.Helper()
	want := map[string]bool{}
	for _, name := range f.names {
		file := filepath.Join(shapesDir, name+".json")
		want[file] = true
		b, err := json.MarshalIndent(normalize(f.data[name]), "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		b = append(b, '\n')
		if *updateShapes {
			if err := os.MkdirAll(shapesDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, b, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(file)
		if err != nil {
			t.Errorf("fixture %s missing (run with -update): %v", file, err)
			continue
		}
		if !bytes.Equal(bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n")), b) {
			t.Errorf("fixture %s is out of date with the real response (run with -update):\n--- file\n%s\n--- response\n%s", file, got, b)
		}
	}
	entries, _ := os.ReadDir(shapesDir)
	for _, e := range entries {
		file := filepath.Join(shapesDir, e.Name())
		if !want[file] {
			if *updateShapes {
				_ = os.Remove(file)
			} else {
				t.Errorf("stale fixture %s has no sample", file)
			}
		}
	}
}

// ---- the test ----

func TestGoldenShapes(t *testing.T) {
	h := Setup(t, Opts{})
	alice := h.Signup(t, "alice@example.com")
	bob := h.Signup(t, "bob@example.com")
	var fx fixtures

	do := func(want int, method, path string, body any, opts ...ReqOpt) *Resp {
		t.Helper()
		r := h.Do(t, method, path, body, opts...)
		if r.Code != want {
			t.Fatalf("%s %s: %d %s (want %d)", method, path, r.Code, r.Body, want)
		}
		return r
	}
	items := func(r *Resp) []any {
		t.Helper()
		v, ok := r.JSON(t)["items"].([]any)
		if !ok {
			t.Fatalf("no items: %s", r.Body)
		}
		return v
	}

	// ---- public ----
	cfg := do(200, "GET", "/api/v1/auth/config", nil).JSON(t)
	check(t, "auth_config", cfg, spec{"signup_enabled": "bool"})
	fx.add("auth_config", cfg)

	// ---- auth_user: signup, login, PATCH /auth/me ----
	anon := h.NewClient()
	su := do(201, "POST", "/api/v1/auth/signup", map[string]any{"email": "carol@example.com", "display_name": "Carol", "password": TestPassword}, As(anon)).JSON(t)
	check(t, "signup response", su, spec{"user": "object"})
	check(t, "signup.user", obj(t, su["user"]), specAuthUser)
	fx.add("auth_user", su["user"])
	li := do(200, "POST", "/api/v1/auth/login", map[string]any{"email": "carol@example.com", "password": TestPassword}, As(h.NewClient())).JSON(t)
	check(t, "login response", li, spec{"user": "object"})
	check(t, "login.user", obj(t, li["user"]), specAuthUser)
	pm := do(200, "PATCH", "/api/v1/auth/me", map[string]any{"display_name": "Carol C"}, As(anon)).JSON(t)
	check(t, "PATCH /auth/me response", pm, spec{"user": "object"})
	check(t, "PATCH /auth/me.user", obj(t, pm["user"]), specAuthUser)

	// ---- projects ----
	created := do(201, "POST", "/api/v1/projects", map[string]any{"key": "web", "name": "Web", "description": "The site"}, As(alice)).JSON(t)
	check(t, "project (create)", created, with(specProject, "role", "string"))
	if created["role"] != "owner" || created["key"] != "WEB" || created["archived_at"] != nil {
		t.Errorf("created project: %v", created)
	}
	fx.add("project", created)
	do(201, "POST", "/api/v1/projects/WEB/members", map[string]any{"email": "bob@example.com", "role": "editor"}, As(alice))

	list := do(200, "GET", "/api/v1/projects", nil, As(alice))
	check(t, "project list envelope", list.JSON(t), spec{"items": "array", "next_cursor": "null"})
	check(t, "project (list item)", obj(t, items(list)[0]), specProject)

	// ---- labels ----
	lab := do(201, "POST", "/api/v1/projects/WEB/labels", map[string]any{"name": "bug", "color": "red"}, As(alice)).JSON(t)
	check(t, "label", lab, specLabel)
	fx.add("label", lab)
	labList := do(200, "GET", "/api/v1/projects/WEB/labels", nil, As(alice))
	check(t, "label list item", obj(t, items(labList)[0]), specLabel)

	// ---- tickets: full with every nullable filled, full with nulls, list items ----
	nulls := do(201, "POST", "/api/v1/projects/WEB/tickets", map[string]any{"title": "Plain"}, As(alice)).JSON(t)
	check(t, "ticket (nulls, create)", nulls, specTicket)
	for _, k := range []string{"assignee", "due_date"} {
		if v, ok := nulls[k]; !ok || v != nil {
			t.Errorf("ticket.%s must be present as null, got %v (present=%v)", k, v, ok)
		}
	}
	if labs, ok := nulls["labels"].([]any); !ok || len(labs) != 0 {
		t.Errorf("ticket.labels must be [] when none: %v", nulls["labels"])
	}
	fx.add("ticket_nulls", nulls)
	fullSpec := with(specTicket, "assignee", "object", "due_date", "date")
	full := do(201, "POST", "/api/v1/projects/WEB/tickets", map[string]any{
		"title": "Full", "description": "Body **md**", "status": "in_progress", "priority": "high",
		"due_date": "2026-12-31", "assignee": bob.User.ID, "labels": []string{lab["id"].(string)},
	}, As(alice)).JSON(t)
	check(t, "ticket (full, create)", full, fullSpec)
	check(t, "ticket.assignee", obj(t, full["assignee"]), specAssignee)
	labs := full["labels"].([]any)
	if len(labs) != 1 {
		t.Fatalf("labels: %v", labs)
	}
	check(t, "ticket.labels[0]", obj(t, labs[0]), specLabel)
	if full["ref"] != "WEB-2" {
		t.Errorf("ref = %v", full["ref"])
	}
	fx.add("ticket", full)
	for name, r := range map[string]*Resp{
		"GET":   do(200, "GET", "/api/v1/tickets/WEB-2", nil, As(alice)),
		"PATCH": do(200, "PATCH", "/api/v1/tickets/WEB-2", map[string]any{"priority": "low"}, As(alice)),
	} {
		check(t, "ticket ("+name+")", r.JSON(t), with(fullSpec, "comment_count", "int"))
	}
	if _, has := do(200, "GET", "/api/v1/tickets/WEB-2", nil, As(alice)).JSON(t)["renumbered"]; has {
		t.Error("renumbered must be omitted unless the request was a move")
	}
	tl := do(200, "GET", "/api/v1/projects/WEB/tickets", nil, As(alice))
	var itemFull, itemNulls map[string]any
	for _, it := range items(tl) {
		m := obj(t, it)
		if m["ref"] == "WEB-2" {
			itemFull = m
		} else {
			itemNulls = m
		}
	}
	check(t, "ticket list item (full)", itemFull, with(specTicketItem, "assignee", "object", "due_date", "date"))
	check(t, "ticket list item (nulls)", itemNulls, specTicketItem)
	if _, has := itemFull["description"]; has {
		t.Error("description must be absent from list items")
	}
	check(t, "ticket list item assignee", obj(t, itemFull["assignee"]), specAssignee)
	fx.add("ticket_list_item", itemFull)
	fx.add("ticket_list_item_nulls", itemNulls)

	mv := do(200, "POST", "/api/v1/tickets/WEB-1/move", map[string]any{"status": "done", "place": "top"}, As(alice)).JSON(t)
	check(t, "ticket move response", mv, with(specTicket, "renumbered", "bool"))
	fx.add("ticket_move", mv)

	// ---- list envelope with a cursor ----
	page := do(200, "GET", "/api/v1/projects/WEB/tickets?limit=1", nil, As(alice))
	check(t, "ticket page 1", page.JSON(t), spec{"items": "array", "next_cursor": "string"})
	fx.add("list_envelope", page.JSON(t))
	last := do(200, "GET", "/api/v1/projects/WEB/tickets?limit=200", nil, As(alice))
	check(t, "ticket last page", last.JSON(t), spec{"items": "array", "next_cursor": "null"})
	fx.add("list_envelope_last", last.JSON(t))

	// ---- project_detail and the read-token role ----
	det := do(200, "GET", "/api/v1/projects/WEB", nil, As(alice)).JSON(t)
	check(t, "project_detail", det, with(specProject, "counts", "object"))
	check(t, "project_detail.counts", obj(t, det["counts"]), specCounts)
	counts := obj(t, det["counts"])
	if counts["backlog"] != float64(0) || counts["todo"] != float64(0) || counts["in_progress"] != float64(1) || counts["done"] != float64(1) {
		t.Errorf("counts = %v", counts)
	}
	fx.add("project_detail", det)
	readSecret, _ := h.MkToken(t, alice, service.ScopeRead, "")
	rp := do(200, "GET", "/api/v1/projects/WEB", nil, Bearer(readSecret)).JSON(t)
	check(t, "project_detail (read token)", rp, with(specProject, "counts", "object"))
	if rp["role"] != "viewer" {
		t.Errorf("project.role for a read token whose owner is an owner = %v, want viewer", rp["role"])
	}
	fx.add("project_read_token", rp)
	rl := do(200, "GET", "/api/v1/projects", nil, Bearer(readSecret))
	if r := obj(t, items(rl)[0])["role"]; r != "viewer" {
		t.Errorf("project list role for a read token = %v", r)
	}
	do(200, "PATCH", "/api/v1/projects/WEB", map[string]any{"archived": true}, As(alice))
	arch := do(200, "GET", "/api/v1/projects/WEB", nil, As(alice)).JSON(t)
	check(t, "project (archived)", arch, with(specProject, "counts", "object", "archived_at", "time"))
	fx.add("project_archived", arch)
	do(200, "PATCH", "/api/v1/projects/WEB", map[string]any{"archived": false}, As(alice))

	// ---- members ----
	ml := do(200, "GET", "/api/v1/projects/WEB/members", nil, As(alice))
	check(t, "member list envelope", ml.JSON(t), spec{"items": "array", "next_cursor": "null"})
	mem := obj(t, items(ml)[0])
	check(t, "member", mem, specMember)
	check(t, "member.user", obj(t, mem["user"]), specUser)
	fx.add("member", mem)
	fx.add("user", mem["user"])

	// ---- comments: by a user (no owner_name) and by a token (owner_name), edited ----
	cu := do(201, "POST", "/api/v1/tickets/WEB-2/comments", map[string]any{"body": "from a human"}, As(alice)).JSON(t)
	check(t, "comment (user)", cu, specComment)
	check(t, "comment.author (user)", obj(t, cu["author"]), specAuthorUser)
	if a := obj(t, cu["author"]); a["type"] != "user" || a["bot"] != false {
		t.Errorf("user author: %v", a)
	}
	if _, has := obj(t, cu["author"])["owner_name"]; has {
		t.Error("owner_name must be absent for user authors")
	}
	fx.add("comment_user", cu)
	writeSecret, _ := h.MkToken(t, alice, service.ScopeWrite, "")
	cb := do(201, "POST", "/api/v1/tickets/WEB-2/comments", map[string]any{"body": "from an agent"}, Bearer(writeSecret)).JSON(t)
	check(t, "comment (token)", cb, specComment)
	check(t, "comment.author (token)", obj(t, cb["author"]), specAuthorBot)
	if a := obj(t, cb["author"]); a["type"] != "api_token" || a["bot"] != true || a["owner_name"] != "Alice" {
		t.Errorf("token author: %v", a)
	}
	fx.add("comment_token", cb)
	ce := do(200, "PATCH", "/api/v1/comments/"+cu["id"].(string), map[string]any{"body": "edited"}, As(alice)).JSON(t)
	check(t, "comment (edited)", ce, with(specComment, "edited_at", "time"))
	fx.add("comment_edited", ce)
	cl := do(200, "GET", "/api/v1/tickets/WEB-2/comments", nil, As(alice))
	check(t, "comment list envelope", cl.JSON(t), spec{"items": "array", "next_cursor": "null"})
	check(t, "comment list item", obj(t, items(cl)[0]), with(specComment, "edited_at", "time"))
	if got := do(200, "GET", "/api/v1/tickets/WEB-2", nil, As(alice)).JSON(t)["comment_count"]; got != float64(2) {
		t.Errorf("comment_count = %v", got)
	}

	// ---- activity: a user row and a token row ----
	do(200, "PATCH", "/api/v1/tickets/WEB-2", map[string]any{"title": "Renamed by agent"}, Bearer(writeSecret))
	al := do(200, "GET", "/api/v1/tickets/WEB-2/activity", nil, As(alice))
	check(t, "activity list envelope", al.JSON(t), spec{"items": "array", "next_cursor": "null"})
	var actUser, actBot map[string]any
	for _, it := range items(al) {
		m := obj(t, it)
		switch obj(t, m["actor"])["type"] {
		case "user":
			if actUser == nil {
				actUser = m
			}
		case "api_token":
			if actBot == nil {
				actBot = m
			}
		}
	}
	if actUser == nil || actBot == nil {
		t.Fatalf("need a user and a token activity row: %s", al.Body)
	}
	check(t, "activity (user)", actUser, specActivity)
	check(t, "activity.actor (user)", obj(t, actUser["actor"]), specAuthorUser)
	check(t, "activity (token)", actBot, specActivity)
	check(t, "activity.actor (token)", obj(t, actBot["actor"]), specAuthorBot)
	for _, a := range []map[string]any{actUser, actBot} {
		for field, pair := range obj(t, a["changes"]) {
			if p, ok := pair.([]any); !ok || len(p) != 2 {
				t.Errorf("changes[%s] must be a [old, new] pair, got %v", field, pair)
			}
		}
	}
	fx.add("activity_user", actUser)
	fx.add("activity_token", actBot)

	// ---- tokens ----
	tc := do(201, "POST", "/api/v1/tokens", map[string]any{"name": "ci", "scope": "write"}, As(alice)).JSON(t)
	check(t, "token creation response", tc, spec{"token": "object", "secret": "secret"})
	check(t, "token (created)", obj(t, tc["token"]), specToken)
	fx.add("token_created", tc)
	fx.add("token", tc["token"])
	lt := do(201, "POST", "/api/v1/tokens", map[string]any{"name": "web only", "scope": "read", "project_id": "WEB"}, As(alice)).JSON(t)
	limSecret := lt["secret"].(string)
	do(200, "GET", "/api/v1/projects", nil, Bearer(limSecret)) // sets last_used_at
	rv := do(201, "POST", "/api/v1/tokens", map[string]any{"name": "old", "scope": "read"}, As(alice)).JSON(t)
	do(204, "DELETE", "/api/v1/tokens/"+obj(t, rv["token"])["id"].(string), nil, As(alice))
	tlist := do(200, "GET", "/api/v1/tokens", nil, As(alice))
	check(t, "token list envelope", tlist.JSON(t), spec{"items": "array", "next_cursor": "null"})
	var tLim, tRev map[string]any
	for _, it := range items(tlist) {
		switch m := obj(t, it); m["name"] {
		case "web only":
			tLim = m
		case "old":
			tRev = m
		}
	}
	check(t, "token (limited, used)", tLim, with(specToken, "project", "object", "last_used_at", "time", "scope", "string"))
	check(t, "token.project", obj(t, tLim["project"]), spec{"id": "ulid", "key": "string"})
	if tLim["scope"] != "read" || obj(t, tLim["project"])["key"] != "WEB" {
		t.Errorf("limited token: %v", tLim)
	}
	check(t, "token (revoked)", tRev, with(specToken, "revoked_at", "time"))
	fx.add("token_limited", tLim)
	fx.add("token_revoked", tRev)
	for _, it := range items(tlist) {
		if _, has := obj(t, it)["secret"]; has {
			t.Error("the token list must never carry a secret")
		}
	}

	// ---- /auth/me ----
	ms := do(200, "GET", "/api/v1/auth/me", nil, As(alice)).JSON(t)
	check(t, "me (session)", ms, spec{"user": "object", "auth": "object"})
	check(t, "me.user", obj(t, ms["user"]), specAuthUser)
	check(t, "me.auth (session)", obj(t, ms["auth"]), spec{"method": "string"})
	fx.add("me_session", ms)
	mt := do(200, "GET", "/api/v1/auth/me", nil, Bearer(limSecret)).JSON(t)
	check(t, "me (token)", mt, spec{"user": "object", "auth": "object"})
	check(t, "me.auth (token)", obj(t, mt["auth"]), spec{"method": "string", "token": "object"})
	check(t, "me.auth.token (limited)", obj(t, obj(t, mt["auth"])["token"]), with(specMeToken, "project_id", "ulid", "project_key", "string"))
	fx.add("me_token", mt)
	mu := do(200, "GET", "/api/v1/auth/me", nil, Bearer(writeSecret)).JSON(t)
	check(t, "me.auth.token (unlimited)", obj(t, obj(t, mu["auth"])["token"]), specMeToken)
	fx.add("me_token_unlimited", mu)

	// ---- errors ----
	nf := do(404, "GET", "/api/v1/projects/NOPE", nil, As(alice)).JSON(t)
	check(t, "error", nf, spec{"error": "object"})
	check(t, "error.error", obj(t, nf["error"]), spec{"code": "string", "message": "string"})
	fx.add("error", nf)
	ve := do(422, "POST", "/api/v1/projects/WEB/tickets", map[string]any{"title": ""}, As(alice)).JSON(t)
	check(t, "error (422)", obj(t, ve["error"]), spec{"code": "string", "message": "string", "fields": "object"})
	for k, v := range obj(t, obj(t, ve["error"])["fields"]) {
		if _, ok := v.(string); !ok {
			t.Errorf("fields.%s must be a string, got %#v", k, v)
		}
	}
	fx.add("error_validation", ve)

	fx.sync(t)
}
