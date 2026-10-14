package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/RJGJ/Pabrika/internal/store/db"
)

type decisionService struct{ s *Services }

// MaxDecisionNote is the longest decision note, in characters.
const MaxDecisionNote = 2000

// Validate checks gate, verdict and note (changes_requested needs a note).
func (in RecordDecisionInput) Validate() error {
	f := fieldErrs{}
	if !in.Gate.Valid() {
		f.add("gate", "must be one of requirements, plan")
	}
	if !in.Verdict.Valid() {
		f.add("verdict", "must be one of approved, changes_requested")
	}
	note := ""
	if in.Note != nil {
		note = strings.TrimSpace(*in.Note)
	}
	switch {
	case in.Verdict == VerdictChangesRequested && note == "":
		f.add("note", "is required when requesting changes")
	case runeLen(note) > MaxDecisionNote:
		f.add("note", fmt.Sprintf("must be at most %d characters", MaxDecisionNote))
	}
	return f.err()
}

type decisionRow struct {
	ID, TicketID, Gate, Verdict string
	Note                        *string
	ActorUserID, ActorRole      string
	GateLabel, CreatedAt        string
	UserName, UserEmail         *string
}

func (r decisionRow) actor() DecisionActor {
	a := DecisionActor{ID: r.ActorUserID, Name: deletedUserName}
	if r.UserName != nil {
		a.Name = *r.UserName
	}
	if r.UserEmail != nil {
		a.Email = *r.UserEmail
	}
	return a
}

func (r decisionRow) decision(ticketRef string) Decision {
	return Decision{
		ID: r.ID, TicketID: r.TicketID, TicketRef: ticketRef, Gate: Gate(r.Gate), Verdict: Verdict(r.Verdict),
		Note: r.Note, Actor: r.actor(), ActorRole: Role(r.ActorRole), GateLabel: r.GateLabel,
		CreatedAt: parseTime(r.CreatedAt),
	}
}

// Record stores a human decision on a gate. Session only; owner or editor; the ticket must carry
// the gate's awaiting-* label. It writes a ticket_activity row in the same transaction and never
// changes labels or columns (the Orchestrator relabels).
func (d *decisionService) Record(ctx context.Context, actor Actor, ticketRef string, in RecordDecisionInput) (Decision, error) {
	var out Decision
	if err := d.s.requireSession(actor); err != nil {
		return out, err
	}
	err := d.s.write(ctx, actor, func(tx *Tx) error {
		loc, proj, role, err := d.s.resolveTicket(ctx, tx.Q, actor, ticketRef, RoleEditor, true)
		if err != nil {
			return err
		}
		if err := requireWritable(proj); err != nil {
			return err
		}
		if err := in.Validate(); err != nil {
			return err
		}
		label, _ := GateLabel(in.Gate)
		n, err := tx.Q.TicketHasLabelNamed(ctx, db.TicketHasLabelNamedParams{TicketID: loc.ID, Name: label})
		if err != nil {
			return err
		}
		if n == 0 {
			return NewError(KindConflict, CodeGateNotOpen, fmt.Sprintf("This ticket is not waiting for the %s decision", in.Gate))
		}
		var note *string
		if in.Note != nil {
			if v := strings.TrimSpace(*in.Note); v != "" {
				note = &v
			}
		}
		id := tx.NewID()
		if err := tx.Q.DecisionInsert(ctx, db.DecisionInsertParams{
			ID: id, TicketID: loc.ID, ProjectID: proj.ID, Gate: string(in.Gate), Verdict: string(in.Verdict),
			Note: note, ActorUserID: actor.UserID, ActorRole: string(role), GateLabel: label, CreatedAt: tx.NowText(),
		}); err != nil {
			return err
		}
		changes := map[string][2]any{
			"gate":      {nil, string(in.Verdict)},
			"gate_name": {nil, string(in.Gate)},
		}
		if note != nil {
			changes["note"] = [2]any{nil, activityTruncate(*note)}
		}
		if err := recordTicketActivity(ctx, tx, actor, loc.ID, "decision", changes); err != nil {
			return err
		}
		tx.Emit(Event{Type: EventDecisionRecorded, ProjectID: proj.ID, TicketID: loc.ID, DecisionID: id})
		r, err := tx.Q.DecisionGetView(ctx, id)
		if err != nil {
			return mapNoRows(err)
		}
		out = decisionRow{r.ID, r.TicketID, r.Gate, r.Verdict, r.Note, r.ActorUserID, r.ActorRole, r.GateLabel,
			r.CreatedAt, r.UserName, r.UserEmail}.decision(FormatRef(loc.ProjectKey, loc.Number))
		return nil
	})
	return out, err
}

