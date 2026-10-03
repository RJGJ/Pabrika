package service

import (
	"context"
	"strings"

	"github.com/RJGJ/Pabrika/internal/store"
	"github.com/RJGJ/Pabrika/internal/store/db"
)

type projectService struct{ s *Services }

func toProject(p db.Project) Project {
	return Project{
		ID: p.ID, Key: p.Key, Name: p.Name, Description: p.Description,
		ArchivedAt: parseTimePtr(p.ArchivedAt),
		CreatedAt:  parseTime(p.CreatedAt), UpdatedAt: parseTime(p.UpdatedAt),
	}
}

func errKeyTaken() *Error {
	return NewError(KindConflict, CodeKeyTaken, "This project key is already taken")
}

// emptyCounts returns all four statuses at zero.
func emptyCounts() map[Status]int {
	m := make(map[Status]int, len(Statuses))
	for _, st := range Statuses {
		m[st] = 0
	}
	return m
}

// projectCounts loads live ticket counts for the projects in one query.
func projectCounts(ctx context.Context, q *db.Queries, ids []string) (map[string]map[Status]int, error) {
	out := make(map[string]map[Status]int, len(ids))
	for _, id := range ids {
		out[id] = emptyCounts()
	}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.ProjectStatusCounts(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if m, ok := out[r.ProjectID]; ok {
			m[Status(r.Status)] = int(r.N)
		}
	}
	return out, nil
}

