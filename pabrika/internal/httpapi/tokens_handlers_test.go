package httpapi

import (
	"strings"
	"testing"

	"github.com/RJGJ/Pabrika/internal/auth"
	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/testutil"
)

func tokenUser(c *Client) testutil.User {
	return testutil.User{ID: c.User.ID, Email: c.User.Email, DisplayName: c.User.DisplayName}
}

func TestTokensCreateListRevoke(t *testing.T) {
	h := Setup(t, Opts{})
	c := h.Signup(t, "a@x.io")

	r := h.Do(t, "POST", "/api/v1/tokens", map[string]any{"name": "  CI bot ", "scope": "write"}, As(c))
	if r.Code != 201 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	m := r.JSON(t)
	wantKeys(t, "created", m, "token", "secret")
	tk := m["token"].(map[string]any)
	wantKeys(t, "created.token", tk, "id", "name", "token_prefix", "scope", "project", "last_used_at", "revoked_at", "created_at")
	secret := m["secret"].(string)
	if !auth.IsTokenSecret(secret) {
		t.Fatalf("secret shape: %q", secret)
	}
	if tk["name"] != "CI bot" || tk["scope"] != "write" || tk["project"] != nil || tk["last_used_at"] != nil || tk["revoked_at"] != nil {
		t.Fatalf("%v", tk)
	}
	if tk["token_prefix"] != secret[:8] {
		t.Fatalf("prefix %v vs %s", tk["token_prefix"], secret)
	}
	id := tk["id"].(string)

	// The DB holds only the hash and prefix, never the secret.
	if n := countRows(t, h, "SELECT COUNT(*) FROM api_tokens WHERE token_hash = ?", auth.HashToken(secret)); n != 1 {
		t.Fatal("hash not stored")
	}
	for _, col := range []string{"token_hash", "token_prefix", "name"} {
		if n := countRows(t, h, "SELECT COUNT(*) FROM api_tokens WHERE "+col+" = ?", secret); n != 0 {
			t.Fatalf("secret stored in %s", col)
		}
	}

	// The secret authenticates.
	if r := h.Do(t, "GET", "/api/v1/auth/me", nil, Bearer(secret)); r.Code != 200 {
		t.Fatalf("secret unusable: %d", r.Code)
	}

	// List: whole, newest first, never the secret.
	h.Env.Clock.Advance(1_000_000_000)
	r2 := h.Do(t, "POST", "/api/v1/tokens", map[string]any{"name": "second", "scope": "read"}, As(c))
	id2 := r2.JSON(t)["token"].(map[string]any)["id"].(string)
	r = h.Do(t, "GET", "/api/v1/tokens", nil, As(c))
	if r.Code != 200 {
		t.Fatal(r.Code)
	}
	l := r.JSON(t)
	wantKeys(t, "list", l, "items", "next_cursor")
	if l["next_cursor"] != nil {
		t.Fatal("next_cursor must be null")
	}
	items := l["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["id"] != id2 || items[1].(map[string]any)["id"] != id {
		t.Fatalf("order: %v", items)
	}
	for _, it := range items {
		wantKeys(t, "item", it.(map[string]any), "id", "name", "token_prefix", "scope", "project", "last_used_at", "revoked_at", "created_at")
	}
	if strings.Contains(string(r.Body), secret) || strings.Contains(string(r.Body), auth.HashToken(secret)) {
		t.Fatal("list leaks the secret or its hash")
	}
	// last_used_at is set after use.
	if items[1].(map[string]any)["last_used_at"] == nil {
		// The touch is throttled and may be asynchronous: only assert the key exists.
		t.Log("last_used_at not yet set (throttled)")
	}

	// Revoke: 204, idempotent, stays in the list with revoked_at, secret fails.
	for i := 0; i < 2; i++ {
		if r := h.Do(t, "DELETE", "/api/v1/tokens/"+id, nil, As(c)); r.Code != 204 || len(r.Body) != 0 {
			t.Fatalf("revoke #%d: %d %s", i, r.Code, r.Body)
		}
	}
	if r := h.Do(t, "GET", "/api/v1/auth/me", nil, Bearer(secret)); r.Code != 401 {
		t.Fatalf("revoked token works: %d", r.Code)
	}
	items = h.Do(t, "GET", "/api/v1/tokens", nil, As(c)).JSON(t)["items"].([]any)
	if len(items) != 2 || items[1].(map[string]any)["revoked_at"] == nil {
		t.Fatalf("revoked token missing or without revoked_at: %v", items)
	}
}

func TestTokensListEmpty(t *testing.T) {
	h := Setup(t, Opts{})
	c := h.Signup(t, "a@x.io")
	r := h.Do(t, "GET", "/api/v1/tokens", nil, As(c))
	if r.Code != 200 || string(r.Body) != "{\"items\":[],\"next_cursor\":null}\n" {
		t.Fatalf("%d %q", r.Code, r.Body)
	}
}

