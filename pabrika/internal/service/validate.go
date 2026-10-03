package service

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Status is a ticket column.
type Status string

// Priority is a ticket priority.
type Priority string

// LabelColor is one of the fixed label palette.
type LabelColor string

const (
	StatusBacklog    Status = "backlog"
	StatusTodo       Status = "todo"
	StatusInProgress Status = "in_progress"
	StatusDone       Status = "done"

	PriorityLow    Priority = "low"
	PriorityMedium Priority = "medium"
	PriorityHigh   Priority = "high"
	PriorityUrgent Priority = "urgent"
)

// Exported lists shared with REST and MCP (do not duplicate them elsewhere).
var (
	Statuses    = []Status{StatusBacklog, StatusTodo, StatusInProgress, StatusDone}
	Priorities  = []Priority{PriorityLow, PriorityMedium, PriorityHigh, PriorityUrgent}
	LabelColors = []LabelColor{"gray", "red", "orange", "amber", "green", "teal", "blue", "indigo", "purple", "pink"}
)

// Limits shared with REST and MCP. Lengths count runes, not bytes.
const (
	MaxTitle              = 200
	MaxDescription        = 20000
	MaxCommentBody        = 20000
	MaxProjectName        = 100
	MaxProjectDescription = 2000
	MaxLabelName          = 50
	MaxDisplayName        = 100
	MaxLabelsPerTicket    = 50
	DueDateLayout         = "2006-01-02"
)

// Valid reports whether s is a known status.
func (s Status) Valid() bool { return s.Rank() >= 0 }

// Rank is the column order 0..3 (backlog, todo, in_progress, done); -1 when invalid.
func (s Status) Rank() int {
	for i, v := range Statuses {
		if v == s {
			return i
		}
	}
	return -1
}

// Valid reports whether p is a known priority.
func (p Priority) Valid() bool {
	for _, v := range Priorities {
		if v == p {
			return true
		}
	}
	return false
}

// Valid reports whether c is in the palette.
func (c LabelColor) Valid() bool {
	for _, v := range LabelColors {
		if v == c {
			return true
		}
	}
	return false
}

var keyRe = regexp.MustCompile(`^[A-Z]{2,6}$`)

// NormalizeKey trims and uppercases a project key.
func NormalizeKey(k string) string { return strings.ToUpper(strings.TrimSpace(k)) }

func runeLen(s string) int { return utf8.RuneCountInString(s) }

type fieldErrs map[string]string

func (f fieldErrs) add(field, msg string) {
	if _, ok := f[field]; !ok {
		f[field] = msg
	}
}

func (f fieldErrs) err() error {
	if len(f) == 0 {
		return nil
	}
	return Validation(f)
}

// text validates a required field by its trimmed length (1..max runes).
func (f fieldErrs) text(field, v string, max int) {
	n := runeLen(strings.TrimSpace(v))
	switch {
	case n == 0:
		f.add(field, "must not be empty")
	case n > max:
		f.add(field, fmt.Sprintf("must be at most %d characters", max))
	}
}

func (f fieldErrs) rawText(field, v string, max int) {
	if runeLen(v) > max {
		f.add(field, fmt.Sprintf("must be at most %d characters", max))
	}
}

func (f fieldErrs) notNull(field string, null bool) bool {
	if null {
		f.add(field, "must not be null")
		return false
	}
	return true
}

func checkDueDate(f fieldErrs, v string) {
	if v == "" {
		return
	}
	if _, err := time.Parse(DueDateLayout, v); err != nil || len(v) != len(DueDateLayout) {
		f.add("due_date", "must be a date in YYYY-MM-DD format")
	}
}

func distinctCount(ids []string) int {
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		seen[id] = struct{}{}
	}
	return len(seen)
}

// ---- Project inputs ----

// CreateProjectInput creates a project.
type CreateProjectInput struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Validate checks shape and limits only (pure).
func (in CreateProjectInput) Validate() error {
	f := fieldErrs{}
	if !keyRe.MatchString(NormalizeKey(in.Key)) {
		f.add("key", "must be 2 to 6 letters")
	}
	f.text("name", in.Name, MaxProjectName)
	f.rawText("description", in.Description, MaxProjectDescription)
	return f.err()
}

// UpdateProjectInput patches a project (the key is immutable).
type UpdateProjectInput struct {
	Name        Optional[string] `json:"name"`
	Description Optional[string] `json:"description"`
	Archived    Optional[bool]   `json:"archived"`
}

// Validate checks shape and limits only (pure).
func (in UpdateProjectInput) Validate() error {
	f := fieldErrs{}
	if in.Name.Set && f.notNull("name", in.Name.Null) {
		f.text("name", in.Name.Value, MaxProjectName)
	}
	if in.Description.Set && f.notNull("description", in.Description.Null) {
		f.rawText("description", in.Description.Value, MaxProjectDescription)
	}
	if in.Archived.Set {
		f.notNull("archived", in.Archived.Null)
	}
	return f.err()
}

