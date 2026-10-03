// Package httpapi is the REST API: route table, middleware chain, error shape, request
// decoding, pagination and the response views. Handlers are thin wrappers over internal/service;
// scope, project limit, membership, roles and validation are enforced there, never here.
//
// # Files
//
//	server.go      Server, Deps, Access, Route, route table, New, Mount/MountRaw/SetFallback, /healthz
//	middleware.go  global chain (request id + log, headers, recover, body cap, Origin) and the route guard
//	errors.go      WriteError (exported), mapError, decodeJSON, writeJSON
//	requests.go    field[T] (absent / null / value for PATCH)
//	pagination.go  parseLimit, pageParams, queryOnce, writeList
//	view.go        every response shape and its mapper (spec section 6)
//	*_handlers.go  one file per domain, each with an empty registerXRoutes method (extension points)
//	events.go      reserved for the SSE handler (phase 3)
//
// # Adding an endpoint
//
// Fill in the registerXRoutes method of your domain file (registerAuthRoutes, registerTokenRoutes,
// registerProjectRoutes, registerMemberRoutes, registerLabelRoutes, registerTicketRoutes,
// registerCommentRoutes). They are called from registerRoutes in server.go; do not edit shared
// files. Register under the full /api/v1 path with Go 1.22 mux syntax:
//
//	func (s *Server) registerLabelRoutes() {
//	    s.route("GET /api/v1/projects/{id}/labels", Authed, false, s.listLabels)
//	    s.route("POST /api/v1/projects/{id}/labels", Authed, true, s.createLabel)
//	    s.route("DELETE /api/v1/labels/{id}", Authed, true, s.deleteLabel)
//	}
//
// route(pattern, access, write, handler) takes:
//
//   - Access: Public (no credentials), Authed (session or token), SessionOnly (token gets 403
//     session_required). Always choose one explicitly: AccessUnset fails the coverage test and is
//     treated as SessionOnly + Write by the guard.
//   - write: true for anything that changes data, so a read-scope token is rejected early with
//     403 insufficient_scope. This is a cheap early check; the service enforces it too.
//
// The guard runs before your handler and applies, in this order: resolve the principal (401, the
// cookie slide/clear is applied for you), SessionOnly (403), write scope (403), then the 415
// check of a present body. Your handler then does, in this order:
//
//  1. decode: decodeJSON(w, r, &dto) (400 for malformed, unknown field, trailing data or empty
//     body; 400 body_too_large; 415). It writes the response itself and returns false, so
//     `if !decodeJSON(...) { return }`. Use decodeOpts{Optional: true} for an optional body and
//     decodeOpts{Hints: ...} for friendlier unknown-field messages (PATCH ticket status: "use /move").
//     Query parameters: pageParams(w, r) (limit and opaque cursor), queryOnce for filters.
//  2. call the service with principal(r).Actor(), passing r.PathValue("id") unvalidated (project
//     keys and ULIDs are both accepted; garbage is just ErrNotFound);
//  3. on error call s.fail(w, r, err): mapError turns *service.Error into the status, code and
//     fields of the wire format (Kind decides the status, not_author is sent as forbidden,
//     validation errors are validation_failed with fields) and logs anything unknown as a 500;
//  4. on success build the body with a view mapper (mapProject, ticketFull, ticketListItem,
//     mapMove, mapComment, mapToken, ...) and send it with writeJSON(w, status, v), writeList(w,
//     items, nextCursor) for lists (empty cursor becomes null) or noContent(w) for 204.
//
// Never build JSON shapes by hand: if a field is missing from a view, add it in view.go and to
// the literal key sets in view_test.go. PATCH bodies use field[T] and convert with .opt() to the
// service's Optional. Request DTOs live in this package (a requests_<domain>.go file or the
// handler file); length caps, enums and formats are checked by the service Validate().
//
// Rate limiters, the hasher, sessions and the clock are on the Server (s.loginLimiter,
// s.signupLimiter, s.passwordLimiter, s.hasher, s.sessions, s.now); s.clientIP(r) honours
// TRUST_PROXY; writeRateLimited sets Retry-After. s.svc is the *service.Services; s.hub is the
// event hub (nil when not wired).
//
// # Testing an endpoint
//
// harness_test.go (package httpapi) builds the real Server over testutil.Env with one fake
// clock shared by services, sessions and limiters:
//
//	h := Setup(t, Opts{})                        // Opts: SignupOff, CookieSecure, TrustProxy, BaseURL, File
//	alice := h.Signup(t, "alice@x.io")           // user + session, returns a cookie-jar *Client
//	secret, tokenID := h.MkToken(t, alice, service.ScopeWrite, "")
//	resp := h.Do(t, "POST", "/api/v1/projects", map[string]any{"key": "WEB", "name": "Web"}, As(alice))
//	resp = h.Do(t, "GET", "/api/v1/projects", nil, Bearer(secret))
//	resp.Code, resp.JSON(t), resp.ErrCode(t)
//
// Do always sets Origin to BASE_URL and Content-Type: application/json for bodies; NoOrigin(),
// WithOrigin(o), WithHeader(k, v), RawBody(s, contentType) change that. Seed projects and members
// with h.Env (NewUser, NewProject, AddMember, Archive). Write golden key-set tests with literal
// expected key lists (see view_test.go) so renames fail the test.
//
// # Mount points for later phases
//
// Mount(pattern, access, handler) registers under the guard and appears in Routes(); MountRaw
// bypasses the guard (used by /mcp, which authenticates with Resolver.ResolveBearer and writes
// failures with WriteError); SetFallback (or Mount("/", ...)) installs the SPA handler. Deps.Hub
// carries the event hub for the SSE handler.
//
// # Mux notes
//
// The method-less "/api/" catch-all produces the JSON 404 and 405 (with Allow, by probing the
// mux for other methods); never register "GET /" (it conflicts with it). Cache-Control: no-store
// is set before the handler runs on /api/, /mcp and /healthz, so a handler may override it.
package httpapi
