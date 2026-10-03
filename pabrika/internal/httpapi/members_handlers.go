package httpapi

import (
	"net/http"

	"github.com/RJGJ/Pabrika/internal/service"
)

// registerMemberRoutes registers the member endpoints (phase 2 spec section 10). Add, role
// change and remove are session only.
func (s *Server) registerMemberRoutes() {
	s.route("GET /api/v1/projects/{id}/members", Authed, false, s.listMembers)
	s.route("POST /api/v1/projects/{id}/members", SessionOnly, true, s.addMember)
	s.route("PATCH /api/v1/projects/{id}/members/{userId}", SessionOnly, true, s.setMemberRole)
	s.route("DELETE /api/v1/projects/{id}/members/{userId}", SessionOnly, true, s.removeMember)
}

type addMemberRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

type setRoleRequest struct {
	Role string `json:"role"`
}

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request) {
	ms, err := s.svc.Members.List(r.Context(), principal(r).Actor(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeList(w, mapMembers(ms), "")
}

func (s *Server) addMember(w http.ResponseWriter, r *http.Request) {
	var req addMemberRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	m, err := s.svc.Members.Add(r.Context(), principal(r).Actor(), r.PathValue("id"), req.Email, service.Role(req.Role))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, mapMember(m))
}

func (s *Server) setMemberRole(w http.ResponseWriter, r *http.Request) {
	var req setRoleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	m, err := s.svc.Members.SetRole(r.Context(), principal(r).Actor(), r.PathValue("id"), r.PathValue("userId"), service.Role(req.Role))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mapMember(m))
}

func (s *Server) removeMember(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Members.Remove(r.Context(), principal(r).Actor(), r.PathValue("id"), r.PathValue("userId")); err != nil {
		s.fail(w, r, err)
		return
	}
	noContent(w)
}
