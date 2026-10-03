package mcpserver

import (
	"strings"
	"testing"

	"github.com/RJGJ/Pabrika/internal/httpapi"
	"github.com/RJGJ/Pabrika/internal/service"
)

// Phase 6 audit (docs/security-audit.md H10 and L5 for /mcp): the global header set and the
// log redaction also hold on the MCP routes.

func TestAuditMCPHeadersOnEveryResponse(t *testing.T) {
	h := newHTTPFx(t)
	_, secret := h.token("alice", service.ScopeWrite, nil)
	bearerHdr := map[string]string{"Authorization": "Bearer " + secret}
	cases := []struct {
		name, method, path, body string
		hdr                      map[string]string
		code                     int
	}{
		{"no token", "POST", "/mcp", initBody, nil, 401},
		{"ok", "POST", "/mcp", initBody, bearerHdr, 200},
		{"GET 405", "GET", "/mcp", "", bearerHdr, 405},
		{"origin 403", "POST", "/mcp", initBody, map[string]string{"Authorization": "Bearer " + secret, "Origin": "https://evil.example"}, 403},
		{"subpath 404", "POST", "/mcp/x", initBody, bearerHdr, 404},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := h.do(c.method, c.path, c.body, c.hdr)
			if r.Code != c.code {
				t.Fatalf("status %d want %d: %s", r.Code, c.code, r.Body)
			}
			want := map[string]string{
				"Content-Security-Policy": httpapi.CSP,
				"X-Content-Type-Options":  "nosniff",
				"Referrer-Policy":         httpapi.ReferrerPolicy,
				"X-Frame-Options":         "DENY",
				"Permissions-Policy":      httpapi.PermissionsPolicy,
				"Cache-Control":           "no-store",
			}
			for k, v := range want {
				if got := r.Header.Get(k); got != v {
					t.Errorf("%s = %q, want %q", k, got, v)
				}
			}
			for k := range r.Header {
				if strings.HasPrefix(strings.ToLower(k), "access-control-") {
					t.Errorf("CORS header %s", k)
				}
			}
			if r.Header.Get("X-Request-Id") == "" {
				t.Error("no X-Request-Id")
			}
		})
	}
}

func TestAuditMCPLogsNeverContainTokenOrArguments(t *testing.T) {
	h := newHTTPFx(t)
	_, secret := h.token("alice", service.ScopeWrite, nil)
	call := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"create_ticket","arguments":{"project":"WEB","title":"argument-canary-title"}}}`
	h.do("POST", "/mcp", initBody, map[string]string{"Authorization": "Bearer " + secret})
	h.do("POST", "/mcp", call, map[string]string{"Authorization": "Bearer " + secret})
	h.do("POST", "/mcp", initBody, map[string]string{"Authorization": "Bearer pb_" + strings.Repeat("f", 64)})
	logs := h.logs.String()
	if !strings.Contains(logs, "path=/mcp") {
		t.Fatalf("/mcp requests not logged:\n%s", logs)
	}
	for _, forbidden := range []string{secret, "pb_" + strings.Repeat("f", 64), "Authorization", "Bearer", "argument-canary-title"} {
		if strings.Contains(logs, forbidden) {
			t.Errorf("log contains %q", forbidden)
		}
	}
}
