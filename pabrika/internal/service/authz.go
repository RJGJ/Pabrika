package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/RJGJ/Pabrika/internal/store/db"
)

// authz.go is the single permission choke point. Every method that touches a project, ticket,
// label or comment resolves the owning project and calls requireProject.
//
// Order of checks (never reorder):
//  0. session-only operations: token actor -> Forbidden session_required (requireSession), before any lookup
//  1. unknown project or non-member -> NotFound (identical message)
//  2. token limited to another project -> NotFound (identical)
//  3. write op with a read-scope actor -> Forbidden insufficient_scope
//  4. effective role below the minimum -> Forbidden forbidden
//  5. archived project on a ticket/label/comment write -> Conflict project_archived (requireWritable)
//  6. input validation -> Validation (callers run Validate() last)

// checkActor is the actor-level part: valid type, and read scope vs write. It does not know
// about projects; Projects.Create and Projects.List call it directly.
func (s *Services) checkActor(actor Actor, write bool) error {
	if !actor.valid() {
		return errForbidden(CodeForbidden, "Forbidden")
	}
	if write && actor.readOnly() {
		return errForbidden(CodeInsufficientScope, "This token is read-only")
	}
	return nil
}

// requireUnlimited refuses a project-limited token for project-less operations (Projects.Create).
func (s *Services) requireUnlimited(actor Actor) error {
	if actor.ProjectID != "" {
		return errForbidden(CodeForbidden, "A project-limited token cannot create projects")
	}
	return nil
}

// requireSession is step 0: session-only operations refuse every token actor.
func (s *Services) requireSession(actor Actor) error {
	if !actor.valid() {
		return errForbidden(CodeForbidden, "Forbidden")
	}
	if actor.IsToken() {
		return errForbidden(CodeSessionRequired, "This action requires a signed-in session")
	}
	return nil
}

// effectiveRole caps the role at viewer for a read-scope actor.
func effectiveRole(actor Actor, r Role) Role {
	if actor.readOnly() && r.AtLeast(RoleViewer) {
		return RoleViewer
	}
	return r
}

