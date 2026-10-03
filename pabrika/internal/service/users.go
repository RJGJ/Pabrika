package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/RJGJ/Pabrika/internal/store"
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

// MaxEmail is the longest accepted email address; CodeEmailTaken is the duplicate-signup code.
const (
	MaxEmail       = 254
	CodeEmailTaken = "email_taken"
)

// NormalizeEmail trims and lowercases an email address.
func NormalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// validEmailShape is a deliberately loose check: one "@", a non-empty local part and a dotted
// domain, no whitespace. Deliverability is not our business.
func validEmailShape(e string) bool {
	if strings.ContainsAny(e, " \t\r\n") || strings.Count(e, "@") != 1 {
		return false
	}
	local, domain, _ := strings.Cut(e, "@")
	if local == "" || domain == "" || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return false
	}
	return strings.Contains(domain, ".") && !strings.Contains(domain, "..")
}

func (u *userService) ValidateNew(email, displayName string) *Error {
	f := fieldErrs{}
	e := NormalizeEmail(email)
	switch {
	case e == "":
		f.add("email", "must not be empty")
	case len(e) > MaxEmail:
		f.add("email", fmt.Sprintf("must be at most %d characters", MaxEmail))
	case !validEmailShape(e):
		f.add("email", "must be a valid email address")
	}
	f.text("display_name", displayName, MaxDisplayName)
	if len(f) == 0 {
		return nil
	}
	return Validation(f)
}

func (u *userService) Create(ctx context.Context, email, displayName, passwordHash string) (User, error) {
	if verr := u.ValidateNew(email, displayName); verr != nil {
		return User{}, verr
	}
	email = NormalizeEmail(email)
	name := strings.TrimSpace(displayName)
	taken := NewError(KindConflict, CodeEmailTaken, "An account with this email already exists")
	var out User
	err := u.s.write(ctx, UserActor(""), func(tx *Tx) error {
		if _, err := tx.Q.GetUserByEmail(ctx, email); err == nil {
			return taken
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		row, err := tx.Q.CreateUser(ctx, db.CreateUserParams{
			ID: tx.NewID(), Email: email, DisplayName: name, PasswordHash: passwordHash, CreatedAt: tx.NowText(),
		})
		if err != nil {
			if store.IsUniqueViolation(err) {
				return taken
			}
			return err
		}
		out = User{ID: row.ID, Email: row.Email, DisplayName: row.DisplayName, CreatedAt: parseTime(row.CreatedAt)}
		return nil
	})
	if err != nil {
		return User{}, err
	}
	return out, nil
}

func (u *userService) Credentials(ctx context.Context, email string) (string, string, error) {
	var id, hash string
	err := u.s.read(ctx, func(q *db.Queries) error {
		row, err := q.AccountGetCredentialsByEmail(ctx, NormalizeEmail(email))
		if err != nil {
			return mapNoRows(err)
		}
		id, hash = row.ID, row.PasswordHash
		return nil
	})
	return id, hash, err
}

func (u *userService) PasswordHash(ctx context.Context, userID string) (string, error) {
	var hash string
	err := u.s.read(ctx, func(q *db.Queries) error {
		h, err := q.AccountGetPasswordHash(ctx, userID)
		if err != nil {
			return mapNoRows(err)
		}
		hash = h
		return nil
	})
	return hash, err
}

func (u *userService) SetPassword(ctx context.Context, userID, newHash, keepSessionHash string) error {
	return u.s.write(ctx, UserActor(userID), func(tx *Tx) error {
		n, err := tx.Q.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{PasswordHash: newHash, ID: userID})
		if err != nil {
			return err
		}
		if n == 0 {
			return errNotFound()
		}
		if keepSessionHash == "" {
			_, err = tx.Q.SessionDeleteAllForUser(ctx, userID)
		} else {
			_, err = tx.Q.DeleteSessionsForUserExcept(ctx, db.DeleteSessionsForUserExceptParams{UserID: userID, TokenHash: keepSessionHash})
		}
		return err
	})
}
