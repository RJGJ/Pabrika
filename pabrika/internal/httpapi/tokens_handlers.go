package httpapi

import (
	"net/http"

	"github.com/RJGJ/Pabrika/internal/auth"
	"github.com/RJGJ/Pabrika/internal/service"
)

// registerTokenRoutes registers the caller's API token management. All routes are SessionOnly
// (tokens cannot manage tokens) and none needs Write.
func (s *Server) registerTokenRoutes() {
	s.route("GET /api/v1/tokens", SessionOnly, false, s.listTokens)
	s.route("POST /api/v1/tokens", SessionOnly, false, s.createToken)
	s.route("DELETE /api/v1/tokens/{id}", SessionOnly, false, s.revokeToken)
}

type createTokenRequest struct {
	Name      string  `json:"name"`
	Scope     string  `json:"scope"`
	ProjectID *string `json:"project_id"` // key or ULID; null or absent = all projects
}

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.Tokens.List(r.Context(), principal(r).Actor())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeList(w, mapTokens(items), "")
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var req createTokenRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	in := service.CreateTokenInput{Name: req.Name, Scope: service.Scope(req.Scope)}
	if req.ProjectID != nil {
		in.ProjectRef = *req.ProjectID
	}
	secret, hash, prefix := auth.Generate()
	in.Hash, in.Prefix = hash, prefix
	t, err := s.svc.Tokens.Create(r.Context(), principal(r).Actor(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, mapCreatedToken(t, secret))
}

func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Tokens.Revoke(r.Context(), principal(r).Actor(), r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	noContent(w)
}
