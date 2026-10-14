package service

import (
	"context"
	"encoding/json"

	"github.com/RJGJ/Pabrika/internal/store"
	"github.com/RJGJ/Pabrika/internal/store/db"
)

// ListProject returns the project feed: ticket activity and live comments of live tickets,
// newest first by (created_at, id). Any member; a token needs only read scope.
func (a *activityService) ListProject(ctx context.Context, actor Actor, projectRef string, f FeedFilter) (FeedPage, error) {
	var out FeedPage
	err := a.s.read(ctx, func(q *db.Queries) error {
		proj, _, err := a.s.requireProject(ctx, q, actor, projectRef, RoleViewer, false)
		if err != nil {
			return err
		}
		fe := fieldErrs{}
		switch f.Kind {
		case "", FeedKindActivity, FeedKindComment, FeedKindDecision:
		default:
			fe.add("kind", "must be one of activity, comment, decision")
		}
		switch f.ActorType {
		case "", string(ActorUser), string(ActorAPIToken):
		default:
			fe.add("actor_type", "must be one of user, api_token")
		}
		if err := fe.err(); err != nil {
			return err
		}
		limit := tktClampLimit(f.Limit)
		p := db.FeedListParams{ProjectID: proj.ID, RowLimit: int64(limit) + 1}
		if f.Kind != "" {
			p.Kind = &f.Kind
		}
		if f.ActorType != "" {
			p.ActorType = &f.ActorType
		}
		if f.Since != nil {
			v := store.FormatTime(f.Since.UTC())
			p.Since = &v
		}
		if f.Ticket != "" {
			loc, err := a.s.locateTicket(ctx, q, f.Ticket)
			if err != nil || loc.ProjectID != proj.ID {
				return fieldError("ticket", "must be a ticket of this project")
			}
			p.TicketID = &loc.ID
		}
		if f.Cursor != "" {
			keys, err := DecodeCursor(f.Cursor, 2)
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
		rows, err := q.FeedList(ctx, p)
		if err != nil {
			return err
		}
		more := len(rows) > limit
		if more {
			rows = rows[:limit]
		}
		for _, r := range rows {
			item := FeedItem{
				ID: r.ID, Kind: r.Kind, TicketID: r.TicketID, TicketRef: FormatRef(proj.Key, r.TicketNumber),
				Title: r.TicketTitle, Actor: commentAuthorOf(r.ActorType, r.ActorID, r.UserName, r.TokenName, r.OwnerName),
				CreatedAt: parseTime(r.CreatedAt), Changes: map[string][2]any{},
			}
			if r.Kind == FeedKindComment {
				item.Action = "commented"
				item.Summary = r.Summary
			} else {
				item.Action = r.Verb
				if json.Unmarshal([]byte(r.Changes), &item.Changes) != nil || item.Changes == nil {
					item.Changes = map[string][2]any{}
				}
			}
			out.Items = append(out.Items, item)
		}
		if more {
			last := rows[len(rows)-1]
			out.NextCursor = EncodeCursor(last.CreatedAt, last.ID)
		}
		return nil
	})
	return out, err
}
