package httpapi

import (
	"net/http"
	"unicode/utf8"

	"github.com/RJGJ/Pabrika/internal/service"
)

// maxQueryLen is the cap on the `q` ticket filter (characters).
const maxQueryLen = 200

// registerTicketRoutes registers the ticket endpoints, including move and activity (spec
// section 10). Ticket paths accept the ULID or KEY-N; project paths the key or ULID.
func (s *Server) registerTicketRoutes() {
	s.route("GET /api/v1/projects/{id}/tickets", Authed, false, s.listTickets)
	s.route("POST /api/v1/projects/{id}/tickets", Authed, true, s.createTicket)
	s.route("GET /api/v1/tickets/{id}", Authed, false, s.getTicket)
	s.route("PATCH /api/v1/tickets/{id}", Authed, true, s.updateTicket)
	s.route("DELETE /api/v1/tickets/{id}", Authed, true, s.deleteTicket)
	s.route("POST /api/v1/tickets/{id}/move", Authed, true, s.moveTicket)
	s.route("GET /api/v1/tickets/{id}/activity", Authed, false, s.ticketActivity)
}

// ticketPatchHints turns the unknown-field 400 for status and position into a pointer to /move.
var ticketPatchHints = map[string]string{
	"status":   "status cannot be changed here; use /move",
	"position": "position cannot be changed here; use /move",
}

func (s *Server) listTickets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	fields := map[string]string{}
	limit, lf := parseLimit(q)
	for k, v := range lf {
		fields[k] = v
	}
	once := func(name string) string {
		v, dup := queryOnce(q, name)
		if dup {
			fields[name] = "may be given only once"
		}
		return v
	}
	status, priority := once("status"), once("priority")
	assignee, label, text := once("assignee"), once("label"), once("q")
	once("cursor")
	if _, dup := queryOnce(q, "limit"); dup {
		fields["limit"] = "may be given only once"
	}
	if utf8.RuneCountInString(text) > maxQueryLen {
		fields["q"] = "must be 200 characters or fewer"
	}
	f := service.TicketFilter{LabelID: label, Query: text, Limit: limit, Cursor: q.Get("cursor")}
	if status != "" {
		v := service.Status(status)
		if !v.Valid() {
			fields["status"] = "must be one of backlog, todo, in_progress, done"
		}
		f.Status = &v
	}
	if priority != "" {
		v := service.Priority(priority)
		if !v.Valid() {
			fields["priority"] = "must be one of low, medium, high, urgent"
		}
		f.Priority = &v
	}
	p := principal(r)
	switch assignee {
	case "":
	case "none":
		f.Assignee = &service.AssigneeFilter{Unassigned: true}
	case "me":
		f.Assignee = &service.AssigneeFilter{UserID: p.User.ID}
	default:
		f.Assignee = &service.AssigneeFilter{UserID: assignee}
	}
	if len(fields) > 0 {
		writeValidation(w, fields)
		return
	}
	page, err := s.svc.Tickets.List(r.Context(), p.Actor(), r.PathValue("id"), f)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeList(w, mapTicketList(page.Items), page.NextCursor)
}

func (s *Server) createTicket(w http.ResponseWriter, r *http.Request) {
	var in service.CreateTicketInput
	if !decodeJSON(w, r, &in) {
		return
	}
	t, err := s.svc.Tickets.Create(r.Context(), principal(r).Actor(), r.PathValue("id"), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, ticketFull(t))
}

func (s *Server) getTicket(w http.ResponseWriter, r *http.Request) {
	t, err := s.svc.Tickets.Get(r.Context(), principal(r).Actor(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ticketFull(t))
}

func (s *Server) updateTicket(w http.ResponseWriter, r *http.Request) {
	var in service.UpdateTicketInput
	if !decodeJSON(w, r, &in, decodeOpts{Hints: ticketPatchHints}) {
		return
	}
	t, err := s.svc.Tickets.Update(r.Context(), principal(r).Actor(), r.PathValue("id"), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ticketFull(t))
}

func (s *Server) deleteTicket(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Tickets.Delete(r.Context(), principal(r).Actor(), r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	noContent(w)
}

func (s *Server) moveTicket(w http.ResponseWriter, r *http.Request) {
	var in service.MoveInput
	if !decodeJSON(w, r, &in) {
		return
	}
	res, err := s.svc.Tickets.Move(r.Context(), principal(r).Actor(), r.PathValue("id"), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mapMove(res))
}

func (s *Server) ticketActivity(w http.ResponseWriter, r *http.Request) {
	limit, cursor, ok := pageParams(w, r)
	if !ok {
		return
	}
	page, err := s.svc.Activity.List(r.Context(), principal(r).Actor(), r.PathValue("id"), limit, cursor)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeList(w, mapActivities(page.Items), page.NextCursor)
}
