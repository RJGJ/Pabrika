package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/RJGJ/Pabrika/internal/store/db"
)

type userService struct{ s *Services }

// UpdateProfile changes the actor's display name (trimmed, 1..MaxDisplayName runes).
// Session-only: a token actor gets `session_required` before anything else. Emits no event.
func (u *userService) UpdateProfile(ctx context.Context, actor Actor, displayName string) (User, error) {
	if err := u.s.requireSession(actor); err != nil {
		return User{}, err
	}
	name := strings.TrimSpace(displayName)
	if n := runeLen(name); n == 0 {
		return User{}, fieldError("display_name", "must not be empty")
	} else if n > MaxDisplayName {
		return User{}, fieldError("display_name", fmt.Sprintf("must be at most %d characters", MaxDisplayName))
	}
	var out User
	err := u.s.write(ctx, actor, func(tx *Tx) error {
		row, err := tx.Q.UpdateUserDisplayName(ctx, db.UpdateUserDisplayNameParams{DisplayName: name, ID: actor.UserID})
		if err != nil {
			return mapNoRows(err)
		}
		out = User{ID: row.ID, Email: row.Email, DisplayName: row.DisplayName, CreatedAt: parseTime(row.CreatedAt)}
		return nil
	})
	return out, err
}
