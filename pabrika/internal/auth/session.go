package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/RJGJ/Pabrika/internal/store"
	"github.com/RJGJ/Pabrika/internal/store/db"
)

const (
	// SessionCookieName is the session cookie.
	SessionCookieName = "pb_session"
	// SessionLifetime is the sliding session lifetime (30 days).
	SessionLifetime = 30 * 24 * time.Hour
	// SlideAfter is how stale an expiry must be before it is extended again (one hour), so
	// reads do not write on every request.
	SlideAfter = time.Hour
	// PurgeInterval is how often expired sessions are deleted.
	PurgeInterval = time.Hour

	sessionValueLen = 43 // 32 random bytes, base64url without padding
)

// SessionOptions configure Sessions. Zero values: Now = time.Now, CookieSecure = false,
// Logger = slog.Default().
type SessionOptions struct {
	Now          func() time.Time
	CookieSecure bool
	Logger       *slog.Logger
}

// Sessions persists sessions (only the SHA-256 of the cookie value is stored) and builds cookies.
type Sessions struct {
	st     *store.Store
	now    func() time.Time
	secure bool
	log    *slog.Logger
}

// NewSessions builds a Sessions on an opened, migrated store.
func NewSessions(st *store.Store, o SessionOptions) *Sessions {
	s := &Sessions{st: st, now: o.Now, secure: o.CookieSecure, log: o.Logger}
	if s.now == nil {
		s.now = func() time.Time { return time.Now().UTC() }
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	return s
}

// SessionRecord is a stored session with its user.
type SessionRecord struct {
	TokenHash string
	User      AuthUser
	ExpiresAt time.Time
	CreatedAt time.Time
}

// HashSessionValue is the stored form of a cookie value.
func HashSessionValue(value string) string { return HashToken(value) }

// validSessionValue is a cheap shape check so garbage cookies never reach the database.
func validSessionValue(v string) bool {
	if len(v) != sessionValueLen {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(v)
	return err == nil && len(b) == 32
}

// Create starts a new session for userID with a fresh random value (never client-chosen) and
// returns the cookie value (to be sent once) and the expiry.
func (s *Sessions) Create(ctx context.Context, userID string) (value string, expires time.Time, err error) {
	return s.Rotate(ctx, userID, "")
}

// Rotate is Create that also deletes the session behind oldValue (a cookie the login request
// already carried) in the same transaction. An empty or unknown oldValue deletes nothing.
func (s *Sessions) Rotate(ctx context.Context, userID, oldValue string) (value string, expires time.Time, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	value = base64.RawURLEncoding.EncodeToString(raw)
	now := s.now().UTC()
	expires = now.Add(SessionLifetime)
	err = s.st.WithTx(ctx, func(q *db.Queries) error {
		if validSessionValue(oldValue) {
			if _, err := q.SessionDeleteByHash(ctx, HashSessionValue(oldValue)); err != nil {
				return err
			}
		}
		return q.CreateSession(ctx, db.CreateSessionParams{
			TokenHash: HashSessionValue(value), UserID: userID,
			ExpiresAt: store.FormatTime(expires), CreatedAt: store.FormatTime(now),
		})
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return value, expires, nil
}

// Lookup returns the live session with the given hash. Missing and expired both give
// ErrInvalidCredentials; any other error is a database failure.
func (s *Sessions) Lookup(ctx context.Context, tokenHash string) (SessionRecord, error) {
	row, err := s.st.Read().SessionGetByHash(ctx, tokenHash)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionRecord{}, ErrInvalidCredentials
	}
	if err != nil {
		return SessionRecord{}, err
	}
	exp, err := store.ParseTime(row.ExpiresAt)
	if err != nil || !exp.After(s.now()) {
		return SessionRecord{}, ErrInvalidCredentials
	}
	created, _ := store.ParseTime(row.CreatedAt)
	userCreated, _ := store.ParseTime(row.UserCreatedAt)
	return SessionRecord{
		TokenHash: row.TokenHash,
		User:      AuthUser{ID: row.UserID, Email: row.Email, DisplayName: row.DisplayName, CreatedAt: userCreated},
		ExpiresAt: exp, CreatedAt: created,
	}, nil
}

// NeedsSlide reports whether the expiry was last extended more than SlideAfter ago, i.e.
// expires_at - now < lifetime - 1h.
func (s *Sessions) NeedsSlide(rec SessionRecord) bool {
	return rec.ExpiresAt.Sub(s.now()) < SessionLifetime-SlideAfter
}

// Slide extends the session to now + 30 days when NeedsSlide; otherwise it does nothing and
// writes nothing. The bool says whether it extended.
func (s *Sessions) Slide(ctx context.Context, rec SessionRecord) (SessionRecord, bool, error) {
	if !s.NeedsSlide(rec) {
		return rec, false, nil
	}
	exp := s.now().UTC().Add(SessionLifetime)
	err := s.st.WithTx(ctx, func(q *db.Queries) error {
		_, err := q.SessionExtend(ctx, db.SessionExtendParams{ExpiresAt: store.FormatTime(exp), TokenHash: rec.TokenHash})
		return err
	})
	if err != nil {
		return rec, false, err
	}
	rec.ExpiresAt = exp
	return rec, true, nil
}

// Delete removes a session by its stored hash (idempotent).
func (s *Sessions) Delete(ctx context.Context, tokenHash string) error {
	return s.st.WithTx(ctx, func(q *db.Queries) error {
		_, err := q.SessionDeleteByHash(ctx, tokenHash)
		return err
	})
}

// PurgeExpired deletes every expired session and returns how many.
func (s *Sessions) PurgeExpired(ctx context.Context) (int64, error) {
	var n int64
	err := s.st.WithTx(ctx, func(q *db.Queries) (err error) {
		n, err = q.SessionDeleteExpired(ctx, store.FormatTime(s.now()))
		return err
	})
	return n, err
}

// Validate is the read-only liveness check used on every SSE keepalive: no sliding, no
// writes, no cookies. ErrInvalidCredentials when the row is missing or expired; any other
// error is a database failure (callers keep the stream open on those).
func (s *Sessions) Validate(ctx context.Context, tokenHash string) error {
	_, err := s.Lookup(ctx, tokenHash)
	return err
}

// StartPurge purges once immediately, then on every tick, until ctx is done. A nil ticks
// channel means a real hourly ticker. The returned channel closes when the goroutine exits.
func (s *Sessions) StartPurge(ctx context.Context, ticks <-chan time.Time) <-chan struct{} {
	done := make(chan struct{})
	var stop func()
	if ticks == nil {
		t := time.NewTicker(PurgeInterval)
		ticks, stop = t.C, t.Stop
	}
	go func() {
		defer close(done)
		if stop != nil {
			defer stop()
		}
		purge := func() {
			if n, err := s.PurgeExpired(ctx); err != nil {
				if ctx.Err() == nil {
					s.log.Error("session purge failed", "err", err)
				}
			} else if n > 0 {
				s.log.Info("expired sessions purged", "count", n)
			}
		}
		purge()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticks:
				purge()
			}
		}
	}()
	return done
}

// Cookie builds the session cookie for value: HttpOnly, SameSite=Lax, Path=/,
// Max-Age=2592000, Secure when configured.
func (s *Sessions) Cookie(value string) *http.Cookie {
	return &http.Cookie{
		Name: SessionCookieName, Value: value, Path: "/",
		MaxAge: int(SessionLifetime / time.Second), HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: s.secure,
	}
}

// ClearCookie builds the expiring cookie with the same Path and attributes (Max-Age=0).
func (s *Sessions) ClearCookie() *http.Cookie {
	return &http.Cookie{
		Name: SessionCookieName, Value: "", Path: "/", MaxAge: -1,
		Expires: time.Unix(0, 0).UTC(), HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: s.secure,
	}
}
