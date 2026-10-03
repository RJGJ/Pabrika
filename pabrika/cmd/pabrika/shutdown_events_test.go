package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/RJGJ/Pabrika/internal/auth"
	"github.com/RJGJ/Pabrika/internal/config"
	"github.com/RJGJ/Pabrika/internal/httpapi"
	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/testutil"
)

// Shutdown with the REAL hub and the real httpapi handler: srv.Shutdown never cancels request
// contexts, so an open event stream would pin it until the deadline unless the hook (hub
// Shutdown) runs first.

type sseFixture struct {
	cookie  string
	hook    func()
	handler http.Handler
}

func newSSEFixture(t *testing.T, hookDelay time.Duration) sseFixture {
	t.Helper()
	env := testutil.NewTestServices(t, testutil.WithHub(service.HubOptions{}))
	u := env.NewUser(t, "o@x.io", "O")
	env.NewProject(t, u, "WEB")
	srv := httpapi.New(httpapi.Deps{
		Config: config.Config{Port: 8080, DBPath: "unused", BaseURL: "http://localhost:8080"},
		Store:  env.Store, Services: env.Svc, Now: env.Clock.Now, Hub: env.Hub,
		Hasher: auth.NewHasher(auth.TestParams, 0), Logger: quietLog(),
	})
	return sseFixture{
		cookie:  env.NewSession(t, u.ID),
		handler: srv.Handler(),
		hook:    func() { env.Hub.Shutdown(); time.Sleep(hookDelay) },
	}
}

func (f sseFixture) get(addr string) (*http.Response, error) {
	req, _ := http.NewRequest("GET", "http://"+addr+"/api/v1/projects/WEB/events", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: f.cookie})
	return http.DefaultClient.Do(req)
}

func TestShutdownEndsOpenEventStreamCleanly(t *testing.T) {
	f := newSSEFixture(t, 0)
	ln := listen(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- runServer(ctx, serveParams{Listener: ln, Handler: f.handler, Log: quietLog(), ShutdownHook: f.hook, ShutdownTimeout: 5 * time.Second})
	}()
	resp, err := f.get(ln.Addr().String())
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("open: %v %v", resp, err)
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	for {
		l, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(l, ": connected") {
			break
		}
	}
	begin := time.Now()
	cancel()
	rest, err := io.ReadAll(br) // a clean EOF, not a reset
	if err != nil {
		t.Fatalf("stream did not end cleanly: %v", err)
	}
	if strings.Contains(string(rest), "event:") {
		t.Fatalf("frames after shutdown: %q", rest)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("runServer did not return")
	}
	if time.Since(begin) > 3*time.Second {
		t.Fatalf("shutdown took %v with a stream open", time.Since(begin))
	}
}

func TestShutdownNewStreamGets503(t *testing.T) {
	f := newSSEFixture(t, 600*time.Millisecond) // keep the listener up briefly after the hub is down
	ln := listen(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- runServer(ctx, serveParams{Listener: ln, Handler: f.handler, Log: quietLog(), ShutdownHook: f.hook, ShutdownTimeout: 5 * time.Second})
	}()
	waitUp := time.Now().Add(2 * time.Second)
	for {
		if resp, err := http.Get("http://" + ln.Addr().String() + "/healthz"); err == nil {
			resp.Body.Close()
			break
		} else if time.Now().After(waitUp) {
			t.Fatal(err)
		}
	}
	cancel()
	time.Sleep(200 * time.Millisecond) // hub is shut down, srv.Shutdown has not started yet
	resp, err := f.get(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b struct{ Error struct{ Code string } }
	_ = json.NewDecoder(resp.Body).Decode(&b)
	if resp.StatusCode != 503 || b.Error.Code != "unavailable" {
		t.Fatalf("new connect: %d %s", resp.StatusCode, b.Error.Code)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}
