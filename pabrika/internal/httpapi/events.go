package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/RJGJ/Pabrika/internal/auth"
	"github.com/RJGJ/Pabrika/internal/service"
)

// Defaults of the SSE handler (phase 3 spec section 5).
const (
	defaultKeepalive    = 25 * time.Second // keepalive comment plus session and membership re-check
	defaultWriteTimeout = 10 * time.Second // per-frame write deadline
	defaultCheckTimeout = 5 * time.Second  // bound on each DB check made on a tick
)

// sessionChecker is auth.Sessions.Validate: nil = live session, auth.ErrInvalidCredentials = the
// session is gone or expired, anything else = a database failure. It must be read-only (no
// sliding): an idle open tab must not keep a session alive past its expiry.
type sessionChecker interface {
	Validate(ctx context.Context, tokenHash string) error
}

// eventsHandler serves GET /api/v1/projects/{id}/events (server-sent events).
//
// Operational notes (phase 6 documents these for operators):
//
//   - Nothing on this route may buffer or compress. The app has no compression middleware and
//     no http.TimeoutHandler; every wrapper in the chain implements Flush and Unwrap so
//     http.ResponseController reaches the real connection. Reverse proxies must not buffer
//     either: nginx needs `proxy_buffering off` for this path (the X-Accel-Buffering header
//     also tells it), proxy read timeouts must exceed the 25 s keepalive, and any proxy
//     compression must exclude text/event-stream. The Cache-Control value
//     "no-store, no-transform" asks intermediaries not to transform the body.
//   - Browsers allow about six HTTP/1.1 connections per origin across all tabs and every open
//     board holds one stream. Behind an HTTP/2 proxy (Caddy by default) that limit does not
//     apply; plain HTTP on localhost is HTTP/1.1, so more than about five open boards in one
//     browser can starve ordinary requests. Known limitation.
//   - The http.Server has no WriteTimeout; the handler also sets a write deadline before every
//     frame (and clears the read deadline once) so a server-level timeout added later cannot
//     cut the stream and a client that stops reading cannot pin the goroutine.
type eventsHandler struct {
	hub      *service.Hub
	projects service.ProjectService
	sessions sessionChecker
	log      *slog.Logger

	keepalive    time.Duration // default 25s
	writeTimeout time.Duration // per-frame write deadline, default 10s
	checkTimeout time.Duration // per DB check on a tick, default 5s

	// beforeLoop is a test hook: it runs after the 200 and the preamble are flushed, before the
	// select loop (tests use it to hold the handler while events pile up).
	beforeLoop func()
}

func newEventsHandler(hub *service.Hub, projects service.ProjectService, sessions sessionChecker, log *slog.Logger) *eventsHandler {
	return &eventsHandler{
		hub: hub, projects: projects, sessions: sessions, log: log,
		keepalive: defaultKeepalive, writeTimeout: defaultWriteTimeout, checkTimeout: defaultCheckTimeout,
	}
}

// registerEventRoutes mounts the stream. It is session-only (a token gets 403 session_required
// from the guard) and read-only, so Write stays false.
func (s *Server) registerEventRoutes() {
	s.events = newEventsHandler(s.hub, s.svc.Projects, s.sessions, s.log)
	s.Mount("GET /api/v1/projects/{id}/events", SessionOnly, s.events)
}

func (h *eventsHandler) writeFail(w http.ResponseWriter, r *http.Request, err error) {
	e, ok := mapError(err)
	if !ok {
		h.log.Error("event stream failed", "err", err, "request_id", w.Header().Get("X-Request-Id"), "path", r.URL.Path)
	}
	e.write(w)
}

func setStreamHeaders(w http.ResponseWriter) {
	hd := w.Header()
	hd.Set("Content-Type", "text/event-stream")
	// Replaces the blanket "no-store" the headers middleware sets before the handler runs.
	hd.Set("Cache-Control", "no-store, no-transform")
	hd.Set("X-Accel-Buffering", "no")
	// No Connection header (forbidden on HTTP/2, Go manages it on HTTP/1.1) and no
	// Content-Encoding: the app never compresses.
}

