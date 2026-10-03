package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/RJGJ/Pabrika/internal/store/db"
)

type ticketService struct{ s *Services }

const (
	defaultListLimit = 50
	maxListLimit     = 200
	activityTextCap  = 200
)

// tktClampLimit applies the shared list-limit rules: <= 0 means the default, above max is clamped.
func tktClampLimit(limit int) int {
	switch {
	case limit <= 0:
		return defaultListLimit
	case limit > maxListLimit:
		return maxListLimit
	}
	return limit
}

// ---- row shape shared by Get (full row) and List (no description) ----

type ticketRow struct {
	ID, ProjectID        string
	Number               int64
	Title, Description   string
	Status, Priority     string
	AssigneeID           *string
	Position             float64
	DueDate              *string
	CreatedAt, UpdatedAt string
}

func rowFromTicket(t db.Ticket) ticketRow {
	return ticketRow{t.ID, t.ProjectID, t.Number, t.Title, t.Description, t.Status, t.Priority,
		t.AssigneeID, t.Position, t.DueDate, t.CreatedAt, t.UpdatedAt}
}

func rowFromListRow(r db.TicketListPageRow) ticketRow {
	return ticketRow{r.ID, r.ProjectID, r.Number, r.Title, "", r.Status, r.Priority,
		r.AssigneeID, r.Position, r.DueDate, r.CreatedAt, r.UpdatedAt}
}

