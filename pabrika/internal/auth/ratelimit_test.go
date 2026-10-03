package auth_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RJGJ/Pabrika/internal/auth"
	"github.com/RJGJ/Pabrika/internal/testutil"
)

func TestLimiterFixedWindow(t *testing.T) {
	clock := testutil.NewFakeClock()
	l := auth.NewLimiter(5, time.Minute, 0, clock.Now)
	for i := 1; i <= 5; i++ {
		if ok, _ := l.Allow("k"); !ok {
			t.Fatalf("attempt %d denied", i)
		}
	}
	ok, retry := l.Allow("k")
	if ok || retry != time.Minute {
		t.Fatalf("6th = %v %v", ok, retry)
	}
	// denied attempts keep counting, retry-after shrinks whole seconds, never below 1 s
	clock.Advance(20*time.Second + 500*time.Millisecond)
	if ok, retry := l.Allow("k"); ok || retry != 40*time.Second {
		t.Fatalf("after 20.5s = %v %v", ok, retry)
	}
	clock.Advance(39*time.Second + 400*time.Millisecond)
	if ok, retry := l.Allow("k"); ok || retry != time.Second {
		t.Fatalf("near end = %v %v", ok, retry)
	}
	// window resets
	clock.Advance(time.Second)
	for i := 1; i <= 5; i++ {
		if ok, _ := l.Allow("k"); !ok {
			t.Fatalf("after reset attempt %d denied", i)
		}
	}
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("limit not re-applied after reset")
	}
}

func TestLimiterSeparateBuckets(t *testing.T) {
	clock := testutil.NewFakeClock()
	l := auth.NewLimiter(1, time.Minute, 0, clock.Now)
	if ok, _ := l.Allow(auth.LoginKey("1.1.1.1", "a@x.io")); !ok {
		t.Fatal()
	}
	if ok, _ := l.Allow(auth.LoginKey("1.1.1.1", " A@X.io ")); ok {
		t.Fatal("email not normalised in key")
	}
	for _, k := range []string{auth.LoginKey("1.1.1.1", "b@x.io"), auth.LoginKey("2.2.2.2", "a@x.io"), auth.LoginKey("1.1.1.1", "")} {
		if ok, _ := l.Allow(k); !ok {
			t.Fatalf("key %q wrongly shared", k)
		}
	}
	if auth.PasswordKey("U1", "1.1.1.1") == auth.PasswordKey("U1", "2.2.2.2") || auth.PasswordKey("U1", "i") == auth.PasswordKey("U2", "i") {
		t.Fatal("password keys collide")
	}
	// separate limiters are separate buckets
	l2 := auth.NewLimiter(1, time.Minute, 0, clock.Now)
	if ok, _ := l2.Allow(auth.LoginKey("1.1.1.1", "a@x.io")); !ok {
		t.Fatal("signup limiter shares state with login limiter")
	}
}

func TestLimiterCapEvictsOldest(t *testing.T) {
	clock := testutil.NewFakeClock()
	l := auth.NewLimiter(1, time.Hour, 3, clock.Now)
	for _, k := range []string{"a", "b", "c"} {
		l.Allow(k)
		clock.Advance(time.Second)
	}
	l.Allow("d") // evicts a
	if l.Len() != 3 {
		t.Fatalf("len = %d", l.Len())
	}
	if ok, _ := l.Allow("a"); !ok { // a was forgotten (evicts b)
		t.Fatal("oldest key was not evicted")
	}
	if ok, _ := l.Allow("c"); ok { // c is still tracked and spent
		t.Fatal("recent key was evicted")
	}
	for i := 0; i < 100; i++ {
		l.Allow(fmt.Sprintf("flood-%d", i))
	}
	if l.Len() != 3 {
		t.Fatalf("len after flood = %d", l.Len())
	}
}