// ---- Label inputs ----

// LabelInput creates a label; an empty Color means gray.
type LabelInput struct {
	Name  string     `json:"name"`
	Color LabelColor `json:"color"`
}

// Validate checks shape and limits only (pure).
func (in LabelInput) Validate() error {
	f := fieldErrs{}
	f.text("name", in.Name, MaxLabelName)
	if in.Color != "" && !in.Color.Valid() {
		f.add("color", "must be one of the label palette colors")
	}
	return f.err()
}

// UpdateLabelInput patches a label.
type UpdateLabelInput struct {
	Name  Optional[string]     `json:"name"`
	Color Optional[LabelColor] `json:"color"`
}

// Validate checks shape and limits only (pure).
func (in UpdateLabelInput) Validate() error {
	f := fieldErrs{}
	if in.Name.Set && f.notNull("name", in.Name.Null) {
		f.text("name", in.Name.Value, MaxLabelName)
	}
	if in.Color.Set && f.notNull("color", in.Color.Null) && !in.Color.Value.Valid() {
		f.add("color", "must be one of the label palette colors")
	}
	return f.err()
}

// ---- Ticket inputs ----

// CreateTicketInput creates a ticket. Empty Status/Priority mean todo/medium.
type CreateTicketInput struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Status      Status   `json:"status"`
	Priority    Priority `json:"priority"`
	DueDate     *string  `json:"due_date"`
	AssigneeID  *string  `json:"assignee"`
	LabelIDs    []string `json:"labels"`
}

// Validate checks shape and limits only (pure). The assignee and label membership checks need the DB.
func (in CreateTicketInput) Validate() error {
	f := fieldErrs{}
	f.text("title", in.Title, MaxTitle)
	f.rawText("description", in.Description, MaxDescription)
	if in.Status != "" && !in.Status.Valid() {
		f.add("status", "must be one of backlog, todo, in_progress, done")
	}
	if in.Priority != "" && !in.Priority.Valid() {
		f.add("priority", "must be one of low, medium, high, urgent")
	}
	if in.DueDate != nil {
		checkDueDate(f, *in.DueDate)
	}
	if distinctCount(in.LabelIDs) > MaxLabelsPerTicket {
		f.add("labels", fmt.Sprintf("at most %d labels per ticket", MaxLabelsPerTicket))
	}
	return f.err()
}

// UpdateTicketInput patches a ticket. Status cannot change here (use Move).
type UpdateTicketInput struct {
	Title       Optional[string]   `json:"title"`
	Description Optional[string]   `json:"description"`
	Priority    Optional[Priority] `json:"priority"`
	DueDate     Optional[*string]  `json:"due_date"`
	AssigneeID  Optional[*string]  `json:"assignee"`
	LabelIDs    Optional[[]string] `json:"labels"`
}

// Validate checks shape and limits only (pure).
func (in UpdateTicketInput) Validate() error {
	f := fieldErrs{}
	if in.Title.Set && f.notNull("title", in.Title.Null) {
		f.text("title", in.Title.Value, MaxTitle)
	}
	if in.Description.Set && f.notNull("description", in.Description.Null) {
		f.rawText("description", in.Description.Value, MaxDescription)
	}
	if in.Priority.Set && f.notNull("priority", in.Priority.Null) && !in.Priority.Value.Valid() {
		f.add("priority", "must be one of low, medium, high, urgent")
	}
	if in.DueDate.Set && !in.DueDate.Null && in.DueDate.Value != nil {
		checkDueDate(f, *in.DueDate.Value)
	}
	if in.LabelIDs.Set && !in.LabelIDs.Null && distinctCount(in.LabelIDs.Value) > MaxLabelsPerTicket {
		f.add("labels", fmt.Sprintf("at most %d labels per ticket", MaxLabelsPerTicket))
	}
	return f.err()
}

// MoveInput places a ticket. At most one of Before, After, Place; Place is "top" or "bottom".
type MoveInput struct {
	Status Status `json:"status"`
	Before string `json:"before"`
	After  string `json:"after"`
	Place  string `json:"place"`
}

// Validate checks shape only (pure). Anchor checks need the DB.
func (in MoveInput) Validate() error {
	f := fieldErrs{}
	if in.Status == "" {
		f.add("status", "is required")
	} else if !in.Status.Valid() {
		f.add("status", "must be one of backlog, todo, in_progress, done")
	}
	set := 0
	for _, v := range []string{in.Before, in.After, in.Place} {
		if v != "" {
			set++
		}
	}
	if set > 1 {
		msg := "only one of before, after, place may be set"
		for name, v := range map[string]string{"before": in.Before, "after": in.After, "place": in.Place} {
			if v != "" {
				f.add(name, msg)
			}
		}
	}
	if in.Place != "" && in.Place != "top" && in.Place != "bottom" {
		f.add("place", `must be "top" or "bottom"`)
	}
	return f.err()
}
