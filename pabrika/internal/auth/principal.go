package auth

import (
	"context"
	"net/http"
	"time"

	"github.com/RJGJ/Pabrika/internal/service"
)

// Authentication methods.
const (
	MethodSession = "session"
	MethodToken   = "token"
)

// AuthUser is the authenticated account.
type AuthUser struct {
	ID, Email, DisplayName string
	CreatedAt              time.Time
}

// SessionInfo describes the session behind a Method == session principal. TokenHash is the
// SHA-256 hex of the cookie value; it is exported on purpose so the event stream can call
// Sessions.Validate(ctx, TokenHash) on every keepalive.
type SessionInfo struct {
	TokenHash string
	ExpiresAt time.Time
}

// TokenInfo describes the API token behind a Method == token principal.
type TokenInfo struct {
	ID, Name   string
	Scope      service.Scope
	ProjectID  string // "" = not limited
	ProjectKey string // "" = not limited
}

// Principal is the result of authentication.
type Principal struct {
	User    AuthUser
	Method  string
	Session *SessionInfo // Method == session
	Token   *TokenInfo   // Method == token

	// SetCookie, when non-nil, must be added to the response: the session slid and the cookie
	// is re-issued with a fresh Max-Age. Resolve has no ResponseWriter, so the caller applies it.
	SetCookie *http.Cookie
}

// Actor is the only place that turns an authenticated request into service permissions:
// sessions become service.UserActor, tokens service.TokenActor. The service enforces scope
// and project limit from these fields.
func (p Principal) Actor() service.Actor {
	if p.Method == MethodToken && p.Token != nil {
		return service.TokenActor(p.Token.ID, p.User.ID, p.Token.Scope, p.Token.ProjectID)
	}
	return service.UserActor(p.User.ID)
}

type principalKey struct{}

// WithPrincipal stores p in the context.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the principal stored by WithPrincipal.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}
