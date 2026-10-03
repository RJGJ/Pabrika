package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/RJGJ/Pabrika/internal/store"
	"github.com/RJGJ/Pabrika/internal/store/db"
)

type memberService struct{ s *Services }

func errLastOwner() *Error {
	return NewError(KindConflict, CodeLastOwner, "A project must keep at least one owner")
}

func errAlreadyMember() *Error {
	return NewError(KindConflict, CodeAlreadyMember, "This user is already a member of the project")
}

func toMember(userID, email, name, role, createdAt string) Member {
	return Member{UserID: userID, Email: email, DisplayName: name, Role: Role(role), CreatedAt: parseTime(createdAt)}
}

// List returns the members, owners first, then display name, then id. Viewers may list.
func (m *memberService) List(ctx context.Context, actor Actor, projectRef string) ([]Member, error) {
	out := []Member{}
	err := m.s.read(ctx, func(q *db.Queries) error {
		proj, _, err := m.s.requireProject(ctx, q, actor, projectRef, RoleViewer, false)
		if err != nil {
			return err
		}
		rows, err := q.MemberList(ctx, proj.ID)
		if err != nil {
			return err
		}
		for _, r := range rows {
			out = append(out, toMember(r.UserID, r.Email, r.DisplayName, r.Role, r.CreatedAt))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Add adds an existing user by email (owner, session only).
func (m *memberService) Add(ctx context.Context, actor Actor, projectRef, email string, role Role) (Member, error) {
	if err := m.s.requireSession(actor); err != nil {
		return Member{}, err
	}
	var out Member
	err := m.s.write(ctx, actor, func(tx *Tx) error {
		proj, _, err := m.s.requireProject(ctx, tx.Q, actor, projectRef, RoleOwner, true)
		if err != nil {
			return err
		}
		email := strings.TrimSpace(email)
		if email == "" {
			return fieldError("email", "must not be empty")
		}
		if !role.Valid() {
			return fieldError("role", "must be owner, editor or viewer")
		}
		u, err := tx.Q.GetUserByEmail(ctx, email)
		if errors.Is(err, sql.ErrNoRows) {
			return &Error{Kind: KindValidation, Code: CodeUserNotFound, Message: "No account with this email",
				Fields: map[string]string{"email": "No account with this email"}}
		}
		if err != nil {
			return err
		}
		if _, err := tx.Q.MemberGet(ctx, db.MemberGetParams{ProjectID: proj.ID, UserID: u.ID}); err == nil {
			return errAlreadyMember()
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		now := tx.NowText()
		if err := tx.Q.MemberInsert(ctx, db.MemberInsertParams{ProjectID: proj.ID, UserID: u.ID, Role: string(role), CreatedAt: now}); err != nil {
			if store.IsUniqueViolation(err) {
				return errAlreadyMember()
			}
			return err
		}
		out = toMember(u.ID, u.Email, u.DisplayName, string(role), now)
		tx.Emit(Event{Type: EventMemberChanged, ProjectID: proj.ID, UserID: u.ID})
		return nil
	})
	return out, err
}

// SetRole changes a member's role (owner, session only). Demoting the last owner conflicts.
func (m *memberService) SetRole(ctx context.Context, actor Actor, projectRef, userID string, role Role) (Member, error) {
	if err := m.s.requireSession(actor); err != nil {
		return Member{}, err
	}
	var out Member
	err := m.s.write(ctx, actor, func(tx *Tx) error {
		proj, _, err := m.s.requireProject(ctx, tx.Q, actor, projectRef, RoleOwner, true)
		if err != nil {
			return err
		}
		if !role.Valid() {
			return fieldError("role", "must be owner, editor or viewer")
		}
		target, err := m.target(ctx, tx.Q, proj.ID, userID)
		if err != nil {
			return err
		}
		if Role(target.Role) == role {
			out = toMember(target.UserID, target.Email, target.DisplayName, target.Role, target.CreatedAt)
			return nil
		}
		if Role(target.Role) == RoleOwner {
			if err := m.requireAnotherOwner(ctx, tx.Q, proj.ID); err != nil {
				return err
			}
		}
		if _, err := tx.Q.MemberUpdateRole(ctx, db.MemberUpdateRoleParams{Role: string(role), ProjectID: proj.ID, UserID: target.UserID}); err != nil {
			return err
		}
		out = toMember(target.UserID, target.Email, target.DisplayName, string(role), target.CreatedAt)
		tx.Emit(Event{Type: EventMemberChanged, ProjectID: proj.ID, UserID: target.UserID})
		return nil
	})
	return out, err
}

// Remove removes a member (owner, any member) or lets a member leave (session only). The last
// owner cannot be removed. The user's assignments in the project are cleared in the same transaction.
func (m *memberService) Remove(ctx context.Context, actor Actor, projectRef, userID string) error {
	if err := m.s.requireSession(actor); err != nil {
		return err
	}
	return m.s.write(ctx, actor, func(tx *Tx) error {
		proj, role, err := m.s.requireProject(ctx, tx.Q, actor, projectRef, RoleViewer, false)
		if err != nil {
			return err
		}
		self := strings.EqualFold(strings.TrimSpace(userID), actor.UserID)
		if !self && role != RoleOwner {
			// editors and viewers may only remove themselves; no lookup, so nothing is revealed
			return errForbidden(CodeForbidden, "You do not have permission to do this")
		}
		target, err := m.target(ctx, tx.Q, proj.ID, userID)
		if err != nil {
			return err
		}
		if Role(target.Role) == RoleOwner {
			if err := m.requireAnotherOwner(ctx, tx.Q, proj.ID); err != nil {
				return err
			}
		}
		if _, err := tx.Q.MemberDelete(ctx, db.MemberDeleteParams{ProjectID: proj.ID, UserID: target.UserID}); err != nil {
			return err
		}
		// live tickets first (they get an activity row), then clear every ticket incl. soft-deleted
		assignee := target.UserID
		live, err := tx.Q.MemberListLiveAssignedTickets(ctx, db.MemberListLiveAssignedTicketsParams{ProjectID: proj.ID, AssigneeID: &assignee})
		if err != nil {
			return err
		}
		if _, err := tx.Q.MemberClearAssignee(ctx, db.MemberClearAssigneeParams{ProjectID: proj.ID, AssigneeID: &assignee}); err != nil {
			return err
		}
		changes, _ := json.Marshal(map[string][2]any{"assignee": {target.UserID, nil}})
		for _, tid := range live {
			if err := tx.Q.MemberInsertActivity(ctx, db.MemberInsertActivityParams{
				ID: tx.NewID(), TicketID: tid, ActorType: string(actor.Type), ActorID: actor.ID,
				Changes: string(changes), CreatedAt: tx.NowText(),
			}); err != nil {
				return err
			}
		}
		tx.Emit(Event{Type: EventMemberChanged, ProjectID: proj.ID, UserID: target.UserID})
		tx.AfterCommit(func() { m.s.deps.Streams.CloseUser(proj.ID, target.UserID, CloseRemoved) })
		return nil
	})
}

// target loads a member by user id; unknown ids and non-members are ErrNotFound.
func (m *memberService) target(ctx context.Context, q *db.Queries, projectID, userID string) (db.MemberGetRow, error) {
	if !IsULID(userID) {
		return db.MemberGetRow{}, errNotFound()
	}
	row, err := q.MemberGet(ctx, db.MemberGetParams{ProjectID: projectID, UserID: upperID(userID)})
	if err != nil {
		return db.MemberGetRow{}, mapNoRows(err)
	}
	return row, nil
}

// requireAnotherOwner counts owners inside the current write transaction (the single write
// connection serialises it, so it cannot race) and conflicts when the target is the only one.
func (m *memberService) requireAnotherOwner(ctx context.Context, q *db.Queries, projectID string) error {
	n, err := q.MemberCountOwners(ctx, projectID)
	if err != nil {
		return err
	}
	if n <= 1 {
		return errLastOwner()
	}
	return nil
}
