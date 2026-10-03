// Package auth owns credentials: argon2id password hashing, session cookies, API tokens, the
// in-memory rate limiter and the single current-user resolution (Resolver.Resolve) used by both
// the REST API and, through ResolveBearer, the MCP endpoint. It imports store and service (for
// Principal.Actor) and knows nothing about HTTP routing.
//
// Secrets are never stored: the database holds only SHA-256 hashes of session cookie values and
// API token secrets, and argon2id PHC strings for passwords.
package auth

import "errors"

var (
	// ErrNoCredentials: the request carries neither an Authorization header nor a session cookie.
	ErrNoCredentials = errors.New("auth: no credentials")
	// ErrInvalidCredentials: unknown, expired or revoked credentials (never told apart).
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
)

// AuthError is returned by the resolver for rejected credentials. It unwraps to
// ErrInvalidCredentials, and carries what the caller must do to the response because Resolve has
// no ResponseWriter: ClearCookie asks for an expired pb_session cookie, Bearer asks for a
// `WWW-Authenticate: Bearer` header.
type AuthError struct {
	Err         error
	ClearCookie bool
	Bearer      bool
}

func (e *AuthError) Error() string { return e.Err.Error() }
func (e *AuthError) Unwrap() error { return e.Err }
