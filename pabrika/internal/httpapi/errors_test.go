package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/RJGJ/Pabrika/internal/service"
)

func TestMapError(t *testing.T) {
	val := service.Validation(map[string]string{"title": "must be 200 characters or fewer"})
	userNotFound := &service.Error{Kind: service.KindValidation, Code: service.CodeUserNotFound, Message: "No account with this email",
		Fields: map[string]string{"email": "No account with this email"}}
	anchor := service.Validation(map[string]string{"before": "must reference another live ticket in the target column"})
	anchor.Code = service.CodeAnchorInvalid

	tests := []struct {
		name   string
		err    error
		status int
		code   string
		fields map[string]string
	}{
		{"validation", val, 422, "validation_failed", map[string]string{"title": "must be 200 characters or fewer"}},
		{"user_not_found is validation_failed on email", userNotFound, 422, "validation_failed", map[string]string{"email": "No account with this email"}},
		{"anchor_invalid keeps its field", anchor, 422, "validation_failed", map[string]string{"before": "must reference another live ticket in the target column"}},
		{"bad request cursor", service.NewError(service.KindBadRequest, service.CodeInvalidCursor, "Invalid cursor"), 400, "invalid_cursor", nil},
		{"not found", service.NewError(service.KindNotFound, service.CodeNotFound, "Not found"), 404, "not_found", nil},
		{"forbidden", service.NewError(service.KindForbidden, service.CodeForbidden, "no"), 403, "forbidden", nil},
		{"not_author goes out as forbidden", service.NewError(service.KindForbidden, service.CodeNotAuthor, "no"), 403, "forbidden", nil},
		{"insufficient_scope passes through", service.NewError(service.KindForbidden, service.CodeInsufficientScope, "no"), 403, "insufficient_scope", nil},
		{"session_required passes through", service.NewError(service.KindForbidden, service.CodeSessionRequired, "no"), 403, "session_required", nil},
		{"conflict code", service.NewError(service.KindConflict, service.CodeProjectArchived, "archived"), 409, "project_archived", nil},
		{"email_taken", service.NewError(service.KindConflict, "email_taken", "taken"), 409, "email_taken", nil},
		{"wrapped", fmt.Errorf("ctx: %w", service.NewError(service.KindConflict, service.CodeLastOwner, "last")), 409, "last_owner", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, ok := mapError(tc.err)
			if !ok || e.Status != tc.status || e.Code != tc.code {
				t.Fatalf("got %+v ok=%v", e, ok)
			}
			if len(e.Fields) != len(tc.fields) {
				t.Fatalf("fields %v want %v", e.Fields, tc.fields)
			}
			for k, v := range tc.fields {
				if e.Fields[k] != v {
					t.Fatalf("fields %v want %v", e.Fields, tc.fields)
				}
			}
		})
	}

	// Anything else is a generic 500 that never echoes the cause.
	e, ok := mapError(errors.New("sqlite: disk I/O error at /secret/path"))
	if ok || e.Status != 500 || e.Code != "internal" || strings.Contains(e.Message, "sqlite") {
		t.Fatalf("%+v ok=%v", e, ok)
	}
}

