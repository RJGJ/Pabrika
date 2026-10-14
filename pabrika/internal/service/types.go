package service

import (
	"context"
	"time"
)

// Domain types and service interfaces (the contract of phase-1-foundation.md section 6.4).
// Input structs live in validate.go. Each interface is implemented in its own file
// (projects.go, members.go, labels.go, tickets.go, comments.go, activity.go, users.go).

// ---- Projects ----

type ProjectService interface {
	Create(ctx context.Context, actor Actor, in CreateProjectInput) (Project, error)
	Get(ctx context.Context, actor Actor, ref string) (ProjectDetail, error)
	Resolve(ctx context.Context, actor Actor, ref string) (ProjectRef, error)
	List(ctx context.Context, actor Actor, includeArchived bool) ([]ProjectSummary, error)
	Update(ctx context.Context, actor Actor, ref string, in UpdateProjectInput) (Project, error)
	Delete(ctx context.Context, actor Actor, ref string) error
}

type Project struct {
	ID, Key, Name, Description string
	ArchivedAt                 *time.Time
	CreatedAt, UpdatedAt       time.Time
}

type ProjectRef struct {
	ID, Key string
	Role    Role
}

type ProjectSummary struct {
	Project
	Role         Role
	TicketCounts map[Status]int
}

type ProjectDetail struct {
	Project
	Role         Role
	TicketCounts map[Status]int // all four keys, 0 when empty
}

// ---- Members ----

type MemberService interface {
	List(ctx context.Context, actor Actor, projectRef string) ([]Member, error)
	Add(ctx context.Context, actor Actor, projectRef, email string, role Role) (Member, error)
	SetRole(ctx context.Context, actor Actor, projectRef, userID string, role Role) (Member, error)
	Remove(ctx context.Context, actor Actor, projectRef, userID string) error
}

type Member struct {
	UserID, Email, DisplayName string
	Role                       Role
	CreatedAt                  time.Time
}

// ---- Labels ----

type LabelService interface {
	List(ctx context.Context, actor Actor, projectRef string) ([]Label, error)
	Create(ctx context.Context, actor Actor, projectRef string, in LabelInput) (Label, error)
	Update(ctx context.Context, actor Actor, labelID string, in UpdateLabelInput) (Label, error)
	Delete(ctx context.Context, actor Actor, labelID string) error
}

type Label struct {
	ID, ProjectID, Name string
	Color               LabelColor
}

// ---- Tickets ----

type TicketService interface {
	Create(ctx context.Context, actor Actor, projectRef string, in CreateTicketInput) (Ticket, error)
	Get(ctx context.Context, actor Actor, ref string) (Ticket, error)
	List(ctx context.Context, actor Actor, projectRef string, f TicketFilter) (TicketPage, error)
	Update(ctx context.Context, actor Actor, ref string, in UpdateTicketInput) (Ticket, error)
	Move(ctx context.Context, actor Actor, ref string, in MoveInput) (MoveResult, error)
	ChangeLabels(ctx context.Context, actor Actor, ref string, in ChangeLabelsInput) (Ticket, error)
	Delete(ctx context.Context, actor Actor, ref string) error
	Resolve(ctx context.Context, actor Actor, ref string) (TicketRef, error)
}

type MoveResult struct {
	Ticket     Ticket
	Renumbered bool
}

type TicketFilter struct {
	Status   *Status
	Priority *Priority
	Assignee *AssigneeFilter
	LabelID  string   // single label, kept for compatibility; merged into LabelIDs
	LabelIDs []string // all of these labels must be present (AND), at most MaxLabelFilter
	Query    string
	Limit    int
	Cursor   string
}

type AssigneeFilter struct {
	Unassigned bool
	UserID     string
}

type TicketPage struct {
	Items      []Ticket
	NextCursor string // "" = last page
}

type TicketRef struct {
	ID, ProjectID, Key string
	Number             int64
}

type UserRef struct{ ID, Email, DisplayName string }

// Ticket carries everything a transport renders, loaded with batched queries.
type Ticket struct {
	ID, ProjectID, ProjectKey string
	Number                    int64
	Ref                       string // "WEB-12"
	Title, Description        string
	Status                    Status
	Priority                  Priority
	AssigneeID                *string
	Assignee                  *UserRef
	Position                  float64
	DueDate                   *string
	Labels                    []Label // name order, never nil
	CommentCount              int
	CreatedAt, UpdatedAt      time.Time

	// Derived workflow fields (phase 4). PRURL, Branch and Round are a cache parsed from the
	// description and orchestrator bot comments. Decisions is set only for single-ticket reads
	// (Get, Create, Update, Move, ChangeLabels), never for list items; DecisionsLoaded says so.
	PRURL, Branch   *string
	Round           *Round
	Decisions       []WorkflowDecision
	DecisionsLoaded bool
}

// Round is the review round counter derived from the latest orchestrator marker.
type Round struct{ N, Max int }

// HasWorkflow reports whether any workflow field is set (the JSON object is null otherwise).
func (t Ticket) HasWorkflow() bool {
	return t.PRURL != nil || t.Branch != nil || t.Round != nil || len(t.Decisions) > 0
}

// DecisionActor is the human behind a decision.
type DecisionActor struct{ ID, Name, Email string }