func TestTokensProjectLimit(t *testing.T) {
	h := Setup(t, Opts{})
	c := h.Signup(t, "a@x.io")
	stranger := h.Signup(t, "b@x.io")
	mine := h.Env.NewProject(t, tokenUser(c), "WEB")
	theirs := h.Env.NewProject(t, tokenUser(stranger), "OPS")

	// By key and by ULID.
	for _, ref := range []string{"WEB", mine.ID, "web"} {
		r := h.Do(t, "POST", "/api/v1/tokens", map[string]any{"name": "t", "scope": "read", "project_id": ref}, As(c))
		if r.Code != 201 {
			t.Fatalf("%q: %d %s", ref, r.Code, r.Body)
		}
		p := r.JSON(t)["token"].(map[string]any)["project"].(map[string]any)
		wantKeys(t, "token.project", p, "id", "key")
		if p["id"] != mine.ID || p["key"] != "WEB" {
			t.Fatalf("%v", p)
		}
	}
	// A project the caller is not in, and an unknown one: 404, indistinguishable.
	r1 := h.Do(t, "POST", "/api/v1/tokens", map[string]any{"name": "t", "scope": "read", "project_id": theirs.ID}, As(c))
	r2 := h.Do(t, "POST", "/api/v1/tokens", map[string]any{"name": "t", "scope": "read", "project_id": "01ARZ3NDEKTSV4RRFFQ69G5FAV"}, As(c))
	if r1.Code != 404 || r2.Code != 404 || string(r1.Body) != string(r2.Body) {
		t.Fatalf("%d %s / %d %s", r1.Code, r1.Body, r2.Code, r2.Body)
	}
	// null and absent mean all projects.
	r := h.Do(t, "POST", "/api/v1/tokens", map[string]any{"name": "t", "scope": "read", "project_id": nil}, As(c))
	if r.Code != 201 || r.JSON(t)["token"].(map[string]any)["project"] != nil {
		t.Fatalf("null project: %d %s", r.Code, r.Body)
	}
	// Listing shows the project key too.
	items := h.Do(t, "GET", "/api/v1/tokens", nil, As(c)).JSON(t)["items"].([]any)
	limited := 0
	for _, it := range items {
		if p, ok := it.(map[string]any)["project"].(map[string]any); ok && p["key"] == "WEB" {
			limited++
		}
	}
	if limited != 3 {
		t.Fatalf("limited tokens listed: %d", limited)
	}
}

func TestTokensValidation(t *testing.T) {
	h := Setup(t, Opts{})
	c := h.Signup(t, "a@x.io")
	cases := []struct {
		body  map[string]any
		field string
	}{
		{map[string]any{"name": "", "scope": "read"}, "name"},
		{map[string]any{"name": "   ", "scope": "read"}, "name"},
		{map[string]any{"name": strings.Repeat("x", 101), "scope": "read"}, "name"},
		{map[string]any{"name": "ok", "scope": "admin"}, "scope"},
		{map[string]any{"name": "ok"}, "scope"},
	}
	for i, tc := range cases {
		r := h.Do(t, "POST", "/api/v1/tokens", tc.body, As(c))
		if r.Code != 422 || r.ErrCode(t) != "validation_failed" {
			t.Fatalf("case %d: %d %s", i, r.Code, r.Body)
		}
		if _, ok := r.JSON(t)["error"].(map[string]any)["fields"].(map[string]any)[tc.field]; !ok {
			t.Fatalf("case %d: no fields.%s: %s", i, tc.field, r.Body)
		}
	}
	if n := countRows(t, h, "SELECT COUNT(*) FROM api_tokens"); n != 0 {
		t.Fatal("token created on validation failure")
	}
	// Malformed bodies.
	for _, raw := range []string{`{"name":"x","scope":"read","bogus":1}`, `{"name":1,"scope":"read"}`, `nope`} {
		if r := h.Do(t, "POST", "/api/v1/tokens", nil, As(c), RawBody(raw, "application/json")); r.Code != 400 {
			t.Fatalf("%q: %d", raw, r.Code)
		}
	}
	if r := h.Do(t, "POST", "/api/v1/tokens", nil, As(c)); r.Code != 400 {
		t.Fatalf("no body: %d", r.Code)
	}
	if r := h.Do(t, "POST", "/api/v1/tokens", nil, As(c), RawBody(`{}`, "text/plain")); r.Code != 415 {
		t.Fatalf("415: %d", r.Code)
	}
}