func TestErrorShape(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, 404, "not_found", "Project not found")
	if rec.Code != 404 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	var m map[string]map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 || len(m["error"]) != 2 || m["error"]["code"] != "not_found" || m["error"]["message"] != "Project not found" {
		t.Fatalf("%v", m)
	}

	rec = httptest.NewRecorder()
	writeValidation(rec, map[string]string{"title": "bad", "name": "worse"})
	var v struct {
		Error struct {
			Code, Message string
			Fields        map[string]string
		}
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if rec.Code != 422 || v.Error.Code != "validation_failed" || v.Error.Fields["title"] != "bad" || v.Error.Message != "worse" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func decodeReq(t *testing.T, body string, ct string, opts ...decodeOpts) (ok bool, rec *httptest.ResponseRecorder, dst struct {
	Name field[string]  `json:"name"`
	Due  field[*string] `json:"due"`
}) {
	t.Helper()
	req := httptest.NewRequest("POST", "/x", strings.NewReader(body))
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	rec = httptest.NewRecorder()
	ok = decodeJSON(rec, req, &dst, opts...)
	return
}

func TestDecodeJSON(t *testing.T) {
	tests := []struct {
		name string
		body string
		ct   string
		code int // 0 = success
		errc string
	}{
		{"ok", `{"name":"a"}`, "application/json", 0, ""},
		{"charset param allowed", `{"name":"a"}`, "application/json; charset=utf-8", 0, ""},
		{"upper case media type", `{"name":"a"}`, "Application/JSON", 0, ""},
		{"no content type", `{"name":"a"}`, "", 415, "unsupported_media_type"},
		{"form content type", `name=a`, "application/x-www-form-urlencoded", 415, "unsupported_media_type"},
		{"text/plain", `{"name":"a"}`, "text/plain", 415, "unsupported_media_type"},
		{"json-ish type", `{"name":"a"}`, "application/jsonp", 415, "unsupported_media_type"},
		{"empty body", ``, "application/json", 400, "bad_request"},
		{"whitespace body", "  \n", "application/json", 400, "bad_request"},
		{"malformed", `{"name":`, "application/json", 400, "bad_request"},
		{"array", `[1]`, "application/json", 400, "bad_request"},
		{"string", `"x"`, "application/json", 400, "bad_request"},
		{"null", `null`, "application/json", 400, "bad_request"},
		{"trailing object", `{"name":"a"}{"name":"b"}`, "application/json", 400, "bad_request"},
		{"trailing junk", `{"name":"a"} x`, "application/json", 400, "bad_request"},
		{"unknown field", `{"nom":"a"}`, "application/json", 400, "bad_request"},
		{"wrong type", `{"name":5}`, "application/json", 400, "bad_request"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, rec, _ := decodeReq(t, tc.body, tc.ct)
			if tc.code == 0 {
				if !ok {
					t.Fatalf("expected ok, got %d %s", rec.Code, rec.Body)
				}
				return
			}
			var e struct{ Error struct{ Code string } }
			_ = json.Unmarshal(rec.Body.Bytes(), &e)
			if ok || rec.Code != tc.code || e.Error.Code != tc.errc {
				t.Fatalf("got ok=%v %d %s", ok, rec.Code, rec.Body)
			}
		})
	}
}

func TestDecodeJSONOptionsAndFieldStates(t *testing.T) {
	// No body is fine when optional.
	req := httptest.NewRequest("POST", "/x", nil)
	var dst struct{}
	if !decodeJSON(httptest.NewRecorder(), req, &dst, decodeOpts{Optional: true}) {
		t.Fatal("optional body")
	}
	// Hints replace the unknown-field message.
	ok, rec, _ := decodeReq(t, `{"status":"done"}`, "application/json", decodeOpts{Hints: map[string]string{"status": "status cannot be changed here; use /move"}})
	if ok || !strings.Contains(rec.Body.String(), "use /move") || rec.Code != 400 {
		t.Fatalf("hint: %d %s", rec.Code, rec.Body)
	}

	// field[T]: absent, null, value.
	_, _, d := decodeReq(t, `{}`, "application/json")
	if d.Name.Set || d.Due.Set {
		t.Fatalf("absent: %+v", d)
	}
	_, _, d = decodeReq(t, `{"name":"x","due":null}`, "application/json")
	if !d.Name.Set || d.Name.Null || d.Name.Value != "x" || !d.Due.Set || !d.Due.Null {
		t.Fatalf("value/null: %+v", d)
	}
	o := d.Due.opt()
	if !o.Set || !o.Null {
		t.Fatalf("opt: %+v", o)
	}
	_, _, d = decodeReq(t, `{"due":"2026-01-02"}`, "application/json")
	if !d.Due.Set || d.Due.Null || d.Due.Value == nil || *d.Due.Value != "2026-01-02" {
		t.Fatalf("pointer value: %+v", d)
	}
}