func TestLimiterSweep(t *testing.T) {
	clock := testutil.NewFakeClock()
	l := auth.NewLimiter(1, time.Minute, 0, clock.Now)
	l.Allow("old1")
	l.Allow("old2")
	clock.Advance(40 * time.Second)
	l.Allow("fresh")
	if n := l.Sweep(); n != 0 {
		t.Fatalf("early sweep removed %d", n)
	}
	clock.Advance(30 * time.Second) // old* are 70 s old, fresh 30 s
	if n := l.Sweep(); n != 2 || l.Len() != 1 {
		t.Fatalf("sweep removed %d, len %d", n, l.Len())
	}
	if ok, _ := l.Allow("fresh"); ok {
		t.Fatal("fresh bucket was swept")
	}
	// a key that reset its window moves behind newer entries and is not swept early
	clock.Advance(time.Minute)
	l.Allow("fresh") // new window starts now
	l.Allow("other")
	clock.Advance(30 * time.Second)
	if n := l.Sweep(); n != 0 {
		t.Fatalf("swept live buckets: %d", n)
	}
}

func req(remote string, hdr ...string) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = remote
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Add(hdr[i], hdr[i+1])
	}
	return r
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		name  string
		r     *http.Request
		trust bool
		want  string
	}{
		{"plain v4 with port", req("203.0.113.9:5555"), false, "203.0.113.9"},
		{"v4 no port", req("203.0.113.9"), false, "203.0.113.9"},
		{"mapped v4", req("[::ffff:203.0.113.9]:80"), false, "203.0.113.9"},
		{"v6 collapses to /64", req("[2001:db8:1:2:aaaa:bbbb:cccc:dddd]:80"), false, "2001:db8:1:2::/64"},
		{"v6 zone stripped", req("[fe80::1%eth0]:80"), false, "fe80::/64"},
		{"spoofed XFF ignored when flag false", req("203.0.113.9:1", "X-Forwarded-For", "198.51.100.7"), false, "203.0.113.9"},
		{"XFF first hop", req("10.0.0.1:1", "X-Forwarded-For", "198.51.100.7, 10.0.0.2"), true, "198.51.100.7"},
		{"XFF first hop trimmed", req("10.0.0.1:1", "X-Forwarded-For", "  198.51.100.7  ,x"), true, "198.51.100.7"},
		{"XFF only first line counts", req("10.0.0.1:1", "X-Forwarded-For", "198.51.100.7", "X-Forwarded-For", "192.0.2.1"), true, "198.51.100.7"},
		{"XFF with port", req("10.0.0.1:1", "X-Forwarded-For", "198.51.100.7:4444"), true, "198.51.100.7"},
		{"XFF mapped v4", req("10.0.0.1:1", "X-Forwarded-For", "::ffff:198.51.100.7"), true, "198.51.100.7"},
		{"XFF v6", req("10.0.0.1:1", "X-Forwarded-For", "2001:db8::5"), true, "2001:db8::/64"},
		{"XFF garbage falls back", req("10.0.0.1:1", "X-Forwarded-For", "not-an-ip"), true, "10.0.0.1"},
		{"XFF empty first entry falls back", req("10.0.0.1:1", "X-Forwarded-For", ", 198.51.100.7"), true, "10.0.0.1"},
		{"XFF absent falls back", req("10.0.0.1:1"), true, "10.0.0.1"},
		{"unparsable remote kept raw", req("weird"), false, "weird"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := auth.ClientIP(c.r, c.trust); got != c.want {
				t.Fatalf("ClientIP = %q, want %q", got, c.want)
			}
		})
	}
	// two addresses in the same /64 share a key; different /64s do not
	a := auth.ClientIP(req("[2001:db8:1:2::1]:1"), false)
	b := auth.ClientIP(req("[2001:db8:1:2:ffff:ffff:ffff:ffff]:1"), false)
	c := auth.ClientIP(req("[2001:db8:1:3::1]:1"), false)
	if a != b || a == c {
		t.Fatalf("/64 collapse: %q %q %q", a, b, c)
	}
}
