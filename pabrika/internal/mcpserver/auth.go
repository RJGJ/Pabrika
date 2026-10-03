package mcpserver

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/RJGJ/Pabrika/internal/auth"
	"github.com/RJGJ/Pabrika/internal/httpapi"
	"github.com/RJGJ/Pabrika/internal/service"
)

// ServeHTTP applies, in order: the Origin rule (every method), bearer authentication
// (Resolver.ResolveBearer, never cookies), the method rule of the stateless mode and the body
// cap, then hands the request to the SDK with the principal in its context.
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.originAllowed(r) {
		httpapi.WriteError(w, http.StatusForbidden, httpapi.CodeOriginMismatch, "Origin not allowed")
		return
	}
	p, err := h.Resolver.ResolveBearer(r)
	if err != nil {
		if _, ok := err.(*auth.AuthError); ok || err == auth.ErrNoCredentials {
			w.Header().Set("WWW-Authenticate", "Bearer")
			httpapi.WriteError(w, http.StatusUnauthorized, httpapi.CodeUnauthorized, "Authentication required")
			return
		}
		h.Logger.Error("mcp auth failed", "err", err)
		httpapi.WriteError(w, http.StatusInternalServerError, httpapi.CodeInternal, "Internal error")
		return
	}
	if p.Method != auth.MethodToken || p.Token == nil {
		w.Header().Set("WWW-Authenticate", "Bearer")
		httpapi.WriteError(w, http.StatusUnauthorized, httpapi.CodeUnauthorized, "Authentication required")
		return
	}
	if r.Method != http.MethodPost {
		// Stateless: no standalone stream and no sessions to delete (the SDK answers DELETE
		// with 400, so it is normalised here).
		w.Header().Set("Allow", "POST")
		httpapi.WriteError(w, http.StatusMethodNotAllowed, httpapi.CodeMethodNotAllowed, "Method not allowed")
		return
	}
	if r.ContentLength > httpapi.MaxBodyBytes {
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.CodeBodyTooLarge, "Request body too large")
		return
	}
	// originAllowed matched this Origin to BASE_URL case-insensitively and with default ports
	// normalised; the SDK's own check compares exactly, so hand it the canonical spelling.
	if _, ok := r.Header["Origin"]; ok {
		r.Header.Set("Origin", h.origin)
	}
	r = r.WithContext(auth.WithPrincipal(r.Context(), p))
	h.sdk.ServeHTTP(&noStoreWriter{ResponseWriter: w}, r)
}

// noStoreWriter keeps Cache-Control: no-store (the SDK sets "no-cache, no-transform").
type noStoreWriter struct {
	http.ResponseWriter
	wrote bool
}

func (w *noStoreWriter) force() {
	if !w.wrote {
		w.wrote = true
		w.Header().Set("Cache-Control", "no-store")
	}
}

func (w *noStoreWriter) WriteHeader(code int) {
	w.force()
	w.ResponseWriter.WriteHeader(code)
}

func (w *noStoreWriter) Write(b []byte) (int, error) {
	w.force()
	return w.ResponseWriter.Write(b)
}

func (w *noStoreWriter) Flush() {
	w.force()
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *noStoreWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// originAllowed: an absent Origin is allowed (non-browser clients); a present one must equal
// BASE_URL's origin. "null" never matches.
func (h *handler) originAllowed(r *http.Request) bool {
	vals, present := r.Header["Origin"]
	if !present {
		return true
	}
	if len(vals) != 1 {
		return false
	}
	got, ok := normalizeOrigin(vals[0])
	return ok && got == h.origin
}

// normalizeOrigin lowercases scheme and host and drops the default port (same form as
// config.Config.Origin). "null", paths and junk fail.
func normalizeOrigin(v string) (string, bool) {
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", false
	}
	if u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	scheme, host, port := strings.ToLower(u.Scheme), strings.ToLower(u.Hostname()), u.Port()
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		host += ":" + port
	}
	return scheme + "://" + host, true
}

// toolsFor returns the tools a principal may see: read scope gets the 5 read tools, write
// scope all 14. The service enforces scope again on every write.
func toolsFor(p auth.Principal) []*toolDef {
	readOnly := p.Token == nil || p.Token.Scope != service.ScopeWrite
	var out []*toolDef
	for _, d := range allTools {
		if d.write && readOnly {
			continue
		}
		out = append(out, d)
	}
	return out
}

// VisibleToolNames lists the tool names visible to a principal (tests, docs).
func VisibleToolNames(p auth.Principal) []string {
	var names []string
	for _, d := range toolsFor(p) {
		names = append(names, d.name)
	}
	return names
}