func isDone(c <-chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

// ServeHTTP implements http.Handler.
func (h *eventsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, ok := auth.PrincipalFrom(r.Context())
	if !ok || p.Method != auth.MethodSession || p.Session == nil {
		// The SessionOnly guard makes this unreachable; fail closed if it is ever bypassed.
		WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "Authentication required")
		return
	}
	actor := service.UserActor(p.User.ID)
	if h.hub == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeUnavailable, "Live updates are unavailable")
		return
	}

	// Membership check 1 (key or ULID; non-member, unknown and out-of-limit are all not found).
	ref, err := h.projects.Resolve(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		h.writeFail(w, r, err)
		return
	}

	if r.Method == http.MethodHead {
		setStreamHeaders(w)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Subscribe, then check membership again: a removal that lands between check 1 and the
	// subscription would otherwise leave a stream nobody will close.
	sub, err := h.hub.Subscribe(ref.ID, p.User.ID)
	if err != nil {
		if errors.Is(err, service.ErrHubClosed) {
			WriteError(w, http.StatusServiceUnavailable, CodeUnavailable, "Live updates are unavailable, the server is shutting down")
			return
		}
		h.writeFail(w, r, err)
		return
	}
	defer sub.Close()
	if _, err := h.projects.Resolve(r.Context(), actor, ref.ID); err != nil {
		h.writeFail(w, r, err)
		return
	}

	h.stream(w, r, p, actor, ref.ID, sub)
}

// stream runs the open handshake and the event loop. It logs once on exit: project id, user id,
// reason and duration, never event content.
func (h *eventsHandler) stream(w http.ResponseWriter, r *http.Request, p auth.Principal, actor service.Actor, projectID string, sub *service.Subscription) {
	start := time.Now()
	reason := "client_gone"
	defer func() {
		if sr := sub.Reason(); sr != service.CloseNone && sr != service.CloseClient && reason == "client_gone" {
			reason = string(sr)
		}
		h.log.Info("event stream closed", "project_id", projectID, "user_id", p.User.ID, "reason", reason,
			"duration_ms", time.Since(start).Milliseconds())
	}()

	rc := http.NewResponseController(w)
	// The server's ReadTimeout would otherwise expire the background read Go runs on the
	// connection and cancel the request context mid-stream.
	if err := rc.SetReadDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		h.log.Warn("event stream: clear read deadline", "err", err)
	}
	// write sends one frame under a fresh write deadline and flushes it.
	write := func(frame string) error {
		if err := rc.SetWriteDeadline(time.Now().Add(h.writeTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			h.log.Warn("event stream: set write deadline", "err", err)
		}
		if _, err := io.WriteString(w, frame); err != nil {
			return err
		}
		return rc.Flush()
	}

	setStreamHeaders(w)
	w.WriteHeader(http.StatusOK)
	if err := write("retry: 3000\n\n: connected\n\n"); err != nil {
		reason = "write_error"
		return
	}

	if h.beforeLoop != nil {
		h.beforeLoop()
	}
	ticker := time.NewTicker(h.keepalive)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-sub.Done():
			return
		case ev := <-sub.C():
			if isDone(sub.Done()) {
				return // no further frames once access is lost
			}
			data, err := json.Marshal(ev)
			if err != nil {
				h.log.Error("event stream: marshal", "err", err)
				continue
			}
			if err := write("event: " + string(ev.Type) + "\ndata: " + string(data) + "\n\n"); err != nil {
				reason = "write_error"
				return
			}
		case <-ticker.C:
			if err := write(": keepalive\n\n"); err != nil {
				reason = "write_error"
				return
			}
			if r := h.recheck(r.Context(), p.Session.TokenHash, actor, projectID); r != "" {
				reason = r
				return
			}
		}
	}
}

// recheck runs the tick checks: session liveness first, then membership. It returns the close
// reason, or "" to keep the stream open (also on infrastructure errors, which are only logged).
func (h *eventsHandler) recheck(parent context.Context, tokenHash string, actor service.Actor, projectID string) string {
	ctx, cancel := context.WithTimeout(parent, h.checkTimeout)
	defer cancel()
	if err := h.sessions.Validate(ctx, tokenHash); err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			return "session_invalid"
		}
		if parent.Err() != nil {
			return "client_gone"
		}
		h.log.Warn("event stream: session check failed, keeping stream open", "err", err)
	}
	if _, err := h.projects.Resolve(ctx, actor, projectID); err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return "access_lost"
		}
		if parent.Err() != nil {
			return "client_gone"
		}
		h.log.Warn("event stream: membership check failed, keeping stream open", "err", err)
	}
	return ""
}
