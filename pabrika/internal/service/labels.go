package service

import (
	"context"
	"strings"

	"github.com/RJGJ/Pabrika/internal/store"
	"github.com/RJGJ/Pabrika/internal/store/db"
)

type labelService struct{ s *Services }

func toLabel(l db.Label) Label {
	return Label{ID: l.ID, ProjectID: l.ProjectID, Name: l.Name, Color: LabelColor(l.Color)}
}

func errLabelExists() *Error {
	return NewError(KindConflict, CodeLabelExists, "A label with this name already exists")
}

// List returns the project's labels in name order (viewer+).
func (l *labelService) List(ctx context.Context, actor Actor, projectRef string) ([]Label, error) {
	out := []Label{}
	err := l.s.read(ctx, func(q *db.Queries) error {
		proj, _, err := l.s.requireProject(ctx, q, actor, projectRef, RoleViewer, false)
		if err != nil {
			return err
		}
		rows, err := q.LabelList(ctx, proj.ID)
		if err != nil {
			return err
		}
		for _, r := range rows {
			out = append(out, toLabel(r))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Create adds a label (editor+, project not archived). An empty color means gray.
func (l *labelService) Create(ctx context.Context, actor Actor, projectRef string, in LabelInput) (Label, error) {
	var out Label
	err := l.s.write(ctx, actor, func(tx *Tx) error {
		proj, _, err := l.s.requireProjectWrite(ctx, tx.Q, actor, projectRef, RoleEditor)
		if err != nil {
			return err
		}
		if err := in.Validate(); err != nil {
			return err
		}
		name := strings.TrimSpace(in.Name)
		color := in.Color
		if color == "" {
			color = "gray"
		}
		taken, err := tx.Q.LabelNameTaken(ctx, db.LabelNameTakenParams{ProjectID: proj.ID, Name: name})
		if err != nil {
			return err
		}
		if taken {
			return errLabelExists()
		}
		row, err := tx.Q.LabelInsert(ctx, db.LabelInsertParams{ID: tx.NewID(), ProjectID: proj.ID, Name: name, Color: string(color)})
		if err != nil {
			if store.IsUniqueViolation(err) {
				return errLabelExists()
			}
			return err
		}
		out = toLabel(row)
		tx.Emit(Event{Type: EventLabelChanged, ProjectID: proj.ID, LabelID: row.ID})
		return nil
	})
	return out, err
}

// Update renames or recolors a label. Identical values are a no-op (no event).
func (l *labelService) Update(ctx context.Context, actor Actor, labelID string, in UpdateLabelInput) (Label, error) {
	var out Label
	err := l.s.write(ctx, actor, func(tx *Tx) error {
		loc, proj, _, err := l.s.resolveLabel(ctx, tx.Q, actor, labelID, RoleEditor, true)
		if err != nil {
			return err
		}
		if err := requireWritable(proj); err != nil {
			return err
		}
		if err := in.Validate(); err != nil {
			return err
		}
		cur, err := tx.Q.LabelGet(ctx, loc.ID)
		if err != nil {
			return mapNoRows(err)
		}
		name, color := cur.Name, cur.Color
		if in.Name.Set {
			name = strings.TrimSpace(in.Name.Value)
		}
		if in.Color.Set {
			color = string(in.Color.Value)
		}
		if name == cur.Name && color == cur.Color {
			out = toLabel(cur)
			return nil
		}
		taken, err := tx.Q.LabelNameTaken(ctx, db.LabelNameTakenParams{ProjectID: loc.ProjectID, Name: name, ExcludeID: loc.ID})
		if err != nil {
			return err
		}
		if taken {
			return errLabelExists()
		}
		row, err := tx.Q.LabelUpdate(ctx, db.LabelUpdateParams{Name: name, Color: color, ID: loc.ID})
		if err != nil {
			if store.IsUniqueViolation(err) {
				return errLabelExists()
			}
			return err
		}
		out = toLabel(row)
		tx.Emit(Event{Type: EventLabelChanged, ProjectID: loc.ProjectID, LabelID: loc.ID})
		return nil
	})
	return out, err
}

// Delete removes a label; ticket_labels rows go with it by cascade. Writes no ticket activity.
func (l *labelService) Delete(ctx context.Context, actor Actor, labelID string) error {
	return l.s.write(ctx, actor, func(tx *Tx) error {
		loc, proj, _, err := l.s.resolveLabel(ctx, tx.Q, actor, labelID, RoleEditor, true)
		if err != nil {
			return err
		}
		if err := requireWritable(proj); err != nil {
			return err
		}
		if err := tx.Q.LabelDelete(ctx, loc.ID); err != nil {
			return err
		}
		tx.Emit(Event{Type: EventLabelChanged, ProjectID: loc.ProjectID, LabelID: loc.ID})
		return nil
	})
}
