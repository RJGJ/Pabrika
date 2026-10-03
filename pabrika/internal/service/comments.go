package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/RJGJ/Pabrika/internal/store/db"
)

type commentService struct{ s *Services }

const (
	deletedUserName  = "deleted user"
	deletedTokenName = "deleted token"
)

// commentAuthorOf resolves a (type, id) author to display names from LEFT JOINed columns; a missing
// user or token row yields "deleted user" / "deleted token", never an error.
func commentAuthorOf(typ, id string, userName, tokenName, ownerName *string) CommentAuthor {
	a := CommentAuthor{Type: ActorType(typ), ID: id}
	if a.Type == ActorAPIToken {
		a.Name = deletedTokenName
		if tokenName != nil {
			a.Name = *tokenName
		}
		if ownerName != nil {
			a.OwnerName = *ownerName
		}
		return a
	}
	a.Name = deletedUserName
	if userName != nil {
		a.Name = *userName
	}
	return a
}

// commentRow is the common shape of the three comment view queries.
type commentRow struct {
	ID, TicketID, AuthorType, AuthorID, Body, CreatedAt string
	EditedAt                                            *string
	UserName, TokenName, OwnerName                      *string
}

func (r commentRow) comment(ticketRef string) Comment {
	return Comment{
		ID: r.ID, TicketID: r.TicketID, TicketRef: ticketRef,
		Author:    commentAuthorOf(r.AuthorType, r.AuthorID, r.UserName, r.TokenName, r.OwnerName),
		Body:      r.Body,
		CreatedAt: parseTime(r.CreatedAt), EditedAt: parseTimePtr(r.EditedAt),
	}
}

func validateCommentBody(body string) (string, error) {
	b := strings.TrimSpace(body)
	f := fieldErrs{}
	f.text("body", b, MaxCommentBody)
	return b, f.err()
}

func (c *commentService) view(ctx context.Context, q *db.Queries, id, ticketRef string) (Comment, error) {
	r, err := q.CommentGetView(ctx, id)
	if err != nil {
		return Comment{}, mapNoRows(err)
	}
	return commentRow{r.ID, r.TicketID, r.AuthorType, r.AuthorID, r.Body, r.CreatedAt, r.EditedAt,
		r.UserName, r.TokenName, r.OwnerName}.comment(ticketRef), nil
}

func (c *commentService) List(ctx context.Context, actor Actor, ticketRef string, limit int, cursor string) (CommentPage, error) {
	var out CommentPage
	err := c.s.read(ctx, func(q *db.Queries) error {
		loc, _, _, err := c.s.resolveTicket(ctx, q, actor, ticketRef, RoleViewer, false)
		if err != nil {
			return err
		}
		limit := tktClampLimit(limit)
		p := db.CommentListAscParams{TicketID: loc.ID, RowLimit: int64(limit) + 1}
		if cursor != "" {
			keys, err := DecodeCursor(cursor, 2)
			if err != nil {
				return err
			}
			if p.CursorCreatedAt, err = keys.Str(0); err != nil {
				return err
			}
			if p.CursorID, err = keys.Str(1); err != nil {
				return err
			}
			p.HasCursor = 1
		}
		rows, err := q.CommentListAsc(ctx, p)
		if err != nil {
			return err
		}
		more := len(rows) > limit
		if more {
			rows = rows[:limit]
		}
		ref := FormatRef(loc.ProjectKey, loc.Number)
		for _, r := range rows {
			out.Items = append(out.Items, commentRow{r.ID, r.TicketID, r.AuthorType, r.AuthorID, r.Body, r.CreatedAt, r.EditedAt,
				r.UserName, r.TokenName, r.OwnerName}.comment(ref))
		}
		if more {
			last := rows[len(rows)-1]
			out.NextCursor = EncodeCursor(last.CreatedAt, last.ID)
		}
		return nil
	})
	return out, err
}