func TestDecodeJSONBodyTooLarge(t *testing.T) {
	// Through the full chain (the cap is middleware): over the cap with and without a length.
	h := Setup(t, Opts{})
	h.Server.route("POST /api/v1/_t/dec", Public, false, func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			A string `json:"a"`
		}
		if decodeJSON(w, r, &in) {
			noContent(w)
		}
	})
	big := `{"a":"` + strings.Repeat("x", MaxBodyBytes) + `"}`
	if r := h.Do(t, "POST", "/api/v1/_t/dec", nil, RawBody(big, "application/json")); r.Code != 400 || r.ErrCode(t) != "body_too_large" {
		t.Fatalf("known length: %d %s", r.Code, r.Body)
	}
	// Unknown length (chunked): the reader hits the MaxBytesReader limit.
	req := httptest.NewRequest("POST", "/api/v1/_t/dec", strings.NewReader(big))
	req.ContentLength = -1
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", h.Cfg.BaseURL)
	rec := httptest.NewRecorder()
	h.Server.Handler().ServeHTTP(rec, req)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "body_too_large") {
		t.Fatalf("chunked: %d %s", rec.Code, rec.Body)
	}
	// Exactly at the cap is fine.
	ok := `{"a":"` + strings.Repeat("x", MaxBodyBytes-len(`{"a":""}`)) + `"}`
	if len(ok) != MaxBodyBytes {
		t.Fatal(len(ok))
	}
	if r := h.Do(t, "POST", "/api/v1/_t/dec", nil, RawBody(ok, "application/json")); r.Code != 204 {
		t.Fatalf("at cap: %d %s", r.Code, r.Body)
	}
}

func TestParseLimit(t *testing.T) {
	tests := []struct {
		q    string
		want int
		bad  bool
	}{
		{"", 50, false}, {"limit=", 50, false}, {"limit=1", 1, false}, {"limit=200", 200, false},
		{"limit=201", 200, false}, {"limit=99999", 200, false}, {"limit=0", 0, true}, {"limit=-3", 0, true},
		{"limit=abc", 0, true}, {"limit=1.5", 0, true}, {"limit=%20", 0, true},
	}
	for _, tc := range tests {
		q, _ := url.ParseQuery(tc.q)
		got, f := parseLimit(q)
		if tc.bad {
			if f["limit"] == "" {
				t.Errorf("%q: want validation error", tc.q)
			}
			continue
		}
		if f != nil || got != tc.want {
			t.Errorf("%q: got %d %v want %d", tc.q, got, f, tc.want)
		}
	}
}

func TestPageParamsAndList(t *testing.T) {
	rec := httptest.NewRecorder()
	if _, _, ok := pageParams(rec, httptest.NewRequest("GET", "/x?limit=zero", nil)); ok || rec.Code != 422 || !strings.Contains(rec.Body.String(), `"limit"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	limit, cursor, ok := pageParams(httptest.NewRecorder(), httptest.NewRequest("GET", "/x?limit=7&cursor=abc", nil))
	if !ok || limit != 7 || cursor != "abc" {
		t.Fatal(limit, cursor, ok)
	}
	if _, c, _ := pageParams(httptest.NewRecorder(), httptest.NewRequest("GET", "/x?cursor=", nil)); c != "" {
		t.Fatal("empty cursor means first page")
	}

	rec = httptest.NewRecorder()
	writeList(rec, []string(nil), "")
	if strings.TrimSpace(rec.Body.String()) != `{"items":[],"next_cursor":null}` {
		t.Fatalf("%s", rec.Body)
	}
	rec = httptest.NewRecorder()
	writeList(rec, []int{1}, "tok")
	if strings.TrimSpace(rec.Body.String()) != `{"items":[1],"next_cursor":"tok"}` {
		t.Fatalf("%s", rec.Body)
	}

	q, _ := url.ParseQuery("a=1&b=1&b=2")
	if v, dup := queryOnce(q, "a"); v != "1" || dup {
		t.Fatal("a")
	}
	if _, dup := queryOnce(q, "b"); !dup {
		t.Fatal("b should be a duplicate")
	}
	if v, dup := queryOnce(q, "c"); v != "" || dup {
		t.Fatal("c")
	}
}
