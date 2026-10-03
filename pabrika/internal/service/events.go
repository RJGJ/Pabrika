package service

import "time"

// The events seam. Phase 3 adds only the hub (implements Publisher and StreamControl); it does
// not redefine anything here. Events carry ids only, never content.

// EventType names an event.
type EventType string

const (
	EventTicketCreated  EventType = "ticket.created"
	EventTicketUpdated  EventType = "ticket.updated"
	EventTicketMoved    EventType = "ticket.moved"
	EventTicketDeleted  EventType = "ticket.deleted"
	EventCommentAdded   EventType = "comment.added"
	EventCommentChanged EventType = "comment.changed"
	EventLabelChanged   EventType = "label.changed"
	EventMemberChanged  EventType = "member.changed"
	EventProjectUpdated EventType = "project.updated"
)

// EventActor is who caused an event: never the whole Actor. ID is the token id for api_token.
type EventActor struct {
	Type ActorType `json:"type"`
	ID   string    `json:"id"`
}

// EventActorOf projects an Actor to its event form.
func EventActorOf(a Actor) EventActor { return EventActor{Type: a.Type, ID: a.ID} }

// Event is published after a successful commit.
type Event struct {
	Type       EventType  `json:"type"`
	ProjectID  string     `json:"project_id"`
	TicketID   string     `json:"ticket_id,omitempty"`
	CommentID  string     `json:"comment_id,omitempty"`
	LabelID    string     `json:"label_id,omitempty"`
	UserID     string     `json:"user_id,omitempty"`    // member.changed: the affected member
	Renumbered bool       `json:"renumbered,omitempty"` // ticket.moved: the column was renumbered
	Actor      EventActor `json:"actor"`
	At         time.Time  `json:"at"`
}

// Publisher receives events after commit.
type Publisher interface{ Publish(Event) }

// NopPublisher drops every event.
type NopPublisher struct{}

// Publish implements Publisher.
func (NopPublisher) Publish(Event) {}

// CloseReason says why a stream was closed.
type CloseReason string

const (
	CloseNone     CloseReason = ""
	CloseSlow     CloseReason = "slow_client"
	CloseRemoved  CloseReason = "access_removed"
	CloseProject  CloseReason = "project_deleted"
	CloseShutdown CloseReason = "shutdown"
	CloseClient   CloseReason = "client_closed"
)

// StreamControl closes live streams (implemented by the phase 3 hub).
type StreamControl interface {
	CloseUser(projectID, userID string, reason CloseReason)
	CloseProject(projectID string, reason CloseReason)
}

type nopStreams struct{}

func (nopStreams) CloseUser(string, string, CloseReason) {}
func (nopStreams) CloseProject(string, CloseReason)      {}
