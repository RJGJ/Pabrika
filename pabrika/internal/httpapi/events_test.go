package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RJGJ/Pabrika/internal/auth"
	"github.com/RJGJ/Pabrika/internal/config"
	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/testutil"
)

// SSE tests: the real Server (full middleware chain) over a real hub, served by a real
// httptest.Server and read with a real client. State is created through the service layer, so
// these tests do not depend on the REST CRUD handlers.

const sseWait = 5 * time.Second

type sseEnv struct {
	Env   *testutil.Env
	Hub   *service.Hub
	Srv   *Server
	TS    *httptest.Server
	Owner testutil.User
	Proj  testutil.Project
	Logs  *syncBuffer
}

// setupSSE builds the environment. tune runs before the test server starts (keepalive, timeouts).
func setupSSE(t *testing.T, hubOpts service.HubOptions, tune func(*sseEnv)) *sseEnv {
	t.Helper()
	env := testutil.NewTestServices(t, testutil.WithHub(hubOpts))
	logs := &syncBuffer{}
	srv := New(Deps{
		Config: config.Config{Port: 8080, DBPath: "unused", BaseURL: "http://localhost:8080", AllowSignup: true},
		Store:  env.Store, Services: env.Svc, Logger: slog.New(slog.NewTextHandler(logs, nil)),
		Now: env.Clock.Now, Hasher: auth.NewHasher(auth.TestParams, 0), Hub: env.Hub,
	})
	e := &sseEnv{Env: env, Hub: env.Hub, Srv: srv, Logs: logs}
	e.Owner = env.NewUser(t, "owner@x.io", "Owner")
	e.Proj = env.NewProject(t, e.Owner, "WEB")
	e.TS = httptest.NewUnstartedServer(srv.Handler())
	if tune != nil {
		tune(e)
	}
	e.TS.Start()
	t.Cleanup(func() { e.Hub.Shutdown(); e.TS.CloseClientConnections(); e.TS.Close() })
	return e
}

func (e *sseEnv) user(t *testing.T, email string) (testutil.User, string) {
	t.Helper()
	u := e.Env.NewUser(t, email, "User")
	return u, e.Env.NewSession(t, u.ID)
}

func (e *sseEnv) ownerCookie(t *testing.T) string { return e.Env.NewSession(t, e.Owner.ID) }

func (e *sseEnv) waitSubs(t *testing.T, project string, n int) {
	t.Helper()
	deadline := time.Now().Add(sseWait)
	for e.Hub.SubscriberCount(project) != n {
		if time.Now().After(deadline) {
			t.Fatalf("subscribers on %s = %d, want %d", project, e.Hub.SubscriberCount(project), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type stream struct {
	Resp  *http.Response
	lines chan string
}

func (e *sseEnv) get(t *testing.T, method, path, cookie string, hdr map[string]string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequest(method, e.TS.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: cookie})
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return e.TS.Client().Do(req)
}

// open starts a stream; the body is read line by line in the background.
func (e *sseEnv) open(t *testing.T, path, cookie string) *stream {
	t.Helper()
	resp, err := e.get(t, "GET", path, cookie, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("open %s: %d %s", path, resp.StatusCode, b)
	}
	s := &stream{Resp: resp, lines: make(chan string, 1024)}
	go func() {
		defer close(s.lines)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			s.lines <- sc.Text()
		}
	}()
	t.Cleanup(func() { resp.Body.Close() })
	return s
}

func (s *stream) line(t *testing.T, within time.Duration) (string, bool) {
	t.Helper()
	select {
	case l, ok := <-s.lines:
		return l, ok
	case <-time.After(within):
		t.Fatal("timed out waiting for a stream line")
		return "", false
	}
}

// until reads lines until one has the prefix and returns it.
func (s *stream) until(t *testing.T, prefix string) string {
	t.Helper()
	for {
		l, ok := s.line(t, sseWait)
		if !ok {
			t.Fatalf("stream ended while waiting for %q", prefix)
		}
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
}

// frame reads the next named event and returns its name and data.
func (s *stream) frame(t *testing.T) (string, string) {
	t.Helper()
	name := strings.TrimPrefix(s.until(t, "event: "), "event: ")
	data := strings.TrimPrefix(s.until(t, "data: "), "data: ")
	return name, data
}

// eof requires the stream to end within the timeout without data frames.
func (s *stream) eof(t *testing.T, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case l, ok := <-s.lines:
			if !ok {
				return
			}
			if strings.HasPrefix(l, "event:") || strings.HasPrefix(l, "data:") {
				t.Fatalf("frame after close: %q", l)
			}
		case <-deadline:
			t.Fatal("stream did not end")
		}
	}
}

func (s *stream) noLine(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case l, ok := <-s.lines:
		if ok && l != "" && !strings.HasPrefix(l, ":") {
			t.Fatalf("unexpected line %q", l)
		}
	case <-time.After(d):
	}
}