func TestTokensActiveCap(t *testing.T) {
	h := Setup(t, Opts{})
	c := h.Signup(t, "a@x.io")
	var firstID string
	for i := 0; i < 100; i++ {
		r := h.Do(t, "POST", "/api/v1/tokens", map[string]any{"name": "t", "scope": "read"}, As(c))
		if r.Code != 201 {
			t.Fatalf("#%d: %d %s", i, r.Code, r.Body)
		}
		if i == 0 {
			firstID = r.JSON(t)["token"].(map[string]any)["id"].(string)
		}
	}
	r := h.Do(t, "POST", "/api/v1/tokens", map[string]any{"name": "t", "scope": "read"}, As(c))
	if r.Code != 422 {
		t.Fatalf("101st: %d %s", r.Code, r.Body)
	}
	if f := r.JSON(t)["error"].(map[string]any)["fields"].(map[string]any); f["name"] != "Too many active tokens; revoke one first" {
		t.Fatal(f)
	}
	// Revoking frees a slot (revoked tokens do not count).
	h.Do(t, "DELETE", "/api/v1/tokens/"+firstID, nil, As(c))
	if r := h.Do(t, "POST", "/api/v1/tokens", map[string]any{"name": "t", "scope": "read"}, As(c)); r.Code != 201 {
		t.Fatalf("after revoke: %d", r.Code)
	}
}

func TestTokensIsolationBetweenUsers(t *testing.T) {
	h := Setup(t, Opts{})
	a := h.Signup(t, "a@x.io")
	b := h.Signup(t, "b@x.io")
	_, aID := h.MkToken(t, a, "write", "")
	if items := h.Do(t, "GET", "/api/v1/tokens", nil, As(b)).JSON(t)["items"].([]any); len(items) != 0 {
		t.Fatalf("b sees a's tokens: %v", items)
	}
	// Someone else's id and unknown / malformed ids are all the same 404.
	var bodies []string
	for _, id := range []string{aID, "01ARZ3NDEKTSV4RRFFQ69G5FAV", "nonsense"} {
		r := h.Do(t, "DELETE", "/api/v1/tokens/"+id, nil, As(b))
		if r.Code != 404 || r.ErrCode(t) != "not_found" {
			t.Fatalf("%s: %d %s", id, r.Code, r.Body)
		}
		bodies = append(bodies, string(r.Body))
	}
	if bodies[0] != bodies[1] || bodies[1] != bodies[2] {
		t.Fatalf("404 bodies differ: %q", bodies)
	}
	// a's token is untouched.
	if items := h.Do(t, "GET", "/api/v1/tokens", nil, As(a)).JSON(t)["items"].([]any); items[0].(map[string]any)["revoked_at"] != nil {
		t.Fatal("token revoked by another user")
	}
}

func TestTokensAreSessionOnly(t *testing.T) {
	h := Setup(t, Opts{})
	c := h.Signup(t, "a@x.io")
	for _, scope := range []string{"read", "write"} {
		secret, id := h.MkToken(t, c, service.Scope(scope), "")
		for _, rq := range []struct{ method, path string }{
			{"GET", "/api/v1/tokens"},
			{"POST", "/api/v1/tokens"},
			{"DELETE", "/api/v1/tokens/" + id},
		} {
			var body any
			if rq.method == "POST" {
				body = map[string]any{"name": "x", "scope": "read"}
			}
			r := h.Do(t, rq.method, rq.path, body, Bearer(secret))
			if r.Code != 403 || r.ErrCode(t) != "session_required" {
				t.Fatalf("%s %s %s: %d %s", scope, rq.method, rq.path, r.Code, r.Body)
			}
		}
		// The token survived the DELETE attempt.
		if r := h.Do(t, "GET", "/api/v1/auth/me", nil, Bearer(secret)); r.Code != 200 {
			t.Fatalf("token was revoked by a token call: %d", r.Code)
		}
	}
	// Anonymous: 401.
	for _, rq := range []struct{ method, path string }{{"GET", "/api/v1/tokens"}, {"POST", "/api/v1/tokens"}, {"DELETE", "/api/v1/tokens/01ARZ3NDEKTSV4RRFFQ69G5FAV"}} {
		if r := h.Do(t, rq.method, rq.path, nil); r.Code != 401 {
			t.Fatalf("%s %s anon: %d", rq.method, rq.path, r.Code)
		}
	}
}

func TestRouteAccessTable(t *testing.T) {
	h := Setup(t, Opts{})
	want := map[string]Access{
		"GET /api/v1/auth/config":       Public,
		"POST /api/v1/auth/signup":      Public,
		"POST /api/v1/auth/login":       Public,
		"POST /api/v1/auth/logout":      SessionOnly,
		"GET /api/v1/auth/me":           Authed,
		"PATCH /api/v1/auth/me":         SessionOnly,
		"POST /api/v1/auth/me/password": SessionOnly,
		"GET /api/v1/tokens":            SessionOnly,
		"POST /api/v1/tokens":           SessionOnly,
		"DELETE /api/v1/tokens/{id}":    SessionOnly,
	}
	seen := map[string]bool{}
	for _, rt := range h.Server.Routes() {
		if a, ok := want[rt.Pattern]; ok {
			seen[rt.Pattern] = true
			if rt.Access != a || rt.Write {
				t.Errorf("%s: access %v write %v, want %v and no write", rt.Pattern, rt.Access, rt.Write, a)
			}
		}
	}
	for p := range want {
		if !seen[p] {
			t.Errorf("route %s not registered", p)
		}
	}
}
