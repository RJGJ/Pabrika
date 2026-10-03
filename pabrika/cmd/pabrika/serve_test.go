package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/store"
)

type order struct {
	mu sync.Mutex
	l  []string
}

func (o *order) add(s string) { o.mu.Lock(); o.l = append(o.l, s); o.mu.Unlock() }
func (o *order) get() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.l...)
}

type closerFunc func() error

func (f closerFunc) Close() error { return f() }

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

// hangHandler blocks /hang until release is closed and reports when it has started.
func hangHandler(started chan<- struct{}, release <-chan struct{}) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/hang", func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-release
		_, _ = w.Write([]byte("done"))
	})
	return mux
}

func TestGracefulShutdownOrder(t *testing.T) {
	var ord order
	ln := listen(t)
	started, release := make(chan struct{}, 1), make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bgStop := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- runServer(ctx, serveParams{
			Listener: ln, Handler: hangHandler(started, release), Log: quietLog(),
			StartBackground: func(bg context.Context) <-chan struct{} {
				go func() { <-bg.Done(); ord.add("background"); close(bgStop) }()
				return bgStop
			},
			// The hook ends the "stream": it releases the hung request, as hub.Shutdown ends SSE.
			ShutdownHook:    func() { ord.add("hook"); close(release) },
			Close:           closerFunc(func() error { ord.add("close"); return nil }),
			ShutdownTimeout: 5 * time.Second,
		})
	}()

	type res struct {
		body string
		err  error
	}
	got := make(chan res, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/hang")
		if err != nil {
			got <- res{err: err}
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		got <- res{body: string(b)}
	}()
	<-started
	begin := time.Now()
	cancel()

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("runServer: %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("shutdown did not finish")
	}
	if time.Since(begin) > 3*time.Second {
		t.Fatalf("took %v: the hook should have let Shutdown finish promptly", time.Since(begin))
	}
	// The in-flight request completed normally: Shutdown (not Close) ran after the hook.
	if r := <-got; r.err != nil || r.body != "done" {
		t.Fatalf("in-flight request: %+v", r)
	}
	if want := []string{"background", "hook", "close"}; strings.Join(ord.get(), ",") != strings.Join(want, ",") {
		t.Fatalf("order %v, want %v", ord.get(), want)
	}
	if _, err := net.DialTimeout("tcp", ln.Addr().String(), 200*time.Millisecond); err == nil {
		t.Fatal("listener still open")
	}
}

func TestShutdownDeadlineFallsBackToClose(t *testing.T) {
	var ord order
	ln := listen(t)
	started, release := make(chan struct{}, 1), make(chan struct{})
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- runServer(ctx, serveParams{
			Listener: ln, Handler: hangHandler(started, release), Log: quietLog(),
			ShutdownHook:    func() { ord.add("hook") }, // does not release the request
			Close:           closerFunc(func() error { ord.add("close"); return nil }),
			ShutdownTimeout: 200 * time.Millisecond,
		})
	}()
	clientErr := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/hang")
		if err == nil {
			resp.Body.Close()
		}
		clientErr <- err
	}()
	<-started
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("a missed deadline must still return nil, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runServer hung past the deadline")
	}
	if got := strings.Join(ord.get(), ","); got != "hook,close" {
		t.Fatalf("order %s", got)
	}
	select {
	case err := <-clientErr:
		if err == nil {
			t.Fatal("the hung request should have been cut by Close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client still waiting: connections were not closed")
	}
}

func TestShutdownCheckpointsStore(t *testing.T) {
	// A real file database: data written before shutdown is there after the store is closed.
	path := filepath.Join(t.TempDir(), "p.db")
	st, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc := service.New(st, service.Deps{})
	if _, err := svc.Users.Create(context.Background(), "a@x.io", "A", "hash"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // shut down right after start
	if err := runServer(ctx, serveParams{Listener: listen(t), Handler: http.NotFoundHandler(), Log: quietLog(), Close: st}); err != nil {
		t.Fatal(err)
	}
	st2, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if _, _, err := service.New(st2, service.Deps{}).Users.Credentials(context.Background(), "a@x.io"); err != nil {
		t.Fatalf("data lost: %v", err)
	}
}

func TestRunServerListenError(t *testing.T) {
	ln := listen(t)
	addr := ln.Addr().String()
	var closed bool
	err := runServer(context.Background(), serveParams{
		Addr: addr, Handler: http.NotFoundHandler(), Log: quietLog(), // already in use
		Close: closerFunc(func() error { closed = true; return nil }),
	})
	ln.Close()
	if err == nil || !closed {
		t.Fatalf("err=%v closed=%v", err, closed)
	}

	// A listener that fails: the error is returned after cleanup.
	bad := listen(t)
	bad.Close()
	closed = false
	err = runServer(context.Background(), serveParams{Listener: bad, Handler: http.NotFoundHandler(), Log: quietLog(),
		Close: closerFunc(func() error { closed = true; return nil })})
	if err == nil || errors.Is(err, http.ErrServerClosed) || !closed {
		t.Fatalf("err=%v closed=%v", err, closed)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln := listen(t)
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// TestServeEndToEnd runs the real `serve` command: real config, store, migrations, services,
// router, healthcheck, then SIGTERM-equivalent shutdown with exit 0.
func TestServeEndToEnd(t *testing.T) {
	port := strconv.Itoa(freePort(t))
	e, _, errb, dbPath := testEnv(t, "", map[string]string{"PORT": port, "COOKIE_SECURE": "false"})
	var logs bytes.Buffer
	e.stderr = &logs
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	code := make(chan int, 1)
	go func() { code <- run(ctx, []string{"serve"}, e) }()

	deadline := time.Now().Add(10 * time.Second)
	healthy := false
	for time.Now().Before(deadline) {
		if runHealthcheck(e.getenv, io.Discard, time.Second) == 0 {
			healthy = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !healthy {
		cancel()
		<-code
		t.Fatalf("server never became healthy: %s %s", errb, logs.String())
	}

	// The security headers and JSON 404 are in place on the real server.
	resp, err := http.Get("http://127.0.0.1:" + port + "/api/v1/nope")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 || resp.Header.Get("Content-Security-Policy") == "" || resp.Header.Get("X-Request-Id") == "" {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}

	// The SPA fallback is wired: a UI path answers with the bundle (200) or, in a checkout
	// without a build, the 503 "UI not built" page; never the plain mux 404.
	resp, err = http.Get("http://127.0.0.1:" + port + "/p/WEB/t/1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 503 {
		t.Fatalf("SPA path status = %d", resp.StatusCode)
	}

	cancel()
	select {
	case c := <-code:
		if c != 0 {
			t.Fatalf("exit %d: %s", c, logs.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not exit")
	}
	if !strings.Contains(logs.String(), "pabrika starting") || !strings.Contains(logs.String(), "version=") {
		t.Fatalf("startup log line missing: %s", logs.String())
	}
	if _, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 200*time.Millisecond); err == nil {
		t.Fatal("still listening")
	}
	// The database is intact and migrated (idempotent restart).
	if !strings.Contains(logs.String(), "schema_version=") {
		t.Fatalf("log missing schema version: %s", logs.String())
	}
	_ = dbPath
}
