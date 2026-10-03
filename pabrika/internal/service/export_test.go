package service

import (
	"context"

	"github.com/RJGJ/Pabrika/internal/store/db"
)

// Test-only exports of unexported internals, usable from package service_test.

// Write exposes the write/Tx helper.
func (s *Services) Write(ctx context.Context, actor Actor, fn func(tx *Tx) error) error {
	return s.write(ctx, actor, fn)
}

// RequireProject runs requireProject inside a read transaction.
func (s *Services) RequireProject(ctx context.Context, actor Actor, ref string, min Role, write bool) (p db.Project, role Role, err error) {
	err = s.read(ctx, func(q *db.Queries) error {
		p, role, err = s.requireProject(ctx, q, actor, ref, min, write)
		return err
	})
	return
}

// RequireProjectWrite runs requireProjectWrite (incl. the archived check) inside a read transaction.
func (s *Services) RequireProjectWrite(ctx context.Context, actor Actor, ref string, min Role) (p db.Project, role Role, err error) {
	err = s.read(ctx, func(q *db.Queries) error {
		p, role, err = s.requireProjectWrite(ctx, q, actor, ref, min)
		return err
	})
	return
}

// ResolveTicketProject returns the project id a ticket ref resolves to for the actor.
func (s *Services) ResolveTicketProject(ctx context.Context, actor Actor, ref string, min Role, write bool) (projectID string, err error) {
	err = s.read(ctx, func(q *db.Queries) error {
		t, _, _, e := s.resolveTicket(ctx, q, actor, ref, min, write)
		projectID = t.ProjectID
		return e
	})
	return
}

// ResolveLabelProject is the label counterpart of ResolveTicketProject.
func (s *Services) ResolveLabelProject(ctx context.Context, actor Actor, id string, min Role, write bool) (projectID string, err error) {
	err = s.read(ctx, func(q *db.Queries) error {
		l, _, _, e := s.resolveLabel(ctx, q, actor, id, min, write)
		projectID = l.ProjectID
		return e
	})
	return
}

// ResolveCommentProject is the comment counterpart of ResolveTicketProject.
func (s *Services) ResolveCommentProject(ctx context.Context, actor Actor, id string, min Role, write bool) (projectID string, err error) {
	err = s.read(ctx, func(q *db.Queries) error {
		c, _, _, e := s.resolveComment(ctx, q, actor, id, min, write)
		projectID = c.ProjectID
		return e
	})
	return
}

// CheckActor, RequireSession and RequireUnlimited expose the actor-level checks.
func (s *Services) CheckActor(actor Actor, write bool) error { return s.checkActor(actor, write) }
func (s *Services) RequireSession(actor Actor) error         { return s.requireSession(actor) }
func (s *Services) RequireUnlimited(actor Actor) error       { return s.requireUnlimited(actor) }
