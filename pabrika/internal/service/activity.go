package service

import (
	"context"
	"encoding/json"

	"github.com/RJGJ/Pabrika/internal/store/db"
)

type activityService struct{ s *Services }

func (a *activityService) List(ctx context.Context, actor Actor, ticketRef string, limit int, cursor string) (ActivityPage, error) {
	var out ActivityPage
	err := a.s.read(ctx, func(q *db.Queries) error {
		loc, _, _, err := a.s.resolveTicket(ctx, q, actor, ticketRef, RoleViewer, false)
		if err != nil {
			return err
		}
		limit := tktClampLimit(limit)
		p := db.ActivityListParams{TicketID: loc.ID, RowLimit: int64(limit) + 1}
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
		rows, err := q.ActivityList(ctx, p)
		if err != nil {
			return err
		}
		more := len(rows) > limit
		if more {
			rows = rows[:limit]
		}
		for _, r := range rows {
			changes := map[string][2]any{}
			if err := json.Unmarshal([]byte(r.Changes), &changes); err != nil {
				changes = map[string][2]any{}
			}
			out.Items = append(out.Items, Activity{
				ID: r.ID, TicketID: r.TicketID, Action: r.Action, Changes: changes,
				Actor:     commentAuthorOf(r.ActorType, r.ActorID, r.UserName, r.TokenName, r.OwnerName),
				CreatedAt: parseTime(r.CreatedAt),
			})
		}
		if more {
			last := rows[len(rows)-1]
			out.NextCursor = EncodeCursor(last.CreatedAt, last.ID)
		}
		return nil
	})
	return out, err
}