func (e *sseEnv) path(ref string) string { return "/api/v1/projects/" + ref + "/events" }

func (e *sseEnv) newTicket(t *testing.T, title string) service.Ticket {
	t.Helper()
	tk, err := e.Env.Svc.Tickets.Create(context.Background(), service.UserActor(e.Owner.ID), e.Proj.Key, service.CreateTicketInput{Title: title})
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func errBody(t *testing.T, resp *http.Response) (int, string) {
	t.Helper()
	defer resp.Body.Close()
	var b struct{ Error struct{ Code string } }
	_ = json.NewDecoder(resp.Body).Decode(&b)
	return resp.StatusCode, b.Error.Code
}

// ---- open, headers, event arrives ----

func TestEventsStreamHeadersAndEvent(t *testing.T) {
	e := setupSSE(t, service.HubOptions{}, nil)
	s := e.open(t, e.path("WEB"), e.ownerCookie(t))
	h := s.Resp.Header
	if got := h.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content-type %q", got)
	}
	if got := h.Get("Cache-Control"); got != "no-store, no-transform" {
		t.Fatalf("cache-control %q", got)
	}
	if h.Get("X-Accel-Buffering") != "no" || h.Get("Content-Encoding") != "" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers: %v", h)
	}
	s.until(t, "retry: 3000")
	s.until(t, ": connected")
	e.waitSubs(t, e.Proj.ID, 1)

	tk := e.newTicket(t, "Secret title must not leak")
	name, data := s.frame(t)
	if name != "ticket.created" {
		t.Fatalf("event %q", name)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		t.Fatal(err)
	}
	if m["ticket_id"] != tk.ID || m["project_id"] != e.Proj.ID || m["type"] != "ticket.created" {
		t.Fatalf("data %s", data)
	}
	actor := m["actor"].(map[string]any)
	if len(actor) != 2 || actor["type"] != "user" || actor["id"] != e.Owner.ID {
		t.Fatalf("actor %v", actor)
	}
	allowed := map[string]bool{"type": true, "project_id": true, "ticket_id": true, "comment_id": true, "label_id": true,
		"user_id": true, "renumbered": true, "actor": true, "at": true}
	for k := range m {
		if !allowed[k] {
			t.Fatalf("unexpected key %q", k)
		}
	}
	if strings.Contains(data, "Secret title") {
		t.Fatal("ticket content leaked")
	}
}