func (c *commentService) Latest(ctx context.Context, actor Actor, ticketRef string, n int) ([]Comment, bool, error) {
	if n < 1 {
		n = 1
	}
	if n > maxListLimit {
		n = maxListLimit
	}
	var out []Comment
	truncated := false
	err := c.s.read(ctx, func(q *db.Queries) error {
		loc, _, _, err := c.s.resolveTicket(ctx, q, actor, ticketRef, RoleViewer, false)
		if err != nil {
			return err
		}
		rows, err := q.CommentListNewest(ctx, db.CommentListNewestParams{TicketID: loc.ID, RowLimit: int64(n) + 1})
		if err != nil {
			return err
		}
		if len(rows) > n {
			truncated = true
			rows = rows[:n]
		}
		ref := FormatRef(loc.ProjectKey, loc.Number)
		out = make([]Comment, 0, len(rows))
		for i := len(rows) - 1; i >= 0; i-- { // newest-first -> oldest-first
			r := rows[i]
			out = append(out, commentRow{r.ID, r.TicketID, r.AuthorType, r.AuthorID, r.Body, r.CreatedAt, r.EditedAt,
				r.UserName, r.TokenName, r.OwnerName}.comment(ref))
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return out, truncated, nil
}

func (c *commentService) Add(ctx context.Context, actor Actor, ticketRef, body string) (Comment, error) {
	var out Comment
	err := c.s.write(ctx, actor, func(tx *Tx) error {
		loc, proj, _, err := c.s.resolveTicket(ctx, tx.Q, actor, ticketRef, RoleEditor, true)
		if err != nil {
			return err
		}
		if err := requireWritable(proj); err != nil {
			return err
		}
		b, err := validateCommentBody(body)
		if err != nil {
			return err
		}
		id := tx.NewID()
		if err := tx.Q.CommentInsert(ctx, db.CommentInsertParams{
			ID: id, TicketID: loc.ID, AuthorType: string(actor.Type), AuthorID: actor.ID, Body: b, CreatedAt: tx.NowText(),
		}); err != nil {
			return err
		}
		tx.Emit(Event{Type: EventCommentAdded, ProjectID: proj.ID, TicketID: loc.ID, CommentID: id})
		out, err = c.view(ctx, tx.Q, id, FormatRef(loc.ProjectKey, loc.Number))
		return err
	})
	return out, err
}

// isAuthor reports whether the actor is exactly the comment's author (type and id).
func isAuthor(actor Actor, loc commentLoc) bool {
	return string(actor.Type) == loc.AuthorType && actor.ID == loc.AuthorID
}

func (c *commentService) Edit(ctx context.Context, actor Actor, commentID, body string) (Comment, error) {
	var out Comment
	err := c.s.write(ctx, actor, func(tx *Tx) error {
		// 404 -> insufficient_scope -> forbidden (role below editor, e.g. a demoted author)
		loc, proj, _, err := c.s.resolveComment(ctx, tx.Q, actor, commentID, RoleEditor, true)
		if err != nil {
			return err
		}
		if err := requireWritable(proj); err != nil {
			return err
		}
		if !isAuthor(actor, loc) {
			return errForbidden(CodeNotAuthor, "Only the author can edit this comment")
		}
		b, err := validateCommentBody(body)
		if err != nil {
			return err
		}
		ref := FormatRef(loc.ProjectKey, loc.TicketNumber)
		cur, err := c.view(ctx, tx.Q, loc.ID, ref)
		if err != nil {
			return err
		}
		if cur.Body == b {
			out = cur
			return nil
		}
		if err := tx.Q.CommentUpdateBody(ctx, db.CommentUpdateBodyParams{Body: b, EditedAt: tktPtr(tx.NowText()), ID: loc.ID}); err != nil {
			return err
		}
		tx.Emit(Event{Type: EventCommentChanged, ProjectID: proj.ID, TicketID: loc.TicketID, CommentID: loc.ID})
		out, err = c.view(ctx, tx.Q, loc.ID, ref)
		return err
	})
	return out, err
}

func (c *commentService) Delete(ctx context.Context, actor Actor, commentID string) error {
	return c.s.write(ctx, actor, func(tx *Tx) error {
		loc, proj, role, err := c.s.resolveComment(ctx, tx.Q, actor, commentID, RoleEditor, true)
		if err != nil {
			return err
		}
		if err := requireWritable(proj); err != nil {
			return err
		}
		if !isAuthor(actor, loc) && role != RoleOwner {
			return errForbidden(CodeNotAuthor, "Only the author or a project owner can delete this comment")
		}
		if err := tx.Q.CommentSoftDelete(ctx, db.CommentSoftDeleteParams{DeletedAt: tktPtr(tx.NowText()), ID: loc.ID}); err != nil {
			return fmt.Errorf("delete comment: %w", err)
		}
		tx.Emit(Event{Type: EventCommentChanged, ProjectID: proj.ID, TicketID: loc.TicketID, CommentID: loc.ID})
		return nil
	})
}

func (c *commentService) Resolve(ctx context.Context, actor Actor, commentID string) (CommentRef, error) {
	var out CommentRef
	err := c.s.read(ctx, func(q *db.Queries) error {
		loc, _, _, err := c.s.resolveComment(ctx, q, actor, commentID, RoleViewer, false)
		if err != nil {
			return err
		}
		out = CommentRef{ID: loc.ID, TicketID: loc.TicketID, TicketRef: FormatRef(loc.ProjectKey, loc.TicketNumber),
			ProjectID: loc.ProjectID, ProjectKey: loc.ProjectKey}
		return nil
	})
	return out, err
}

func tktPtr[T any](v T) *T { return &v }
