package service

import (
	"errors"
	"log/slog"
	"sync"
)

// DefaultStreamBuffer is the per-subscription event buffer.
const DefaultStreamBuffer = 32

// ErrHubClosed is returned by Subscribe after Shutdown.
var ErrHubClosed = errors.New("event hub is shut down")

// HubOptions configures NewHub.
type HubOptions struct {
	StreamBuffer int          // per-subscription buffer; default DefaultStreamBuffer
	Logger       *slog.Logger // optional; logs slow-client drops (ids only, never event content)
}

// Hub is the in-memory event hub: it implements Publisher and StreamControl.
//
// Locking contract: one RWMutex guards the map and closed flag. Publish sends under RLock, which
// is safe only because Subscription.ch is never closed (only done is). Nothing blocks, logs or
// does I/O under the write lock, and end never touches the hub lock.
type Hub struct {
	buffer int
	log    *slog.Logger

	mu     sync.RWMutex
	closed bool
	subs   map[string]map[*Subscription]struct{}
}

var (
	_ Publisher     = (*Hub)(nil)
	_ StreamControl = (*Hub)(nil)
)

// NewHub builds a hub.
func NewHub(opts HubOptions) *Hub {
	if opts.StreamBuffer <= 0 {
		opts.StreamBuffer = DefaultStreamBuffer
	}
	return &Hub{buffer: opts.StreamBuffer, log: opts.Logger, subs: map[string]map[*Subscription]struct{}{}}
}

// Subscription is one live stream of a (project, user).
type Subscription struct {
	hub       *Hub
	projectID string
	userID    string
	ch        chan Event // never closed
	done      chan struct{}
	once      sync.Once
	reason    CloseReason
}

// C returns the buffered event channel. It is never closed; select on Done as well.
func (s *Subscription) C() <-chan Event { return s.ch }

// Done is closed exactly once when the subscription ends.
func (s *Subscription) Done() <-chan struct{} { return s.done }

// Reason is valid after Done is closed (CloseNone before).
func (s *Subscription) Reason() CloseReason {
	select {
	case <-s.done:
		return s.reason
	default:
		return CloseNone
	}
}

// end finishes the subscription; it never takes the hub lock. The first reason wins.
func (s *Subscription) end(reason CloseReason) {
	s.once.Do(func() {
		s.reason = reason
		close(s.done)
	})
}

// Close ends the subscription from the client side and removes it from the hub. Idempotent.
func (s *Subscription) Close() {
	s.hub.mu.Lock()
	s.hub.removeLocked(s)
	s.hub.mu.Unlock()
	s.end(CloseClient)
}

// removeLocked deletes s from the map, dropping an emptied project entry. Caller holds mu.
func (h *Hub) removeLocked(s *Subscription) {
	if m, ok := h.subs[s.projectID]; ok {
		delete(m, s)
		if len(m) == 0 {
			delete(h.subs, s.projectID)
		}
	}
}

// Subscribe registers a stream. It returns ErrHubClosed after Shutdown.
func (h *Hub) Subscribe(projectID, userID string) (*Subscription, error) {
	s := &Subscription{
		hub: h, projectID: projectID, userID: userID,
		ch: make(chan Event, h.buffer), done: make(chan struct{}),
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, ErrHubClosed
	}
	m := h.subs[projectID]
	if m == nil {
		m = map[*Subscription]struct{}{}
		h.subs[projectID] = m
	}
	m[s] = struct{}{}
	return s, nil
}

// Publish fans e out to the subscriptions of e.ProjectID. It never blocks; a subscription whose
// buffer is full is ended with CloseSlow.
func (h *Hub) Publish(e Event) {
	var slow []*Subscription
	h.mu.RLock()
	for s := range h.subs[e.ProjectID] {
		select {
		case s.ch <- e:
		default:
			slow = append(slow, s)
		}
	}
	h.mu.RUnlock()
	if len(slow) == 0 {
		return
	}
	h.mu.Lock()
	for _, s := range slow {
		h.removeLocked(s)
	}
	h.mu.Unlock()
	for _, s := range slow {
		s.end(CloseSlow)
		if h.log != nil {
			h.log.Warn("event stream dropped: slow client", "project_id", s.projectID, "user_id", s.userID)
		}
	}
}

// CloseUser ends every stream of userID on projectID.
func (h *Hub) CloseUser(projectID, userID string, reason CloseReason) {
	var hit []*Subscription
	h.mu.Lock()
	for s := range h.subs[projectID] {
		if s.userID == userID {
			hit = append(hit, s)
		}
	}
	for _, s := range hit {
		h.removeLocked(s)
	}
	h.mu.Unlock()
	for _, s := range hit {
		s.end(reason)
	}
}

// CloseProject ends every stream on projectID.
func (h *Hub) CloseProject(projectID string, reason CloseReason) {
	h.mu.Lock()
	m := h.subs[projectID]
	delete(h.subs, projectID)
	h.mu.Unlock()
	for s := range m {
		s.end(reason)
	}
}

// Shutdown ends all streams with CloseShutdown and rejects new subscriptions. Idempotent.
func (h *Hub) Shutdown() {
	var all []*Subscription
	h.mu.Lock()
	h.closed = true
	for _, m := range h.subs {
		for s := range m {
			all = append(all, s)
		}
	}
	h.subs = map[string]map[*Subscription]struct{}{}
	h.mu.Unlock()
	for _, s := range all {
		s.end(CloseShutdown)
	}
}

// SubscriberCount is the number of live subscriptions on a project.
func (h *Hub) SubscriberCount(projectID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs[projectID])
}