// hydrateTickets loads labels, assignees and comment counts for any slice of tickets in at most
// three batched queries (never per ticket). Every path (create, get, list, update, move) uses it.
func (s *Services) hydrateTickets(ctx context.Context, q *db.Queries, projectKey string, rows []ticketRow) ([]Ticket, error) {
	out := make([]Ticket, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	ids := make([]string, len(rows))
	var userIDs []string
	seenUser := map[string]bool{}
	for i, r := range rows {
		ids[i] = r.ID
		if r.AssigneeID != nil && !seenUser[*r.AssigneeID] {
			seenUser[*r.AssigneeID] = true
			userIDs = append(userIDs, *r.AssigneeID)
		}
	}
	labelRows, err := q.TicketLabelsForTickets(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("load ticket labels: %w", err)
	}
	labels := map[string][]Label{}
	for _, l := range labelRows {
		labels[l.TicketID] = append(labels[l.TicketID], Label{ID: l.ID, ProjectID: l.ProjectID, Name: l.Name, Color: LabelColor(l.Color)})
	}
	countRows, err := q.TicketCommentCounts(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("load comment counts: %w", err)
	}
	counts := map[string]int{}
	for _, c := range countRows {
		counts[c.TicketID] = int(c.CommentCount)
	}
	users := map[string]UserRef{}
	if len(userIDs) > 0 {
		urows, err := q.TicketUsersByIDs(ctx, userIDs)
		if err != nil {
			return nil, fmt.Errorf("load assignees: %w", err)
		}
		for _, u := range urows {
			users[u.ID] = UserRef{ID: u.ID, Email: u.Email, DisplayName: u.DisplayName}
		}
	}
	for i, r := range rows {
		t := Ticket{
			ID: r.ID, ProjectID: r.ProjectID, ProjectKey: projectKey, Number: r.Number,
			Ref: FormatRef(projectKey, r.Number), Title: r.Title, Description: r.Description,
			Status: Status(r.Status), Priority: Priority(r.Priority), AssigneeID: r.AssigneeID,
			Position: r.Position, DueDate: r.DueDate, Labels: labels[r.ID], CommentCount: counts[r.ID],
			CreatedAt: parseTime(r.CreatedAt), UpdatedAt: parseTime(r.UpdatedAt),
		}
		if t.Labels == nil {
			t.Labels = []Label{}
		}
		if r.AssigneeID != nil {
			if u, ok := users[*r.AssigneeID]; ok {
				u := u
				t.Assignee = &u
			}
		}
		out[i] = t
	}
	return out, nil
}

// loadTicket reads one live ticket and hydrates it.
func (s *Services) loadTicket(ctx context.Context, q *db.Queries, projectKey, id string) (Ticket, error) {
	row, err := q.TicketGet(ctx, id)
	if err != nil {
		return Ticket{}, mapNoRows(err)
	}
	ts, err := s.hydrateTickets(ctx, q, projectKey, []ticketRow{rowFromTicket(row)})
	if err != nil {
		return Ticket{}, err
	}
	return ts[0], nil
}

// ---- activity writer ----

var ticketActivityKeys = map[string]bool{
	"title": true, "description": true, "priority": true, "due_date": true,
	"assignee": true, "labels": true, "status": true, "position": true,
}

// recordTicketActivity writes one ticket_activity row inside the caller's transaction. It refuses
// any changes key outside the pinned set.
func recordTicketActivity(ctx context.Context, tx *Tx, actor Actor, ticketID, action string, changes map[string][2]any) error {
	if changes == nil {
		changes = map[string][2]any{}
	}
	for k := range changes {
		if !ticketActivityKeys[k] {
			return fmt.Errorf("service: unknown activity change key %q", k)
		}
	}
	b, err := json.Marshal(changes)
	if err != nil {
		return fmt.Errorf("encode activity changes: %w", err)
	}
	return tx.Q.ActivityInsert(ctx, db.ActivityInsertParams{
		ID: tx.NewID(), TicketID: ticketID, ActorType: string(actor.Type), ActorID: actor.ID,
		Action: action, Changes: string(b), CreatedAt: tx.NowText(),
	})
}

func activityTruncate(s string) string {
	r := []rune(s)
	if len(r) <= activityTextCap {
		return s
	}
	return string(r[:activityTextCap])
}

// ---- small helpers ----

// tktNormStr turns nil and "" into nil.
func tktNormStr(p *string) *string {
	if p == nil {
		return nil
	}
	v := strings.TrimSpace(*p)
	if v == "" {
		return nil
	}
	return &v
}

func tktPtrEq(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func tktNormalizeIDs(ids []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if IsULID(id) {
			id = strings.ToUpper(id)
		}
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// tktCheckAssignee records a field error when the assignee is not a member of the project.
func tktCheckAssignee(ctx context.Context, q *db.Queries, f fieldErrs, projectID string, assignee *string) error {
	if assignee == nil {
		return nil
	}
	n, err := q.TicketIsMember(ctx, db.TicketIsMemberParams{ProjectID: projectID, UserID: *assignee})
	if err != nil {
		return err
	}
	if n == 0 {
		f.add("assignee", "must be a member of the project")
	}
	return nil
}

// tktCheckLabels returns the labels (name order) when all ids belong to the project, else records a field error.
func tktCheckLabels(ctx context.Context, q *db.Queries, f fieldErrs, projectID string, ids []string) ([]db.Label, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := q.TicketLabelsInProject(ctx, db.TicketLabelsInProjectParams{ProjectID: projectID, LabelIds: ids})
	if err != nil {
		return nil, err
	}
	if len(rows) != len(ids) {
		f.add("labels", "every label must belong to this project")
		return nil, nil
	}
	return rows, nil
}

func tktLabelNames(ls []db.Label) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.Name
	}
	return out
}

func tktSameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]bool{}
	for _, x := range a {
		m[x] = true
	}
	for _, x := range b {
		if !m[x] {
			return false
		}
	}
	return true
}

// ---- Create ----

