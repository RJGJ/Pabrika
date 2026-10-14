package httpapi

import "net/http"

// decisionBody is the request body of POST /tickets/{id}/decisions.
type decisionBody struct {
	Gate    string  `json:"gate"`
	Verdict string  `json:"verdict"`
	Note    *string `json:"note"`
}

// registerDecisionRoutes registers the gate decision endpoints (agentic phase 4). Recording is
// session-only (no token, of any scope, can approve); reading needs only read scope.
func (s *Server) registerDecisionRoutes() {
	s.route("POST /api/v1/tickets/{id}/decisions", SessionOnly, true, s.createDecision)
	s.route("GET /api/v1/tickets/{id}/decisions", Authed, false, s.listDecisions)
}

func (s *Server) createDecision(w http.ResponseWriter, r *http.Request) {
	var in decisionBody
	if !decodeJSON(w, r, &in) {
		return
	}
	d, err := s.svc.Decisions.Record(r.Context(), principal(r).Actor(), r.PathValue("id"), serviceDecisionInput(in))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, mapDecision(d))
}

func (s *Server) listDecisions(w http.ResponseWriter, r *http.Request) {
	limit, cursor, ok := pageParams(w, r)
	if !ok {
		return
	}
	page, err := s.svc.Decisions.List(r.Context(), principal(r).Actor(), r.PathValue("id"), limit, cursor)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeList(w, mapDecisions(page.Items), page.NextCursor)
}
