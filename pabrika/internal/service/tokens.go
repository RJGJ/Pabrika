package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/RJGJ/Pabrika/internal/store/db"
)

// Token limits.
const (
	MaxTokenName     = 100
	MaxActiveTokens  = 100
	tooManyTokensMsg = "Too many active tokens; revoke one first"
)

// TokenService manages the caller's own API tokens. Every method is session-only: a token
// actor gets `session_required` before any lookup or validation. The secret never enters the
// service; internal/auth generates it and hands over only its hash and prefix.
type TokenService interface {
	// Create stores a token. ProjectRef (key or ULID) must name a project the caller is a
	// member of (any role), else ErrNotFound. At most MaxActiveTokens non-revoked tokens per
	// user, else 422 on `name`.
	Create(ctx context.Context, actor Actor, in CreateTokenInput) (Token, error)
	// List returns the caller's own tokens, newest first, revoked ones included.
	List(ctx context.Context, actor Actor) ([]Token, error)
	// Revoke revokes an own token; idempotent. Unknown or someone else's id is ErrNotFound.
	Revoke(ctx context.Context, actor Actor, tokenID string) error
}

// CreateTokenInput: Hash is the SHA-256 hex of the secret and Prefix its first 8 characters.
type CreateTokenInput struct {
	Name       string
	Scope      Scope
	ProjectRef string // "" = every project the owner is in
	Hash       string
	Prefix     string
}

// TokenProject is the project a token is limited to.
type TokenProject struct{ ID, Key string }

// Token is an API token as listed (never carries the secret or its hash).
type Token struct {
	ID, Name, Prefix string
	Scope            Scope
	Project          *TokenProject
	LastUsedAt       *time.Time
	RevokedAt        *time.Time
	CreatedAt        time.Time
}

type tokenService struct{ s *Services }

func (in CreateTokenInput) validate() error {
	f := fieldErrs{}
	f.text("name", in.Name, MaxTokenName)
	if in.Scope != ScopeRead && in.Scope != ScopeWrite {
		f.add("scope", "must be read or write")
	}
	return f.err()
}

func tokenFromRow(id, name, prefix, scope string, projectID, projectKey, lastUsed, revoked *string, created string) Token {
	t := Token{
		ID: id, Name: name, Prefix: prefix, Scope: Scope(scope),
		LastUsedAt: parseTimePtr(lastUsed), RevokedAt: parseTimePtr(revoked), CreatedAt: parseTime(created),
	}
	if projectID != nil {
		t.Project = &TokenProject{ID: *projectID}
		if projectKey != nil {
			t.Project.Key = *projectKey
		}
	}
	return t
}

func (t *tokenService) Create(ctx context.Context, actor Actor, in CreateTokenInput) (Token, error) {
	if err := t.s.requireSession(actor); err != nil {
		return Token{}, err
	}
	var out Token
	err := t.s.write(ctx, actor, func(tx *Tx) error {
		var projectID *string
		var projectKey string
		if ref := strings.TrimSpace(in.ProjectRef); ref != "" {
			p, _, err := t.s.requireProject(ctx, tx.Q, actor, ref, RoleViewer, false)
			if err != nil {
				return err
			}
			projectID, projectKey = &p.ID, p.Key
		}
		in.Name = strings.TrimSpace(in.Name)
		if err := in.validate(); err != nil {
			return err
		}
		n, err := tx.Q.TokenCountActive(ctx, actor.UserID)
		if err != nil {
			return err
		}
		if n >= MaxActiveTokens {
			return fieldError("name", tooManyTokensMsg)
		}
		row, err := tx.Q.CreateAPIToken(ctx, db.CreateAPITokenParams{
			ID: tx.NewID(), UserID: actor.UserID, Name: in.Name, TokenHash: in.Hash, TokenPrefix: in.Prefix,
			Scope: string(in.Scope), ProjectID: projectID, CreatedAt: tx.NowText(),
		})
		if err != nil {
			return fmt.Errorf("create token: %w", err)
		}
		var key *string
		if projectID != nil {
			key = &projectKey
		}
		out = tokenFromRow(row.ID, row.Name, row.TokenPrefix, row.Scope, row.ProjectID, key, row.LastUsedAt, row.RevokedAt, row.CreatedAt)
		return nil
	})
	if err != nil {
		return Token{}, err
	}
	return out, nil
}

func (t *tokenService) List(ctx context.Context, actor Actor) ([]Token, error) {
	if err := t.s.requireSession(actor); err != nil {
		return nil, err
	}
	out := []Token{}
	err := t.s.read(ctx, func(q *db.Queries) error {
		rows, err := q.TokenListForUser(ctx, actor.UserID)
		if err != nil {
			return err
		}
		for _, r := range rows {
			out = append(out, tokenFromRow(r.ID, r.Name, r.TokenPrefix, r.Scope, r.ProjectID, r.ProjectKey, r.LastUsedAt, r.RevokedAt, r.CreatedAt))
		}
		return nil
	})
	return out, err
}

func (t *tokenService) Revoke(ctx context.Context, actor Actor, tokenID string) error {
	if err := t.s.requireSession(actor); err != nil {
		return err
	}
	if !IsULID(tokenID) {
		return errNotFound()
	}
	return t.s.write(ctx, actor, func(tx *Tx) error {
		now := tx.NowText()
		n, err := tx.Q.TokenRevoke(ctx, db.TokenRevokeParams{RevokedAt: &now, ID: upperID(tokenID), UserID: actor.UserID})
		if err != nil {
			return err
		}
		if n == 0 {
			return errNotFound()
		}
		return nil
	})
}
