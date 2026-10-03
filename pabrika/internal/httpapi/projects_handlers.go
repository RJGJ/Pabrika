package httpapi

import (
	"net/http"

	"github.com/RJGJ/Pabrika/internal/service"
)

// registerProjectRoutes registers GET and POST /projects and GET, PATCH, DELETE /projects/{id}
// (phase 2 spec section 10). {id} is the project key or ULID.
func (s *Server) registerProjectRoutes() {
	s.route("GET /api/v1/projects", Authed, false, s.listProjects)
	s.route("POST /api/v1/projects", Authed, true, s.createProject)
	s.route("GET /api/v1/projects/{id}", Authed, false, s.getProject)
	s.route("PATCH /api/v1/projects/{id}", Authed, true, s.patchProject)
	s.route("DELETE /api/v1/projects/{id}", SessionOnly, true, s.deleteProject)
}

type createProjectRequest struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type patchProjectRequest struct {
	Name        field[string] `json:"name"`
	Description field[string] `json:"description"`
	Archived    field[bool]   `json:"archived"`
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	archived, dup := queryOnce(r.URL.Query(), "archived")
	if dup || (archived != "" && archived != "true" && archived != "false") {
		writeValidation(w, map[string]string{"archived": "must be true or false"})
		return
	}
	p := principal(r)
	list, err := s.svc.Projects.List(r.Context(), p.Actor(), archived == "true")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	items := make([]projectView, 0, len(list))
	for _, it := range list {
		items = append(items, mapProjectSummary(p, it))
	}
	writeList(w, items, "")
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var req createProjectRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	p := principal(r)
	proj, err := s.svc.Projects.Create(r.Context(), p.Actor(), service.CreateProjectInput{
		Key: req.Key, Name: req.Name, Description: req.Description,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, mapProject(p, proj, service.RoleOwner))
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	d, err := s.svc.Projects.Get(r.Context(), p.Actor(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mapProjectDetail(p, d))
}

func (s *Server) patchProject(w http.ResponseWriter, r *http.Request) {
	var req patchProjectRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	p := principal(r)
	proj, err := s.svc.Projects.Update(r.Context(), p.Actor(), r.PathValue("id"), service.UpdateProjectInput{
		Name: req.Name.opt(), Description: req.Description.opt(), Archived: req.Archived.opt(),
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Only owners may patch, so the caller's role is owner (effectiveRole caps it for a
	// read-scope token, which cannot get here because the route is a write).
	writeJSON(w, http.StatusOK, mapProject(p, proj, service.RoleOwner))
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Projects.Delete(r.Context(), principal(r).Actor(), r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	noContent(w)
}
