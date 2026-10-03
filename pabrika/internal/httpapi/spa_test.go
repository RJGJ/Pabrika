package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func spaFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":            {Data: []byte("<!doctype html><title>Pabrika</title>")},
		"assets/app-abc.js":     {Data: []byte("console.log(1)")},
		"assets/app-abc.css":    {Data: []byte("body{}")},
		"assets/font-abc.woff2": {Data: []byte("woff2")},
		"assets/logo-abc.svg":   {Data: []byte("<svg xmlns='http://www.w3.org/2000/svg'/>")},
		"favicon.svg":           {Data: []byte("<svg xmlns='http://www.w3.org/2000/svg'/>")},
		"icon.ico":              {Data: []byte{0, 0, 1, 0}},
	}
}

func spaDo(h http.Handler, method, target string, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://example.test"+target, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestSPA(t *testing.T) {
	h := NewSPAHandler(spaFS())
	const index = "<!doctype html><title>Pabrika</title>"
	tests := []struct {
		name, method, path string
		status             int
		body               string // exact body when non-empty
		ctype              string // Content-Type prefix
		cache              string // exact Cache-Control when non-empty
		json404            bool
	}{
		{name: "root", method: "GET", path: "/", status: 200, body: index, ctype: "text/html", cache: "no-cache"},
		{name: "index.html no redirect", method: "GET", path: "/index.html", status: 200, body: index, cache: "no-cache"},
		{name: "deep link", method: "GET", path: "/p/WEB/t/12", status: 200, body: index, cache: "no-cache"},
		{name: "login", method: "GET", path: "/login", status: 200, body: index},
		{name: "mcpx is not /mcp", method: "GET", path: "/mcpx", status: 200, body: index},
		{name: "apix is not /api", method: "GET", path: "/apix", status: 200, body: index},
		{name: "asset immutable", method: "GET", path: "/assets/app-abc.js", status: 200, body: "console.log(1)",
			ctype: "text/javascript", cache: "public, max-age=31536000, immutable"},
		{name: "css", method: "GET", path: "/assets/app-abc.css", status: 200, ctype: "text/css", cache: "public, max-age=31536000, immutable"},
		{name: "woff2", method: "GET", path: "/assets/font-abc.woff2", status: 200, ctype: "font/woff2"},
		{name: "svg", method: "GET", path: "/assets/logo-abc.svg", status: 200, ctype: "image/svg+xml"},
		{name: "ico", method: "GET", path: "/icon.ico", status: 200, ctype: "image/x-icon", cache: "public, max-age=3600"},
		{name: "root file", method: "GET", path: "/favicon.svg", status: 200, cache: "public, max-age=3600"},
		{name: "missing asset", method: "GET", path: "/assets/missing.js", status: 404},
		{name: "missing asset no ext", method: "GET", path: "/assets/missing", status: 404},
		{name: "missing js", method: "GET", path: "/foo.js", status: 404},
		{name: "missing favicon", method: "GET", path: "/favicon.ico", status: 404},
		{name: "api 404", method: "GET", path: "/api/v1/nope", status: 404, json404: true},
		{name: "api POST 404", method: "POST", path: "/api/v1/nope", status: 404, json404: true},
		{name: "api other", method: "GET", path: "/api/other", status: 404, json404: true},
		{name: "api bare", method: "GET", path: "/api", status: 404, json404: true},
		{name: "mcp bare", method: "GET", path: "/mcp", status: 404, json404: true},
		{name: "mcp sub", method: "GET", path: "/mcp/unknown", status: 404, json404: true},
		{name: "healthz sub", method: "GET", path: "/healthz/x", status: 404, json404: true},
		{name: "well-known", method: "GET", path: "/.well-known/security.txt", status: 404, json404: true},
		{name: "dot dot api", method: "GET", path: "/p/../api/x", status: 404, json404: true},
		{name: "traversal", method: "GET", path: "/../etc/passwd", status: 200, body: index},
		{name: "encoded traversal", method: "GET", path: "/%2e%2e/secret", status: 200, body: index},
		{name: "POST root", method: "POST", path: "/", status: 405},
		{name: "DELETE asset", method: "DELETE", path: "/assets/app-abc.js", status: 405},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := spaDo(h, tc.method, tc.path)
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d (body %q)", w.Code, tc.status, w.Body.String())
			}
			if tc.body != "" && w.Body.String() != tc.body {
				t.Errorf("body = %q, want %q", w.Body.String(), tc.body)
			}
			if tc.ctype != "" && !strings.HasPrefix(w.Header().Get("Content-Type"), tc.ctype) {
				t.Errorf("Content-Type = %q, want prefix %q", w.Header().Get("Content-Type"), tc.ctype)
			}
			if tc.cache != "" && w.Header().Get("Cache-Control") != tc.cache {
				t.Errorf("Cache-Control = %q, want %q", w.Header().Get("Cache-Control"), tc.cache)
			}
			if tc.json404 {
				if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
					t.Errorf("Content-Type = %q, want JSON", got)
				}
				if !strings.Contains(w.Body.String(), `"not_found"`) {
					t.Errorf("body = %q, want code not_found", w.Body.String())
				}
			}
			if tc.status == 404 && !tc.json404 {
				if strings.Contains(w.Body.String(), "<html") || strings.Contains(w.Body.String(), "Pabrika") {
					t.Errorf("plain 404 returned HTML/index: %q", w.Body.String())
				}
				if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
					t.Errorf("Content-Type = %q, want text/plain", w.Header().Get("Content-Type"))
				}
			}
			if tc.status == 405 {
				if got := w.Header().Get("Allow"); got != "GET, HEAD" {
					t.Errorf("Allow = %q", got)
				}
				if !strings.Contains(w.Body.String(), `"method_not_allowed"`) {
					t.Errorf("body = %q", w.Body.String())
				}
			}
		})
	}
}

