package httpapi

import (
	"net/http"

	"github.com/RJGJ/Pabrika/internal/service"
)

// registerActivityRoutes registers the project-wide feed (agentic phase 4): any member, session
// or token with read scope.
func (s *Server) registerActivityRoutes() {
	s.route("GET /api/v1/projects/{id}/activity", Authed, false, s.projectActivity)
}

func (s *Server) projectActivity(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, fields := parseLimit(q)
	if fields == nil {
		fields = map[string]string{}
	}
	once := func(name string) string {
		v, dup := queryOnce(q, name)
		if dup {
			fields[name] = "may be given only once"
		}
		return v
	}
	f := service.FeedFilter{
		Kind: once("kind"), ActorType: once("actor_type"), Ticket: once("ticket"),
		Limit: limit, Cursor: once("cursor"),
	}
	if _, dup := queryOnce(q, "limit"); dup {
		fields["limit"] = "may be given only once"
	}
	if len(fields) > 0 {
		writeValidation(w, fields)
		return
	}
	page, err := s.svc.Activity.ListProject(r.Context(), principal(r).Actor(), r.PathValue("id"), f)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeList(w, mapFeed(page.Items), page.NextCursor)
}