// WorkflowDecision is the latest decision of one gate as shown on a single ticket. Current is
// true only when the decision is newer than the last time the gate label was applied.
type WorkflowDecision struct {
	Gate    Gate
	Verdict Verdict
	Note    *string
	At      time.Time
	By      DecisionActor
	Role    Role
	Current bool
}

// ---- Decisions ----

// DecisionService records and reads human gate decisions. Record is session-only and
// record-only: it never edits labels or columns.
type DecisionService interface {
	Record(ctx context.Context, actor Actor, ticketRef string, in RecordDecisionInput) (Decision, error)
	List(ctx context.Context, actor Actor, ticketRef string, limit int, cursor string) (DecisionPage, error)
}

// RecordDecisionInput is the body of a decision. Note is optional, required for changes_requested.
type RecordDecisionInput struct {
	Gate    Gate
	Verdict Verdict
	Note    *string
}

type Decision struct {
	ID, TicketID, TicketRef string
	Gate                    Gate
	Verdict                 Verdict
	Note                    *string
	Actor                   DecisionActor
	ActorRole               Role
	GateLabel               string
	CreatedAt               time.Time
}

type DecisionPage struct {
	Items      []Decision
	NextCursor string
}

// ---- Comments ----

type CommentService interface {
	List(ctx context.Context, actor Actor, ticketRef string, limit int, cursor string) (CommentPage, error)
	Latest(ctx context.Context, actor Actor, ticketRef string, n int) (comments []Comment, truncated bool, err error)
	Add(ctx context.Context, actor Actor, ticketRef, body string) (Comment, error)
	Edit(ctx context.Context, actor Actor, commentID, body string) (Comment, error)
	Delete(ctx context.Context, actor Actor, commentID string) error
	Resolve(ctx context.Context, actor Actor, commentID string) (CommentRef, error)
}

type CommentPage struct {
	Items      []Comment
	NextCursor string
}

type CommentRef struct{ ID, TicketID, TicketRef, ProjectID, ProjectKey string }

type Comment struct {
	ID, TicketID, TicketRef string
	Author                  CommentAuthor
	Body                    string
	CreatedAt               time.Time
	EditedAt                *time.Time
}

// CommentAuthor names who wrote a comment or acted in the activity log. Missing user or token
// rows resolve to the Name "deleted user" / "deleted token"; OwnerName is the token's owner.
type CommentAuthor struct {
	Type      ActorType
	ID, Name  string
	OwnerName string
}

// ---- Activity ----

type ActivityService interface {
	List(ctx context.Context, actor Actor, ticketRef string, limit int, cursor string) (ActivityPage, error)
	// ListProject is the project-wide feed: ticket activity (decisions included) and comments,
	// newest first.
	ListProject(ctx context.Context, actor Actor, projectRef string, f FeedFilter) (FeedPage, error)
}

// Feed item kinds.
const (
	FeedKindActivity = "activity"
	FeedKindComment  = "comment"
	FeedKindDecision = "decision"
)

// FeedFilter narrows the project feed. Kind is "", activity, comment or decision; ActorType is
// "", user or api_token; Ticket is a ticket reference (KEY-N or id) of the same project.
type FeedFilter struct {
	Kind, ActorType, Ticket string
	Since                   *time.Time // only rows strictly newer than this instant
	Limit                   int
	Cursor                  string
}

type FeedPage struct {
	Items      []FeedItem
	NextCursor string
}

// FeedItem is one row of the feed. Summary is set for comments only (body cut to 200 chars).
type FeedItem struct {
	ID, Kind  string
	TicketID  string
	TicketRef string
	Title     string
	Actor     CommentAuthor
	Action    string
	Changes   map[string][2]any
	Summary   string
	CreatedAt time.Time
}

type ActivityPage struct {
	Items      []Activity
	NextCursor string
}

type Activity struct {
	ID, TicketID string
	Actor        CommentAuthor
	Action       string
	Changes      map[string][2]any
	CreatedAt    time.Time
}

// ---- Users ----

type UserService interface {
	// UpdateProfile changes the display name. Session-only (a token actor gets `session_required`).
	UpdateProfile(ctx context.Context, actor Actor, displayName string) (User, error)

	// Create registers an account (signup and the CLI). The email is trimmed and lowercased;
	// all field errors come back in one 422; a duplicate email is KindConflict `email_taken`
	// (also when the UNIQUE constraint fires under a race). passwordHash comes from internal/auth.
	Create(ctx context.Context, email, displayName, passwordHash string) (User, error)
	// ValidateNew checks email and display name only (no DB), so signup can report every 422
	// field before it spends time hashing. Nil when valid.
	ValidateNew(email, displayName string) *Error
	// Credentials is the login lookup; ErrNotFound when the email is unknown.
	Credentials(ctx context.Context, email string) (userID, passwordHash string, err error)
	// PasswordHash returns the stored hash of a user; ErrNotFound when unknown.
	PasswordHash(ctx context.Context, userID string) (string, error)
	// SetPassword stores newHash and deletes the user's sessions except keepSessionHash
	// (a sessions.token_hash) in one transaction. keepSessionHash "" deletes them all.
	SetPassword(ctx context.Context, userID, newHash, keepSessionHash string) error
}

type User struct {
	ID, Email, DisplayName string
	CreatedAt              time.Time
}
