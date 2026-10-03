package auth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/store"
	"github.com/RJGJ/Pabrika/internal/store/db"
)

// TouchInterval is how stale last_used_at must be before it is written again, so a busy
// token does not cost one write per request on the single writer connection.
const TouchInterval = 60 * time.Second

// ResolverDeps are the collaborators of a Resolver. Now and Logger default like Sessions.
type ResolverDeps struct {
	Store    *store.Store
	Sessions *Sessions
	Now      func() time.Time
	Logger   *slog.Logger
}

// Resolver is the single current-user resolution used by every transport.
type Resolver struct {
	st   *store.Store
	sess *Sessions
	now  func() time.Time
	log  *slog.Logger
}

// NewResolver builds a Resolver.
func NewResolver(d ResolverDeps) *Resolver {
	r := &Resolver{st: d.Store, sess: d.Sessions, now: d.Now, log: d.Logger}
	if r.now == nil {
		r.now = func() time.Time { return time.Now().UTC() }
	}
	if r.log == nil {
		r.log = slog.Default()
	}
	return r
}

// hasAuthorization reports whether the request carries an Authorization header at all
// (even an empty one): that selects bearer-only authentication.
func hasAuthorization(r *http.Request) bool {
	_, ok := r.Header["Authorization"]
	return ok
}

// Resolve authenticates a request:
//
//  1. Any Authorization header means bearer only: it must be a well-formed API token that is
//     known and not revoked, else a *AuthError{Bearer: true}. The cookie is neither read nor
//     cleared and there is no fallback to it.
//  2. Otherwise the pb_session cookie: unknown or expired gives *AuthError{ClearCookie: true};
//     a live session slides when stale and then Principal.SetCookie carries the re-issued cookie.
//  3. Otherwise ErrNoCredentials.
//
// Revoked and unknown tokens are indistinguishable. A non-auth error (database failure) is
// returned as is and means 500, not 401.
func (rv *Resolver) Resolve(r *http.Request) (Principal, error) {
	if hasAuthorization(r) {
		return rv.ResolveBearer(r)
	}
	c, err := r.Cookie(SessionCookieName)
	if err != nil {
		return Principal{}, ErrNoCredentials
	}
	return rv.resolveSession(r.Context(), c.Value)
}

// ResolveBearer is step 1 alone: it never looks at cookies. A missing Authorization header
// is ErrNoCredentials. /mcp uses it.
func (rv *Resolver) ResolveBearer(r *http.Request) (Principal, error) {
	if !hasAuthorization(r) {
		return Principal{}, ErrNoCredentials
	}
	invalid := &AuthError{Err: ErrInvalidCredentials, Bearer: true}
	secret, ok := ParseBearer(r.Header.Get("Authorization"))
	if !ok {
		return Principal{}, invalid // shape check only: no database access
	}
	ctx := r.Context()
	hash := HashToken(secret)
	row, err := rv.st.Read().TokenGetByHash(ctx, hash)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, invalid
	}
	if err != nil {
		return Principal{}, err
	}
	if subtle.ConstantTimeCompare([]byte(row.TokenHash), []byte(hash)) != 1 || row.RevokedAt != nil {
		return Principal{}, invalid
	}
	rv.touch(ctx, row.ID, row.LastUsedAt)
	info := &TokenInfo{ID: row.ID, Name: row.Name, Scope: service.Scope(row.Scope)}
	if row.ProjectID != nil {
		info.ProjectID = *row.ProjectID
		if row.ProjectKey != nil {
			info.ProjectKey = *row.ProjectKey
		}
	}
	created, _ := store.ParseTime(row.OwnerCreatedAt)
	return Principal{
		User:   AuthUser{ID: row.UserID, Email: row.OwnerEmail, DisplayName: row.OwnerDisplayName, CreatedAt: created},
		Method: MethodToken, Token: info,
	}, nil
}

// touch stamps last_used_at at most once per TouchInterval. A failure is logged and ignored.
func (rv *Resolver) touch(ctx context.Context, tokenID string, lastUsed *string) {
	now := rv.now().UTC()
	if lastUsed != nil {
		if t, err := store.ParseTime(*lastUsed); err == nil && now.Sub(t) < TouchInterval {
			return
		}
	}
	nowText, threshold := store.FormatTime(now), store.FormatTime(now.Add(-TouchInterval))
	err := rv.st.WithTx(ctx, func(q *db.Queries) error {
		_, err := q.TokenTouch(ctx, db.TokenTouchParams{Now: &nowText, ID: tokenID, Threshold: &threshold})
		return err
	})
	if err != nil {
		rv.log.Warn("touch api token failed", "err", err)
	}
}

func (rv *Resolver) resolveSession(ctx context.Context, value string) (Principal, error) {
	invalid := &AuthError{Err: ErrInvalidCredentials, ClearCookie: true}
	if !validSessionValue(value) {
		return Principal{}, invalid
	}
	rec, err := rv.sess.Lookup(ctx, HashSessionValue(value))
	if errors.Is(err, ErrInvalidCredentials) {
		return Principal{}, invalid
	}
	if err != nil {
		return Principal{}, err
	}
	p := Principal{User: rec.User, Method: MethodSession}
	slid, ok, err := rv.sess.Slide(ctx, rec)
	switch {
	case err != nil:
		// The session is valid; failing to extend it must not lock the user out.
		rv.log.Warn("slide session failed", "err", err)
	case ok:
		rec = slid
		p.SetCookie = rv.sess.Cookie(value)
	}
	p.Session = &SessionInfo{TokenHash: rec.TokenHash, ExpiresAt: rec.ExpiresAt}
	return p, nil
}
