package auth

import (
	"container/list"
	"context"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// DefaultMaxKeys caps the limiter's memory.
const DefaultMaxKeys = 10000

type bucket struct {
	key   string
	start time.Time
	count int
}

// Limiter is an in-memory fixed-window limiter: at most `limit` attempts per `window` per key.
// Entries are kept in window-start order so eviction (Sweep, and the hard cap) is O(1) per entry.
type Limiter struct {
	mu      sync.Mutex
	now     func() time.Time
	limit   int
	window  time.Duration
	maxKeys int
	items   map[string]*list.Element // value: *bucket
	order   *list.List               // oldest window start at the front
}

// NewLimiter builds a limiter. now == nil uses time.Now; maxKeys <= 0 uses DefaultMaxKeys.
func NewLimiter(limit int, window time.Duration, maxKeys int, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	if maxKeys <= 0 {
		maxKeys = DefaultMaxKeys
	}
	return &Limiter{now: now, limit: limit, window: window, maxKeys: maxKeys,
		items: map[string]*list.Element{}, order: list.New()}
}

// Allow records one attempt for key (every attempt counts, allowed or not) and reports whether
// it is within the limit. When denied, retryAfter is the whole seconds (at least 1 s) until
// the key's window resets.
func (l *Limiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	el, found := l.items[key]
	if found {
		b := el.Value.(*bucket)
		if now.Sub(b.start) >= l.window {
			b.start, b.count = now, 0
			l.order.MoveToBack(el)
		}
	} else {
		for len(l.items) >= l.maxKeys {
			l.evictFront()
		}
		el = l.order.PushBack(&bucket{key: key, start: now})
		l.items[key] = el
	}
	b := el.Value.(*bucket)
	b.count++
	if b.count <= l.limit {
		return true, 0
	}
	remaining := b.start.Add(l.window).Sub(now)
	secs := (remaining + time.Second - 1) / time.Second
	if secs < 1 {
		secs = 1
	}
	return false, secs * time.Second
}

func (l *Limiter) evictFront() {
	if el := l.order.Front(); el != nil {
		delete(l.items, el.Value.(*bucket).key)
		l.order.Remove(el)
	}
}

// Sweep drops every bucket whose window has ended and returns how many were removed.
func (l *Limiter) Sweep() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	n := 0
	for el := l.order.Front(); el != nil; el = l.order.Front() {
		if now.Sub(el.Value.(*bucket).start) < l.window {
			break
		}
		l.evictFront()
		n++
	}
	return n
}

// Len is the number of tracked keys.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.items)
}

// RunSweeper calls Sweep every interval until ctx is done.
func (l *Limiter) RunSweeper(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.Sweep()
		}
	}
}

// LoginKey is the bucket key for login and signup: (client IP, trimmed lowercased email).
// Use one Limiter per action so the buckets are separate.
func LoginKey(ip, email string) string {
	return ip + "|" + strings.ToLower(strings.TrimSpace(email))
}

// PasswordKey is the bucket key for password change: (user id, client IP).
func PasswordKey(userID, ip string) string { return userID + "|" + ip }

// ClientIP is the address the limiter and the request log key on. By default it is the host
// of RemoteAddr; with trustProxy it is the first entry of the first X-Forwarded-For line, if
// that parses as an IP (otherwise RemoteAddr). Ports and zones are stripped, IPv4-in-IPv6 is
// unmapped, and an IPv6 address collapses to its /64 prefix (formatted "2001:db8::/64") so
// rotating the low 64 bits does not evade a limit. Unparsable input yields its raw text.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if v := r.Header.Get("X-Forwarded-For"); v != "" {
			first, _, _ := strings.Cut(v, ",")
			if a, ok := parseHostAddr(strings.TrimSpace(first)); ok {
				return collapse(a)
			}
		}
	}
	if a, ok := parseHostAddr(r.RemoteAddr); ok {
		return collapse(a)
	}
	return strings.TrimSpace(r.RemoteAddr)
}

// parseHostAddr parses "ip", "ip:port", "[v6]:port", "[v6]" or "v6%zone".
func parseHostAddr(s string) (netip.Addr, bool) {
	if s == "" {
		return netip.Addr{}, false
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().WithZone("").Unmap(), true
	}
	if a, err := netip.ParseAddr(strings.Trim(s, "[]")); err == nil {
		return a.WithZone("").Unmap(), true
	}
	return netip.Addr{}, false
}

func collapse(a netip.Addr) string {
	if a.Is6() {
		p, _ := a.Prefix(64)
		return p.String()
	}
	return a.String()
}
