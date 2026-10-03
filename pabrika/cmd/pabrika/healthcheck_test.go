package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func portOf(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Port()
}

func TestHealthcheck(t *testing.T) {
	status := 200
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			w.WriteHeader(404)
			return
		}
		w.WriteHeader(status)
	}))
	defer ts.Close()
	port := portOf(t, ts.URL)

	getenv := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	var errb bytes.Buffer
	// Healthy: exit 0, silent. BASE_URL garbage must not matter (only PORT is read).
	if code := runHealthcheck(getenv(map[string]string{"PORT": port, "BASE_URL": "::not a url::", "ALLOW_SIGNUP": "banana"}), &errb, time.Second); code != 0 || errb.Len() != 0 {
		t.Fatalf("healthy: %d %q", code, errb.String())
	}
	// Unhealthy.
	status = 503
	if code := runHealthcheck(getenv(map[string]string{"PORT": port}), &errb, time.Second); code != 1 || !strings.Contains(errb.String(), "503") {
		t.Fatalf("503: %d %q", code, errb.String())
	}
	// Redirects are not followed or treated as healthy.
	status = 302
	if code := runHealthcheck(getenv(map[string]string{"PORT": port}), &errb, time.Second); code != 1 {
		t.Fatalf("302: %d", code)
	}

	// Closed port.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := portOf(t, "http://"+l.Addr().String())
	l.Close()
	if code := runHealthcheck(getenv(map[string]string{"PORT": closed}), &errb, time.Second); code != 1 {
		t.Fatalf("closed port: %d", code)
	}
	// Bad PORT.
	for _, p := range []string{"abc", "0", "70000"} {
		if code := runHealthcheck(getenv(map[string]string{"PORT": p}), &errb, time.Second); code != 1 {
			t.Fatalf("PORT=%s: %d", p, code)
		}
	}
}

func TestHealthcheckTimesOut(t *testing.T) {
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer ts.Close()
	defer close(release)
	start := time.Now()
	var errb bytes.Buffer
	code := runHealthcheck(func(k string) string {
		if k == "PORT" {
			return portOf(t, ts.URL)
		}
		return ""
	}, &errb, 150*time.Millisecond)
	if code != 1 || time.Since(start) > 2*time.Second {
		t.Fatalf("code %d after %v", code, time.Since(start))
	}
}