func (t *ticketService) Create(ctx context.Context, actor Actor, projectRef string, in CreateTicketInput) (Ticket, error) {
	var out Ticket
	err := t.s.write(ctx, actor, func(tx *Tx) error {
		proj, _, err := t.s.requireProjectWrite(ctx, tx.Q, actor, projectRef, RoleEditor)
		if err != nil {
			return err
		}
		if err := in.Validate(); err != nil {
			return err
		}
		status := in.Status
		if status == "" {
			status = StatusTodo
		}
		prio := in.Priority
		if prio == "" {
			prio = PriorityMedium
		}
		title := strings.TrimSpace(in.Title)
		due := tktNormStr(in.DueDate)
		assignee := tktNormStr(in.AssigneeID)
		labelIDs := tktNormalizeIDs(in.LabelIDs)

		f := fieldErrs{}
		if err := tktCheckAssignee(ctx, tx.Q, f, proj.ID, assignee); err != nil {
			return err
		}
		labels, err := tktCheckLabels(ctx, tx.Q, f, proj.ID, labelIDs)
		if err != nil {
			return err
		}
		if err := f.err(); err != nil {
			return err
		}

		number, err := tx.Q.TicketAllocateNumber(ctx, proj.ID)
		if err != nil {
			return fmt.Errorf("allocate ticket number: %w", err)
		}
		col, err := tx.Q.TicketListColumn(ctx, db.TicketListColumnParams{ProjectID: proj.ID, Status: string(status)})
		if err != nil {
			return err
		}
		positions := make([]float64, len(col))
		for i, c := range col {
			positions[i] = c.Position
		}
		now := tx.NowText()
		id := tx.NewID()
		if err := tx.Q.TicketInsert(ctx, db.TicketInsertParams{
			ID: id, ProjectID: proj.ID, Number: number, Title: title, Description: in.Description,
			Status: string(status), Priority: string(prio), AssigneeID: assignee, Position: Bottom(positions),
			DueDate: due, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		for _, l := range labels {
			if err := tx.Q.TicketAddLabel(ctx, db.TicketAddLabelParams{TicketID: id, LabelID: l.ID}); err != nil {
				return err
			}
		}
		changes := map[string][2]any{
			"title":    {nil, title},
			"status":   {nil, string(status)},
			"priority": {nil, string(prio)},
		}
		if in.Description != "" {
			changes["description"] = [2]any{nil, activityTruncate(in.Description)}
		}
		if due != nil {
			changes["due_date"] = [2]any{nil, *due}
		}
		if assignee != nil {
			changes["assignee"] = [2]any{nil, *assignee}
		}
		if len(labels) > 0 {
			changes["labels"] = [2]any{nil, tktLabelNames(labels)}
		}
		if err := recordTicketActivity(ctx, tx, actor, id, "created", changes); err != nil {
			return err
		}
		tx.Emit(Event{Type: EventTicketCreated, ProjectID: proj.ID, TicketID: id})
		out, err = t.s.loadTicket(ctx, tx.Q, proj.Key, id)
		return err
	})
	return out, err
}

// ---- Get / Resolve ----

func (t *ticketService) Get(ctx context.Context, actor Actor, ref string) (Ticket, error) {
	var out Ticket
	err := t.s.read(ctx, func(q *db.Queries) error {
		loc, _, _, err := t.s.resolveTicket(ctx, q, actor, ref, RoleViewer, false)
		if err != nil {
			return err
		}
		out, err = t.s.loadTicket(ctx, q, loc.ProjectKey, loc.ID)
		return err
	})
	return out, err
}

func (t *ticketService) Resolve(ctx context.Context, actor Actor, ref string) (TicketRef, error) {
	var out TicketRef
	err := t.s.read(ctx, func(q *db.Queries) error {
		loc, _, _, err := t.s.resolveTicket(ctx, q, actor, ref, RoleViewer, false)
		if err != nil {
			return err
		}
		out = TicketRef{ID: loc.ID, ProjectID: loc.ProjectID, Key: loc.ProjectKey, Number: loc.Number}
		return nil
	})
	return out, err
}

// ---- List ----

func (t *ticketService) List(ctx context.Context, actor Actor, projectRef string, f TicketFilter) (TicketPage, error) {
	var out TicketPage
	err := t.s.read(ctx, func(q *db.Queries) error {
		proj, _, err := t.s.requireProject(ctx, q, actor, projectRef, RoleViewer, false)
		if err != nil {
			return err
		}
		fe := fieldErrs{}
		if f.Status != nil && !f.Status.Valid() {
			fe.add("status", "must be one of backlog, todo, in_progress, done")
		}
		if f.Priority != nil && !f.Priority.Valid() {
			fe.add("priority", "must be one of low, medium, high, urgent")
		}
		if err := fe.err(); err != nil {
			return err
		}
		limit := tktClampLimit(f.Limit)
		p := db.TicketListPageParams{ProjectID: proj.ID, RowLimit: int64(limit) + 1}
		if f.Status != nil {
			v := string(*f.Status)
			p.Status = &v
		}
		if f.Priority != nil {
			v := string(*f.Priority)
			p.Priority = &v
		}
		if f.Assignee != nil {
			if f.Assignee.Unassigned {
				p.Unassigned = 1
			} else if f.Assignee.UserID != "" {
				v := f.Assignee.UserID
				p.AssigneeID = &v
			}
		}
		if f.LabelID != "" {
			v := strings.ToUpper(f.LabelID)
			p.LabelID = &v
		}
		if qs := strings.TrimSpace(f.Query); qs != "" {
			p.QueryText = &qs
		}
		if f.Cursor != "" {
			keys, err := DecodeCursor(f.Cursor, 3)
			if err != nil {
				return err
			}
			if p.CursorRank, err = keys.Int(0); err != nil {
				return err
			}
			if p.CursorPosition, err = keys.Float(1); err != nil {
				return err
			}
			if p.CursorID, err = keys.Str(2); err != nil {
				return err
			}
			p.HasCursor = 1
		}
		rows, err := q.TicketListPage(ctx, p)
		if err != nil {
			return err
		}
		more := len(rows) > limit
		if more {
			rows = rows[:limit]
		}
		trs := make([]ticketRow, len(rows))
		for i, r := range rows {
			trs[i] = rowFromListRow(r)
		}
		items, err := t.s.hydrateTickets(ctx, q, proj.Key, trs)
		if err != nil {
			return err
		}
		out.Items = items
		if more {
			last := rows[len(rows)-1]
			out.NextCursor = EncodeCursor(last.StatusRank, last.Position, last.ID)
		}
		return nil
	})
	return out, err
}

// ---- Update ----

func (t *ticketService) Update(ctx context.Context, actor Actor, ref string, in UpdateTicketInput) (Ticket, error) {
	var out Ticket
	err := t.s.write(ctx, actor, func(tx *Tx) error {
		loc, proj, _, err := t.s.resolveTicket(ctx, tx.Q, actor, ref, RoleEditor, true)
		if err != nil {
			return err
		}
		if err := requireWritable(proj); err != nil {
			return err
		}
		if err := in.Validate(); err != nil {
			return err
		}
		cur, err := tx.Q.TicketGet(ctx, loc.ID)
		if err != nil {
			return mapNoRows(err)
		}
		f := fieldErrs{}
		changes := map[string][2]any{}

		title, desc, prio, due, assignee := cur.Title, cur.Description, cur.Priority, cur.DueDate, cur.AssigneeID
		if in.Title.Set {
			if v := strings.TrimSpace(in.Title.Value); v != cur.Title {
				changes["title"] = [2]any{cur.Title, v}
				title = v
			}
		}
		if in.Description.Set && in.Description.Value != cur.Description {
			changes["description"] = [2]any{activityTruncate(cur.Description), activityTruncate(in.Description.Value)}
			desc = in.Description.Value
		}
		if in.Priority.Set && string(in.Priority.Value) != cur.Priority {
			changes["priority"] = [2]any{cur.Priority, string(in.Priority.Value)}
			prio = string(in.Priority.Value)
		}
		if in.DueDate.Set {
			var nv *string
			if !in.DueDate.Null {
				nv = tktNormStr(in.DueDate.Value)
			}
			if !tktPtrEq(nv, cur.DueDate) {
				changes["due_date"] = [2]any{tktDerefAny(cur.DueDate), tktDerefAny(nv)}
				due = nv
			}
		}
		if in.AssigneeID.Set {
			var nv *string
			if !in.AssigneeID.Null {
				nv = tktNormStr(in.AssigneeID.Value)
			}
			if err := tktCheckAssignee(ctx, tx.Q, f, proj.ID, nv); err != nil {
				return err
			}
			if !tktPtrEq(nv, cur.AssigneeID) {
				changes["assignee"] = [2]any{tktDerefAny(cur.AssigneeID), tktDerefAny(nv)}
				assignee = nv
			}
		}
		var newLabels []db.Label
		labelsChanged := false
		if in.LabelIDs.Set {
			var ids []string
			if !in.LabelIDs.Null {
				ids = tktNormalizeIDs(in.LabelIDs.Value)
			}
			newLabels, err = tktCheckLabels(ctx, tx.Q, f, proj.ID, ids)
			if err != nil {
				return err
			}
			oldRows, err := tx.Q.TicketLabelsForTickets(ctx, []string{cur.ID})
			if err != nil {
				return err
			}
			oldIDs := make([]string, len(oldRows))
			oldNames := make([]string, len(oldRows))
			for i, r := range oldRows {
				oldIDs[i], oldNames[i] = r.ID, r.Name
			}
			if !tktSameSet(oldIDs, ids) {
				labelsChanged = true
				changes["labels"] = [2]any{oldNames, tktNonNil(tktLabelNames(newLabels))}
			}
		}
		if err := f.err(); err != nil {
			return err
		}
		if len(changes) == 0 {
			out, err = t.s.loadTicket(ctx, tx.Q, proj.Key, cur.ID)
			return err
		}
		if err := tx.Q.TicketUpdateFields(ctx, db.TicketUpdateFieldsParams{
			Title: title, Description: desc, Priority: prio, DueDate: due, AssigneeID: assignee,
			UpdatedAt: tx.NowText(), ID: cur.ID,
		}); err != nil {
			return err
		}
		if labelsChanged {
			if err := tx.Q.TicketClearLabels(ctx, cur.ID); err != nil {
				return err
			}
			for _, l := range newLabels {
				if err := tx.Q.TicketAddLabel(ctx, db.TicketAddLabelParams{TicketID: cur.ID, LabelID: l.ID}); err != nil {
					return err
				}
			}
		}
		action := "updated"
		if len(changes) == 1 {
			if _, ok := changes["assignee"]; ok {
				action = "assigned"
			} else if _, ok := changes["labels"]; ok {
				action = "labeled"
			}
		}
		if err := recordTicketActivity(ctx, tx, actor, cur.ID, action, changes); err != nil {
			return err
		}
		tx.Emit(Event{Type: EventTicketUpdated, ProjectID: proj.ID, TicketID: cur.ID})
		out, err = t.s.loadTicket(ctx, tx.Q, proj.Key, cur.ID)
		return err
	})
	return out, err
}

func tktDerefAny(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func tktNonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ---- Move ----

// resolveAnchor resolves a before/after anchor to its id. Anything unusable (unknown, other
// project, other status, soft-deleted, the moved ticket itself) is anchor_invalid.
func (t *ticketService) resolveAnchor(ctx context.Context, q *db.Queries, field, ref string, proj db.Project, target Status, selfID string) (string, error) {
	e := Validation(map[string]string{field: "must reference another live ticket in the target column"})
	e.Code = CodeAnchorInvalid
	loc, err := t.s.locateTicket(ctx, q, ref)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", e
		}
		return "", err
	}
	if loc.ID == selfID || loc.ProjectID != proj.ID {
		return "", e
	}
	row, err := q.TicketGet(ctx, loc.ID)
	if err != nil {
		if errors.Is(mapNoRows(err), ErrNotFound) {
			return "", e
		}
		return "", err
	}
	if Status(row.Status) != target {
		return "", e
	}
	return loc.ID, nil
}

func (t *ticketService) Move(ctx context.Context, actor Actor, ref string, in MoveInput) (MoveResult, error) {
	var out MoveResult
	err := t.s.write(ctx, actor, func(tx *Tx) error {
		loc, proj, _, err := t.s.resolveTicket(ctx, tx.Q, actor, ref, RoleEditor, true)
		if err != nil {
			return err
		}
		if err := requireWritable(proj); err != nil {
			return err
		}
		if err := in.Validate(); err != nil {
			return err
		}
		cur, err := tx.Q.TicketGet(ctx, loc.ID)
		if err != nil {
			return mapNoRows(err)
		}
		col, err := tx.Q.TicketListColumn(ctx, db.TicketListColumnParams{ProjectID: proj.ID, Status: string(in.Status), ExcludeID: cur.ID})
		if err != nil {
			return err
		}
		idx := len(col) // default: bottom
		anchorField, anchorRef := "", ""
		switch {
		case in.Before != "":
			anchorField, anchorRef = "before", in.Before
		case in.After != "":
			anchorField, anchorRef = "after", in.After
		case in.Place == "top":
			idx = 0
		}
		if anchorField != "" {
			aid, err := t.resolveAnchor(ctx, tx.Q, anchorField, anchorRef, proj, in.Status, cur.ID)
			if err != nil {
				return err
			}
			for i, c := range col {
				if c.ID == aid {
					idx = i
					if anchorField == "after" {
						idx = i + 1
					}
					break
				}
			}
		}

		sameStatus := Status(cur.Status) == in.Status
		if sameStatus {
			curIdx := 0
			for _, c := range col {
				if c.Position < cur.Position || (c.Position == cur.Position && c.ID < cur.ID) {
					curIdx++
				}
			}
			if idx == curIdx {
				out.Ticket, err = t.s.loadTicket(ctx, tx.Q, proj.Key, cur.ID)
				return err
			}
		}

		positions := make([]float64, len(col))
		for i, c := range col {
			positions[i] = c.Position
		}
		pos, renumber := PlanMove(positions, idx)
		if renumber {
			positions = Renumber(len(col))
			for i, c := range col {
				if err := tx.Q.TicketSetPosition(ctx, db.TicketSetPositionParams{Position: positions[i], ID: c.ID}); err != nil {
					return err
				}
			}
			pos, renumber = PlanMove(positions, idx)
			if renumber {
				return errors.New("service: column still needs renumbering after renumber")
			}
			out.Renumbered = true
		}
		if err := tx.Q.TicketUpdateMove(ctx, db.TicketUpdateMoveParams{
			Status: string(in.Status), Position: pos, UpdatedAt: tx.NowText(), ID: cur.ID,
		}); err != nil {
			return err
		}
		changes := map[string][2]any{"position": {cur.Position, pos}}
		if !sameStatus {
			changes["status"] = [2]any{cur.Status, string(in.Status)}
		}
		if err := recordTicketActivity(ctx, tx, actor, cur.ID, "moved", changes); err != nil {
			return err
		}
		tx.Emit(Event{Type: EventTicketMoved, ProjectID: proj.ID, TicketID: cur.ID, Renumbered: out.Renumbered})
		out.Ticket, err = t.s.loadTicket(ctx, tx.Q, proj.Key, cur.ID)
		return err
	})
	return out, err
}

// ---- Delete ----

func (t *ticketService) Delete(ctx context.Context, actor Actor, ref string) error {
	return t.s.write(ctx, actor, func(tx *Tx) error {
		loc, proj, _, err := t.s.resolveTicket(ctx, tx.Q, actor, ref, RoleEditor, true)
		if err != nil {
			return err
		}
		if err := requireWritable(proj); err != nil {
			return err
		}
		now := tx.NowText()
		n, err := tx.Q.TicketSoftDelete(ctx, db.TicketSoftDeleteParams{DeletedAt: &now, UpdatedAt: now, ID: loc.ID})
		if err != nil {
			return err
		}
		if n == 0 {
			return errNotFound()
		}
		if err := recordTicketActivity(ctx, tx, actor, loc.ID, "deleted", nil); err != nil {
			return err
		}
		tx.Emit(Event{Type: EventTicketDeleted, ProjectID: proj.ID, TicketID: loc.ID})
		return nil
	})
}