func TestEventsKeyOrULIDAndCase(t *testing.T) {
	e := setupSSE(t, service.HubOptions{}, nil)
	c := e.ownerCookie(t)
	streams := []*stream{e.open(t, e.path("WEB"), c), e.open(t, e.path("web"), c), e.open(t, e.path(e.Proj.ID), c)}
	e.waitSubs(t, e.Proj.ID, 3)
	e.newTicket(t, "x")
	for _, s := range streams {
		if n, _ := s.frame(t); n != "ticket.created" {
			t.Fatal(n)
		}
	}
	resp, err := e.get(t, "GET", e.path("NOPE"), c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st, code := errBody(t, resp); st != 404 || code != "not_found" {
		t.Fatalf("unknown key: %d %s", st, code)
	}
}

func TestEventsAuthErrors(t *testing.T) {
	e := setupSSE(t, service.HubOptions{}, nil)
	resp, _ := e.get(t, "GET", e.path("WEB"), "", nil)
	if st, code := errBody(t, resp); st != 401 || code != "unauthorized" {
		t.Fatalf("anon: %d %s", st, code)
	}
	_, secret := e.Env.NewTokenWithSecret(t, e.Owner.ID, service.ScopeWrite, "")
	resp, _ = e.get(t, "GET", e.path("WEB"), "", map[string]string{"Authorization": "Bearer " + secret})
	if st, code := errBody(t, resp); st != 403 || code != "session_required" {
		t.Fatalf("token: %d %s", st, code)
	}
	_, outsider := e.user(t, "out@x.io")
	resp, _ = e.get(t, "GET", e.path("WEB"), outsider, nil)
	if st, code := errBody(t, resp); st != 404 || code != "not_found" {
		t.Fatalf("non-member: %d %s", st, code)
	}
	if e.Hub.SubscriberCount(e.Proj.ID) != 0 {
		t.Fatal("rejected requests subscribed")
	}
}

func TestEventsRouteIsSessionOnly(t *testing.T) {
	e := setupSSE(t, service.HubOptions{}, nil)
	for _, r := range e.Srv.Routes() {
		if r.Pattern == "GET /api/v1/projects/{id}/events" {
			if r.Access != SessionOnly || r.Write {
				t.Fatalf("route %+v", r)
			}
			return
		}
	}
	t.Fatal("events route missing from the route table")
}

func TestEventsOtherProjectMemberReceivesNothing(t *testing.T) {
	e := setupSSE(t, service.HubOptions{}, nil)
	u, cookie := e.user(t, "other@x.io")
	other := e.Env.NewProject(t, u, "OTH")
	s := e.open(t, e.path("OTH"), cookie)
	s.until(t, ": connected")
	e.waitSubs(t, other.ID, 1)
	e.newTicket(t, "in WEB")
	s.noLine(t, 200*time.Millisecond)
}

func TestEventsHeadDoesNotSubscribe(t *testing.T) {
	e := setupSSE(t, service.HubOptions{}, nil)
	resp, err := e.get(t, "HEAD", e.path("WEB"), e.ownerCookie(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || len(b) != 0 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("HEAD: %d %q %v", resp.StatusCode, b, resp.Header)
	}
	if e.Hub.SubscriberCount(e.Proj.ID) != 0 {
		t.Fatal("HEAD subscribed")
	}
	// HEAD for a non-member is still 404
	_, outsider := e.user(t, "out@x.io")
	resp, _ = e.get(t, "HEAD", e.path("WEB"), outsider, nil)
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("HEAD non-member: %d", resp.StatusCode)
	}
}

func TestEventsArchivedAndRoleChangeStayStreamable(t *testing.T) {
	e := setupSSE(t, service.HubOptions{}, nil)
	u, cookie := e.user(t, "ed@x.io")
	e.Env.AddMember(t, e.Proj.ID, u.ID, service.RoleOwner)
	owner := service.UserActor(e.Owner.ID)
	s := e.open(t, e.path("WEB"), cookie)
	e.waitSubs(t, e.Proj.ID, 1)
	if _, err := e.Env.Svc.Members.SetRole(context.Background(), owner, "WEB", u.ID, service.RoleViewer); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.frame(t); n != "member.changed" {
		t.Fatal(n)
	}
	if _, err := e.Env.Svc.Projects.Update(context.Background(), owner, "WEB", service.UpdateProjectInput{Archived: service.Some(true)}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.frame(t); n != "project.updated" {
		t.Fatal(n)
	}
	// opening on the archived project works
	s2 := e.open(t, e.path("WEB"), cookie)
	s2.until(t, ": connected")
}

// ---- access loss ----

func TestEventsRemovedMemberStreamClosesAndReconnect404(t *testing.T) {
	e := setupSSE(t, service.HubOptions{}, nil)
	b, cookie := e.user(t, "b@x.io")
	e.Env.AddMember(t, e.Proj.ID, b.ID, service.RoleEditor)
	s := e.open(t, e.path("WEB"), cookie)
	e.waitSubs(t, e.Proj.ID, 1)
	if err := e.Env.Svc.Members.Remove(context.Background(), service.UserActor(e.Owner.ID), "WEB", b.ID); err != nil {
		t.Fatal(err)
	}
	s.eof(t, sseWait)
	resp, _ := e.get(t, "GET", e.path("WEB"), cookie, nil)
	if st, _ := errBody(t, resp); st != 404 {
		t.Fatalf("reconnect: %d", st)
	}
}

func TestEventsLeaveAndProjectDeleteClose(t *testing.T) {
	e := setupSSE(t, service.HubOptions{}, nil)
	b, cookie := e.user(t, "b@x.io")
	e.Env.AddMember(t, e.Proj.ID, b.ID, service.RoleEditor)
	sb := e.open(t, e.path("WEB"), cookie)
	so := e.open(t, e.path("WEB"), e.ownerCookie(t))
	e.waitSubs(t, e.Proj.ID, 2)
	if err := e.Env.Svc.Members.Remove(context.Background(), service.UserActor(b.ID), "WEB", b.ID); err != nil {
		t.Fatal(err)
	}
	sb.eof(t, sseWait)
	if n, _ := so.frame(t); n != "member.changed" {
		t.Fatal(n)
	}
	if err := e.Env.Svc.Projects.Delete(context.Background(), service.UserActor(e.Owner.ID), "WEB"); err != nil {
		t.Fatal(err)
	}
	so.eof(t, sseWait)
}

// ---- keepalive and tick checks ----

func fastTick(e *sseEnv) { e.Srv.events.keepalive = 40 * time.Millisecond }

func TestEventsKeepalive(t *testing.T) {
	e := setupSSE(t, service.HubOptions{}, fastTick)
	s := e.open(t, e.path("WEB"), e.ownerCookie(t))
	s.until(t, ": keepalive")
	s.until(t, ": keepalive")
}

func TestEventsTickClosesOnMembershipLossWithoutHub(t *testing.T) {
	e := setupSSE(t, service.HubOptions{}, fastTick)
	b, cookie := e.user(t, "b@x.io")
	e.Env.AddMember(t, e.Proj.ID, b.ID, service.RoleViewer)
	s := e.open(t, e.path("WEB"), cookie)
	e.waitSubs(t, e.Proj.ID, 1)
	if err := e.Env.Store.Exec(context.Background(), "DELETE FROM project_members WHERE user_id = ?", b.ID); err != nil {
		t.Fatal(err)
	}
	s.eof(t, sseWait)
}

func TestEventsTickClosesOnSessionLoss(t *testing.T) {
	e := setupSSE(t, service.HubOptions{}, fastTick)
	// logout: the session row is deleted
	cookie := e.ownerCookie(t)
	s := e.open(t, e.path("WEB"), cookie)
	e.waitSubs(t, e.Proj.ID, 1)
	if err := e.Srv.Sessions().Delete(context.Background(), auth.HashToken(cookie)); err != nil {
		t.Fatal(err)
	}
	s.eof(t, sseWait)
	resp, _ := e.get(t, "GET", e.path("WEB"), cookie, nil)
	if st, _ := errBody(t, resp); st != 401 {
		t.Fatalf("reconnect: %d", st)
	}

	// expiry with the injected clock; the tick must not slide the session
	cookie2 := e.ownerCookie(t)
	s2 := e.open(t, e.path("WEB"), cookie2)
	e.waitSubs(t, e.Proj.ID, 1)
	s2.until(t, ": keepalive")
	rec, err := e.Srv.Sessions().Lookup(context.Background(), auth.HashToken(cookie2))
	if err != nil {
		t.Fatal(err)
	}
	e.Env.Clock.Advance(rec.ExpiresAt.Sub(e.Env.Clock.Now()) + time.Second)
	s2.eof(t, sseWait)
}

func TestEventsTickDoesNotSlideSession(t *testing.T) {
	e := setupSSE(t, service.HubOptions{}, fastTick)
	cookie := e.ownerCookie(t)
	before, _ := e.Srv.Sessions().Lookup(context.Background(), auth.HashToken(cookie))
	s := e.open(t, e.path("WEB"), cookie)
	e.Env.Clock.Advance(24 * time.Hour) // a slide would trigger on a normal request now
	for i := 0; i < 3; i++ {
		s.until(t, ": keepalive")
	}
	after, err := e.Srv.Sessions().Lookup(context.Background(), auth.HashToken(cookie))
	if err != nil || !after.ExpiresAt.Equal(before.ExpiresAt) {
		t.Fatalf("session slid: %v -> %v (%v)", before.ExpiresAt, after.ExpiresAt, err)
	}
}

func TestEventsPasswordChangeClosesOtherSessionOnly(t *testing.T) {
	e := setupSSE(t, service.HubOptions{}, fastTick)
	a := e.ownerCookie(t)
	b := e.ownerCookie(t)
	sa := e.open(t, e.path("WEB"), a)
	sb := e.open(t, e.path("WEB"), b)
	e.waitSubs(t, e.Proj.ID, 2)
	if err := e.Env.Svc.Users.SetPassword(context.Background(), e.Owner.ID, "newhash", auth.HashToken(a)); err != nil {
		t.Fatal(err)
	}
	sb.eof(t, sseWait)
	sa.until(t, ": keepalive")
	sa.until(t, ": keepalive")
}

type failSessions struct{}

func (failSessions) Validate(context.Context, string) error { return errors.New("db down") }

type failProjects struct{ service.ProjectService }

func (f failProjects) Resolve(ctx context.Context, a service.Actor, ref string) (service.ProjectRef, error) {
	if ctx.Value(failKey{}) != nil {
		return service.ProjectRef{}, errors.New("db down")
	}
	return f.ProjectService.Resolve(ctx, a, ref)
}

type failKey struct{}

func TestEventsCheckErrorsKeepStreamOpen(t *testing.T) {
	e := setupSSE(t, service.HubOptions{}, fastTick)
	e.Srv.events.sessions = failSessions{}
	s := e.open(t, e.path("WEB"), e.ownerCookie(t))
	for i := 0; i < 4; i++ {
		s.until(t, ": keepalive")
	}
	e.newTicket(t, "still alive")
	if n, _ := s.frame(t); n != "ticket.created" {
		t.Fatal(n)
	}

	// membership check erroring with a non-NotFound error also keeps it open
	e2 := setupSSE(t, service.HubOptions{}, fastTick)
	calls := 0
	var mu sync.Mutex
	e2.Srv.events.projects = countingProjects{ProjectService: e2.Env.Svc.Projects, mu: &mu, n: &calls}
	s2 := e2.open(t, e2.path("WEB"), e2.ownerCookie(t))
	for i := 0; i < 4; i++ {
		s2.until(t, ": keepalive")
	}
	mu.Lock()
	defer mu.Unlock()
	if calls < 3 {
		t.Fatalf("tick membership checks: %d", calls)
	}
}

// countingProjects fails every Resolve after the first two (open check 1 and 2).
type countingProjects struct {
	service.ProjectService
	mu *sync.Mutex
	n  *int
}

func (c countingProjects) Resolve(ctx context.Context, a service.Actor, ref string) (service.ProjectRef, error) {
	c.mu.Lock()
	*c.n++
	n := *c.n
	c.mu.Unlock()
	if n > 2 {
		return service.ProjectRef{}, errors.New("db down")
	}
	return c.ProjectService.Resolve(ctx, a, ref)
}

// ---- slow client, cleanup, deadlines ----

func TestEventsSlowClientDropped(t *testing.T) {
	hold := make(chan struct{})
	var once sync.Once
	e := setupSSE(t, service.HubOptions{StreamBuffer: 2}, func(e *sseEnv) {
		e.Srv.events.beforeLoop = func() { <-hold }
	})
	s := e.open(t, e.path("WEB"), e.ownerCookie(t))
	e.waitSubs(t, e.Proj.ID, 1)
	for i := 0; i < 5; i++ {
		e.newTicket(t, "t")
	}
	e.waitSubs(t, e.Proj.ID, 0) // dropped by the hub
	once.Do(func() { close(hold) })
	s.eof(t, sseWait)
}

func TestEventsDisconnectCleanup(t *testing.T) {
	e := setupSSE(t, service.HubOptions{}, nil)
	c := e.ownerCookie(t)
	cycle := func() {
		resp, err := e.get(t, "GET", e.path("WEB"), c, nil)
		if err != nil {
			t.Fatal(err)
		}
		br := bufio.NewReader(resp.Body)
		for {
			l, err := br.ReadString('\n')
			if err != nil || strings.HasPrefix(l, ": connected") {
				break
			}
		}
		resp.Body.Close()
	}
	cycle()
	e.waitSubs(t, e.Proj.ID, 0)
	base := runtime.NumGoroutine()
	for i := 0; i < 20; i++ {
		cycle()
	}
	e.waitSubs(t, e.Proj.ID, 0)
	e.TS.CloseClientConnections()
	deadline := time.Now().Add(sseWait)
	for runtime.NumGoroutine() > base+5 {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines grew: %d -> %d", base, runtime.NumGoroutine())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// deadlineWriter is a fake ResponseWriter recording deadlines and failing after failAfter writes.
type deadlineWriter struct {
	mu        sync.Mutex
	h         http.Header
	events    []string // "deadline:<d>" and "write"
	writes    int
	failAfter int
}

func (w *deadlineWriter) Header() http.Header { return w.h }
func (w *deadlineWriter) WriteHeader(int)     {}
func (w *deadlineWriter) FlushError() error   { return nil }
func (w *deadlineWriter) SetReadDeadline(time.Time) error {
	return nil
}
func (w *deadlineWriter) SetWriteDeadline(t time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = append(w.events, "deadline:"+time.Until(t).Round(time.Second).String())
	return nil
}
func (w *deadlineWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes++
	w.events = append(w.events, "write")
	if w.failAfter > 0 && w.writes > w.failAfter {
		return 0, errors.New("broken pipe")
	}
	return len(b), nil
}

func TestEventsPerFrameWriteDeadlineAndWriteError(t *testing.T) {
	e := setupSSE(t, service.HubOptions{}, nil)
	h := newEventsHandler(e.Hub, e.Env.Svc.Projects, e.Srv.sessions, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.keepalive, h.writeTimeout = 10*time.Millisecond, 7*time.Second
	w := &deadlineWriter{h: http.Header{}, failAfter: 3}
	req := httptest.NewRequest("GET", "/api/v1/projects/WEB/events", nil)
	req.SetPathValue("id", "WEB")
	cookie := e.ownerCookie(t)
	req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{
		User: auth.AuthUser{ID: e.Owner.ID}, Method: auth.MethodSession,
		Session: &auth.SessionInfo{TokenHash: auth.HashToken(cookie)},
	}))
	done := make(chan struct{})
	go func() { h.ServeHTTP(w, req); close(done) }()
	select {
	case <-done: // the failing write ended the handler
	case <-time.After(sseWait):
		t.Fatal("handler did not stop on write error")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for i, ev := range w.events {
		if ev == "write" && (i == 0 || w.events[i-1] != "deadline:7s") {
			t.Fatalf("write %d without a preceding 7s deadline: %v", i, w.events)
		}
	}
	if w.writes < 3 {
		t.Fatalf("writes: %d %v %v", w.writes, w.events, w.h)
	}
	if e.Hub.SubscriberCount(e.Proj.ID) != 0 {
		t.Fatal("subscription leaked")
	}
}

// ---- middleware audit (WP5) ----

// A server with ReadTimeout and WriteTimeout of 200 ms: the stream must outlive both, which
// proves the cleared read deadline and the per-frame write deadline through the real chain.
func TestEventsSurvivesServerTimeouts(t *testing.T) {
	e := setupSSE(t, service.HubOptions{}, func(e *sseEnv) {
		e.TS.Config.ReadTimeout = 200 * time.Millisecond
		e.TS.Config.WriteTimeout = 200 * time.Millisecond
		e.Srv.events.keepalive = 100 * time.Millisecond
	})
	s := e.open(t, e.path("WEB"), e.ownerCookie(t))
	s.until(t, ": connected")
	time.Sleep(700 * time.Millisecond)
	e.newTicket(t, "late")
	if n, _ := s.frame(t); n != "ticket.created" {
		t.Fatal(n)
	}
}

// Frames must arrive before the response ends (flush reaches the connection through the chain),
// and an Accept-Encoding: gzip request must not get a compressed stream.
func TestEventsFlushesThroughChainWithoutCompression(t *testing.T) {
	e := setupSSE(t, service.HubOptions{}, nil)
	req, _ := http.NewRequest("GET", e.TS.URL+e.path("WEB"), nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: e.ownerCookie(t)})
	req.Header.Set("Accept-Encoding", "gzip")
	tr := &http.Transport{DisableCompression: true}
	defer tr.CloseIdleConnections()
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Encoding") != "" || resp.Header.Get("Cache-Control") != "no-store, no-transform" {
		t.Fatalf("headers: %v", resp.Header)
	}
	br := bufio.NewReader(resp.Body)
	got := make(chan string, 1)
	go func() { l, _ := br.ReadString('\n'); got <- l }()
	select {
	case l := <-got:
		if !strings.HasPrefix(l, "retry:") {
			t.Fatalf("first line %q", l)
		}
	case <-time.After(sseWait):
		t.Fatal("preamble not flushed while the response is open")
	}
}

// ---- shutdown (WP6, hub part at the HTTP level) ----

func TestEventsHubShutdownEndsStreamsAndRejectsNew(t *testing.T) {
	e := setupSSE(t, service.HubOptions{}, nil)
	c := e.ownerCookie(t)
	s := e.open(t, e.path("WEB"), c)
	e.waitSubs(t, e.Proj.ID, 1)
	e.Hub.Shutdown()
	s.eof(t, sseWait)

	resp, err := e.get(t, "GET", e.path("WEB"), c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st, code := errBody(t, resp); st != 503 || code != "unavailable" {
		t.Fatalf("after shutdown: %d %s", st, code)
	}
	// http.Server.Shutdown now returns promptly: no handler is pinned by a stream.
	ctx, cancel := context.WithTimeout(context.Background(), sseWait)
	defer cancel()
	start := time.Now()
	if err := e.TS.Config.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("Shutdown was slow")
	}
}