// List returns a ticket's decisions, newest first. Any member; a token needs only read scope.
func (d *decisionService) List(ctx context.Context, actor Actor, ticketRef string, limit int, cursor string) (DecisionPage, error) {
	var out DecisionPage
	err := d.s.read(ctx, func(q *db.Queries) error {
		loc, _, _, err := d.s.resolveTicket(ctx, q, actor, ticketRef, RoleViewer, false)
		if err != nil {
			return err
		}
		limit := tktClampLimit(limit)
		p := db.DecisionListParams{TicketID: loc.ID, RowLimit: int64(limit) + 1}
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
		rows, err := q.DecisionList(ctx, p)
		if err != nil {
			return err
		}
		more := len(rows) > limit
		if more {
			rows = rows[:limit]
		}
		ref := FormatRef(loc.ProjectKey, loc.Number)
		for _, r := range rows {
			out.Items = append(out.Items, decisionRow{r.ID, r.TicketID, r.Gate, r.Verdict, r.Note, r.ActorUserID,
				r.ActorRole, r.GateLabel, r.CreatedAt, r.UserName, r.UserEmail}.decision(ref))
		}
		if more {
			last := rows[len(rows)-1]
			out.NextCursor = EncodeCursor(last.CreatedAt, last.ID)
		}
		return nil
	})
	return out, err
}

// latestDecisions loads the newest decision of each gate of a ticket with the Current flag.
// It returns nil when the ticket has none (or is soft-deleted).
func latestDecisions(ctx context.Context, q *db.Queries, ticketID string) ([]WorkflowDecision, error) {
	rows, err := q.DecisionLatestPerGate(ctx, ticketID)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	applied, err := lastLabelApplications(ctx, q, ticketID)
	if err != nil {
		return nil, err
	}
	out := make([]WorkflowDecision, 0, len(rows))
	for _, r := range rows {
		row := decisionRow{r.ID, r.TicketID, r.Gate, r.Verdict, r.Note, r.ActorUserID, r.ActorRole, r.GateLabel,
			r.CreatedAt, r.UserName, r.UserEmail}
		at := parseTime(r.CreatedAt)
		last, seen := applied[strings.ToLower(r.GateLabel)]
		out = append(out, WorkflowDecision{
			Gate: Gate(r.Gate), Verdict: Verdict(r.Verdict), Note: r.Note, At: at, By: row.actor(),
			Role: Role(r.ActorRole), Current: !seen || !at.Before(last),
		})
	}
	return out, nil
}

// lastLabelApplications maps a lower-cased label name to the time it was last applied to the
// ticket, read from the label changes in ticket_activity (a name in the new list and not in the
// old one). A tie counts as current (a decision needs the label present). A label that never appears there (seeded directly) is absent from the map.
func lastLabelApplications(ctx context.Context, q *db.Queries, ticketID string) (map[string]time.Time, error) {
	rows, err := q.TicketLabelChangeRows(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	out := map[string]time.Time{}
	for _, r := range rows {
		var ch map[string][2]json.RawMessage
		if json.Unmarshal([]byte(r.Changes), &ch) != nil {
			continue
		}
		pair, ok := ch["labels"]
		if !ok {
			continue
		}
		var oldL, newL []string
		_ = json.Unmarshal(pair[0], &oldL)
		if json.Unmarshal(pair[1], &newL) != nil {
			continue
		}
		had := map[string]bool{}
		for _, n := range oldL {
			had[strings.ToLower(n)] = true
		}
		for _, n := range newL {
			if k := strings.ToLower(n); !had[k] {
				out[k] = parseTime(r.CreatedAt)
			}
		}
	}
	return out, nil
}