func TestSPAHead(t *testing.T) {
	h := NewSPAHandler(spaFS())
	for _, p := range []string{"/", "/p/WEB", "/assets/app-abc.js"} {
		w := spaDo(h, "HEAD", p)
		if w.Code != 200 || w.Body.Len() != 0 {
			t.Errorf("HEAD %s: status %d, body %d bytes", p, w.Code, w.Body.Len())
		}
		if w.Header().Get("Content-Type") == "" {
			t.Errorf("HEAD %s: no Content-Type", p)
		}
	}
}

func TestSPAETag(t *testing.T) {
	h := NewSPAHandler(spaFS())
	for _, p := range []string{"/", "/index.html", "/p/WEB", "/assets/app-abc.js"} {
		w := spaDo(h, "GET", p)
		etag := w.Header().Get("ETag")
		if etag == "" || !strings.HasPrefix(etag, `"`) {
			t.Fatalf("%s: ETag = %q", p, etag)
		}
		w2 := spaDo(h, "GET", p, "If-None-Match", etag)
		if w2.Code != http.StatusNotModified {
			t.Errorf("%s: If-None-Match status = %d, want 304", p, w2.Code)
		}
		if w2.Body.Len() != 0 {
			t.Errorf("%s: 304 has a body", p)
		}
		if w3 := spaDo(h, "GET", p, "If-None-Match", `"other"`); w3.Code != 200 {
			t.Errorf("%s: mismatching ETag status = %d", p, w3.Code)
		}
	}
	other := NewSPAHandler(fstest.MapFS{"index.html": {Data: []byte("v2")}})
	if spaDo(other, "GET", "/").Header().Get("ETag") == spaDo(h, "GET", "/").Header().Get("ETag") {
		t.Error("ETag does not depend on content")
	}
}

func TestSPABundleMissing(t *testing.T) {
	h := NewSPAHandler(fstest.MapFS{".gitkeep": {Data: nil}})
	for _, p := range []string{"/", "/p/WEB", "/assets/x.js", "/foo.js"} {
		w := spaDo(h, "GET", p)
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: status = %d, want 503", p, w.Code)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control = %q", p, w.Header().Get("Cache-Control"))
		}
		if !strings.Contains(w.Body.String(), "UI not built") {
			t.Errorf("%s: body = %q", p, w.Body.String())
		}
	}
	w := spaDo(h, "GET", "/api/v1/nope")
	if w.Code != 404 || !strings.Contains(w.Body.String(), "not_found") {
		t.Errorf("api path with empty fs: %d %q", w.Code, w.Body.String())
	}
	if w := spaDo(h, "POST", "/"); w.Code != 405 {
		t.Errorf("POST /: %d", w.Code)
	}
}

func TestSPAMimeTypes(t *testing.T) {
	// distroless has no mime.types: the registrations in spa.go must supply these.
	for ext, want := range map[string]string{
		".js": "text/javascript", ".mjs": "text/javascript", ".css": "text/css", ".woff2": "font/woff2",
		".woff": "font/woff", ".svg": "image/svg+xml", ".ico": "image/x-icon",
		".webmanifest": "application/manifest+json", ".map": "application/json",
	} {
		if got := spaMime(ext); !strings.HasPrefix(got, want) {
			t.Errorf("%s: %q, want prefix %q", ext, got, want)
		}
	}
}

// TestSPAWithServer checks the precedence on the real mux: API routes, /healthz, the
// .well-known JSON 404 and the SPA fallback each reach the right handler.
func TestSPAWithServer(t *testing.T) {
	h := Setup(t, Opts{})
	h.Server.SetFallback(NewSPAHandler(spaFS()))
	srv := httptest.NewServer(h.Server.Handler())
	t.Cleanup(srv.Close)

	get := func(path string) (int, string, http.Header) {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), resp.Header
	}
	if c, b, _ := get("/healthz"); c != 200 || !strings.Contains(b, `"ok"`) {
		t.Errorf("/healthz: %d %q", c, b)
	}
	if c, b, _ := get("/api/v1/auth/config"); c != 200 || !strings.Contains(b, "signup_enabled") {
		t.Errorf("/api/v1/auth/config: %d %q", c, b)
	}
	if c, b, _ := get("/api/v1/nope"); c != 404 || !strings.Contains(b, "not_found") {
		t.Errorf("/api/v1/nope: %d %q", c, b)
	}
	if c, b, _ := get("/.well-known/x"); c != 404 || !strings.Contains(b, "not_found") {
		t.Errorf("/.well-known/x: %d %q", c, b)
	}
	if c, b, hd := get("/p/WEB"); c != 200 || !strings.Contains(b, "Pabrika") || hd.Get("Content-Security-Policy") != CSP {
		t.Errorf("/p/WEB: %d %q csp=%q", c, b, hd.Get("Content-Security-Policy"))
	}
	if c, _, hd := get("/assets/app-abc.js"); c != 200 || hd.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("asset: %d nosniff=%q", c, hd.Get("X-Content-Type-Options"))
	}
	if c, _, _ := get("/assets/missing.js"); c != 404 {
		t.Errorf("missing asset: %d", c)
	}
	if _, _, hd := get("/healthz"); hd.Get("Cache-Control") != "no-store" {
		t.Errorf("/healthz Cache-Control = %q", hd.Get("Cache-Control"))
	}
}