// Create makes a project and the actor's owner membership in one transaction. A token needs
// write scope and must not be project-limited.
func (p *projectService) Create(ctx context.Context, actor Actor, in CreateProjectInput) (Project, error) {
	if err := p.s.checkActor(actor, true); err != nil {
		return Project{}, err
	}
	if err := p.s.requireUnlimited(actor); err != nil {
		return Project{}, err
	}
	if err := in.Validate(); err != nil {
		return Project{}, err
	}
	var out Project
	err := p.s.write(ctx, actor, func(tx *Tx) error {
		taken, err := tx.Q.ProjectKeyExists(ctx, NormalizeKey(in.Key))
		if err != nil {
			return err
		}
		if taken {
			return errKeyTaken()
		}
		now := tx.NowText()
		row, err := tx.Q.ProjectInsert(ctx, db.ProjectInsertParams{
			ID: tx.NewID(), Key: NormalizeKey(in.Key), Name: strings.TrimSpace(in.Name),
			Description: in.Description, CreatedBy: actor.UserID, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			if store.IsUniqueViolationOn(err, "projects.key") {
				return errKeyTaken()
			}
			return err
		}
		if err := tx.Q.ProjectInsertOwner(ctx, db.ProjectInsertOwnerParams{ProjectID: row.ID, UserID: actor.UserID, CreatedAt: now}); err != nil {
			return err
		}
		out = toProject(row)
		return nil
	})
	return out, err
}

// Get returns the project with the actor's effective role and per-status ticket counts.
func (p *projectService) Get(ctx context.Context, actor Actor, ref string) (ProjectDetail, error) {
	var out ProjectDetail
	err := p.s.read(ctx, func(q *db.Queries) error {
		proj, role, err := p.s.requireProject(ctx, q, actor, ref, RoleViewer, false)
		if err != nil {
			return err
		}
		counts, err := projectCounts(ctx, q, []string{proj.ID})
		if err != nil {
			return err
		}
		out = ProjectDetail{Project: toProject(proj), Role: role, TicketCounts: counts[proj.ID]}
		return nil
	})
	return out, err
}

// Resolve maps a key or ULID to {ID, Key, effective Role}; non-members get ErrNotFound.
func (p *projectService) Resolve(ctx context.Context, actor Actor, ref string) (ProjectRef, error) {
	var out ProjectRef
	err := p.s.read(ctx, func(q *db.Queries) error {
		proj, role, err := p.s.requireProject(ctx, q, actor, ref, RoleViewer, false)
		if err != nil {
			return err
		}
		out = ProjectRef{ID: proj.ID, Key: proj.Key, Role: role}
		return nil
	})
	return out, err
}

// List returns the actor's projects ordered by lower(name) then key. A project-limited token
// sees only its project. Archived projects are hidden unless includeArchived.
func (p *projectService) List(ctx context.Context, actor Actor, includeArchived bool) ([]ProjectSummary, error) {
	if err := p.s.checkActor(actor, false); err != nil {
		return nil, err
	}
	out := []ProjectSummary{}
	err := p.s.read(ctx, func(q *db.Queries) error {
		inc := int64(0)
		if includeArchived {
			inc = 1
		}
		rows, err := q.ProjectListForUser(ctx, db.ProjectListForUserParams{
			UserID: actor.UserID, IncludeArchived: inc, OnlyProject: actor.ProjectID,
		})
		if err != nil {
			return err
		}
		ids := make([]string, len(rows))
		for i, r := range rows {
			ids[i] = r.ID
		}
		counts, err := projectCounts(ctx, q, ids)
		if err != nil {
			return err
		}
		for _, r := range rows {
			proj := db.Project{
				ID: r.ID, Key: r.Key, Name: r.Name, Description: r.Description,
				NextTicketNumber: r.NextTicketNumber, CreatedBy: r.CreatedBy, ArchivedAt: r.ArchivedAt,
				CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			}
			out = append(out, ProjectSummary{
				Project: toProject(proj), Role: effectiveRole(actor, Role(r.MemberRole)), TicketCounts: counts[r.ID],
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Update changes name, description and archived state (owner). An unchanged update is a no-op.
// Owners may edit an archived project.
func (p *projectService) Update(ctx context.Context, actor Actor, ref string, in UpdateProjectInput) (Project, error) {
	var out Project
	err := p.s.write(ctx, actor, func(tx *Tx) error {
		proj, _, err := p.s.requireProject(ctx, tx.Q, actor, ref, RoleOwner, true)
		if err != nil {
			return err
		}
		if err := in.Validate(); err != nil {
			return err
		}
		name, desc, archivedAt := proj.Name, proj.Description, proj.ArchivedAt
		changed := false
		if in.Name.Set {
			if n := strings.TrimSpace(in.Name.Value); n != name {
				name, changed = n, true
			}
		}
		if in.Description.Set && in.Description.Value != desc {
			desc, changed = in.Description.Value, true
		}
		now := tx.NowText()
		if in.Archived.Set {
			if in.Archived.Value && archivedAt == nil {
				archivedAt, changed = &now, true
			} else if !in.Archived.Value && archivedAt != nil {
				archivedAt, changed = nil, true
			}
		}
		if !changed {
			out = toProject(proj)
			return nil
		}
		row, err := tx.Q.ProjectUpdate(ctx, db.ProjectUpdateParams{
			Name: name, Description: desc, ArchivedAt: archivedAt, UpdatedAt: now, ID: proj.ID,
		})
		if err != nil {
			return err
		}
		out = toProject(row)
		tx.Emit(Event{Type: EventProjectUpdated, ProjectID: proj.ID})
		return nil
	})
	return out, err
}

// Delete hard-deletes the project (owner, session only); FK cascades remove everything under it.
func (p *projectService) Delete(ctx context.Context, actor Actor, ref string) error {
	if err := p.s.requireSession(actor); err != nil {
		return err
	}
	return p.s.write(ctx, actor, func(tx *Tx) error {
		proj, _, err := p.s.requireProject(ctx, tx.Q, actor, ref, RoleOwner, true)
		if err != nil {
			return err
		}
		if err := tx.Q.ProjectDelete(ctx, proj.ID); err != nil {
			return err
		}
		tx.AfterCommit(func() { p.s.deps.Streams.CloseProject(proj.ID, CloseProject) })
		return nil
	})
}