// resolveProject maps a key-or-ULID ref to the project and the actor's membership in ONE query.
// Unknown and not-a-member are the same ErrNotFound.
func (s *Services) resolveProject(ctx context.Context, q *db.Queries, actor Actor, ref string) (db.Project, Role, error) {
	id, key, err := ParseProjectRef(ref)
	if err != nil {
		return db.Project{}, "", err
	}
	row, err := q.GetProjectForUser(ctx, db.GetProjectForUserParams{UserID: actor.UserID, RefID: id, RefKey: key})
	if errors.Is(err, sql.ErrNoRows) {
		return db.Project{}, "", errNotFound()
	}
	if err != nil {
		return db.Project{}, "", fmt.Errorf("resolve project: %w", err)
	}
	p := db.Project{
		ID: row.ID, Key: row.Key, Name: row.Name, Description: row.Description,
		NextTicketNumber: row.NextTicketNumber, CreatedBy: row.CreatedBy, ArchivedAt: row.ArchivedAt,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	return p, Role(row.MemberRole), nil
}

// requireProject resolves ref and applies steps 1-4. write marks a write operation (also implied
// by min >= editor). It returns the project and the actor's EFFECTIVE role. Step 0 is the
// caller's job (requireSession) because it must run before any lookup.
func (s *Services) requireProject(ctx context.Context, q *db.Queries, actor Actor, ref string, min Role, write bool) (db.Project, Role, error) {
	if !actor.valid() {
		return db.Project{}, "", errForbidden(CodeForbidden, "Forbidden")
	}
	p, role, err := s.resolveProject(ctx, q, actor, ref)
	if err != nil {
		return db.Project{}, "", err
	}
	if actor.ProjectID != "" && actor.ProjectID != p.ID {
		return db.Project{}, "", errNotFound()
	}
	if write || min.AtLeast(RoleEditor) {
		if err := s.checkActor(actor, true); err != nil {
			return db.Project{}, "", err
		}
	}
	role = effectiveRole(actor, role)
	if !role.AtLeast(min) {
		return db.Project{}, "", errForbidden(CodeForbidden, "You do not have permission to do this")
	}
	return p, role, nil
}

// requireWritable is step 5: ticket, label and comment writes in an archived project conflict.
func requireWritable(p db.Project) error {
	if p.ArchivedAt != nil {
		return NewError(KindConflict, CodeProjectArchived, "This project is archived and read-only")
	}
	return nil
}

// requireProjectWrite is requireProject(write) followed by the archived check, for editor-level
// ticket, label and comment writes.
func (s *Services) requireProjectWrite(ctx context.Context, q *db.Queries, actor Actor, ref string, min Role) (db.Project, Role, error) {
	p, role, err := s.requireProject(ctx, q, actor, ref, min, true)
	if err != nil {
		return db.Project{}, "", err
	}
	if err := requireWritable(p); err != nil {
		return db.Project{}, "", err
	}
	return p, role, nil
}

// ---- owner lookups: id -> owning project, then requireProject ----

type ticketLoc struct {
	ID, ProjectID, ProjectKey string
	Number                    int64
}

// locateTicket finds a live ticket by ULID or KEY-N reference (no permission check).
func (s *Services) locateTicket(ctx context.Context, q *db.Queries, ref string) (ticketLoc, error) {
	id, key, number, err := ParseTicketRef(ref)
	if err != nil {
		return ticketLoc{}, err
	}
	if id != "" {
		r, err := q.LocateTicketByID(ctx, id)
		if err != nil {
			return ticketLoc{}, mapNoRows(err)
		}
		return ticketLoc{r.ID, r.ProjectID, r.ProjectKey, r.Number}, nil
	}
	r, err := q.LocateTicketByRef(ctx, db.LocateTicketByRefParams{Key: key, Number: number})
	if err != nil {
		return ticketLoc{}, mapNoRows(err)
	}
	return ticketLoc{r.ID, r.ProjectID, r.ProjectKey, r.Number}, nil
}

// resolveTicket locates the ticket and applies requireProject on its project. A soft-deleted or
// unknown ticket and a ticket in a project the actor cannot see give the same ErrNotFound.
func (s *Services) resolveTicket(ctx context.Context, q *db.Queries, actor Actor, ref string, min Role, write bool) (ticketLoc, db.Project, Role, error) {
	t, err := s.locateTicket(ctx, q, ref)
	if err != nil {
		return ticketLoc{}, db.Project{}, "", err
	}
	p, role, err := s.requireProject(ctx, q, actor, t.ProjectID, min, write)
	if err != nil {
		return ticketLoc{}, db.Project{}, "", err
	}
	return t, p, role, nil
}

type labelLoc struct{ ID, ProjectID string }

// resolveLabel locates the label by ULID and applies requireProject on its project.
func (s *Services) resolveLabel(ctx context.Context, q *db.Queries, actor Actor, labelID string, min Role, write bool) (labelLoc, db.Project, Role, error) {
	if !IsULID(labelID) {
		return labelLoc{}, db.Project{}, "", errNotFound()
	}
	r, err := q.LocateLabel(ctx, upperID(labelID))
	if err != nil {
		return labelLoc{}, db.Project{}, "", mapNoRows(err)
	}
	p, role, err := s.requireProject(ctx, q, actor, r.ProjectID, min, write)
	if err != nil {
		return labelLoc{}, db.Project{}, "", err
	}
	return labelLoc{r.ID, r.ProjectID}, p, role, nil
}

type commentLoc struct {
	ID, TicketID, AuthorType, AuthorID string
	ProjectID, ProjectKey              string
	TicketNumber                       int64
}

// resolveComment locates a live comment on a live ticket and applies requireProject on its project.
func (s *Services) resolveComment(ctx context.Context, q *db.Queries, actor Actor, commentID string, min Role, write bool) (commentLoc, db.Project, Role, error) {
	if !IsULID(commentID) {
		return commentLoc{}, db.Project{}, "", errNotFound()
	}
	r, err := q.LocateComment(ctx, upperID(commentID))
	if err != nil {
		return commentLoc{}, db.Project{}, "", mapNoRows(err)
	}
	p, role, err := s.requireProject(ctx, q, actor, r.ProjectID, min, write)
	if err != nil {
		return commentLoc{}, db.Project{}, "", err
	}
	return commentLoc{r.ID, r.TicketID, r.AuthorType, r.AuthorID, r.ProjectID, r.ProjectKey, r.TicketNumber}, p, role, nil
}

func mapNoRows(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return errNotFound()
	}
	return err
}

func upperID(s string) string {
	id, _, _ := ParseProjectRef(s) // a ULID parses to its uppercase form
	if id != "" {
		return id
	}
	return s
}
