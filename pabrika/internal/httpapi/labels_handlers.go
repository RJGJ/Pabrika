package httpapi

import (
	"net/http"

	"github.com/RJGJ/Pabrika/internal/service"
)

// registerLabelRoutes registers the label endpoints (phase 2 spec section 10).
func (s *Server) registerLabelRoutes() {
	s.route("GET /api/v1/projects/{id}/labels", Authed, false, s.listLabels)
	s.route("POST /api/v1/projects/{id}/labels", Authed, true, s.createLabel)
	s.route("PATCH /api/v1/labels/{id}", Authed, true, s.patchLabel)
	s.route("DELETE /api/v1/labels/{id}", Authed, true, s.deleteLabel)
}

type createLabelRequest struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type patchLabelRequest struct {
	Name  field[string] `json:"name"`
	Color field[string] `json:"color"`
}

func (s *Server) listLabels(w http.ResponseWriter, r *http.Request) {
	ls, err := s.svc.Labels.List(r.Context(), principal(r).Actor(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeList(w, mapLabels(ls), "")
}

func (s *Server) createLabel(w http.ResponseWriter, r *http.Request) {
	var req createLabelRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	l, err := s.svc.Labels.Create(r.Context(), principal(r).Actor(), r.PathValue("id"),
		service.LabelInput{Name: req.Name, Color: service.LabelColor(req.Color)})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, mapLabel(l))
}

func (s *Server) patchLabel(w http.ResponseWriter, r *http.Request) {
	var req patchLabelRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	in := service.UpdateLabelInput{Name: req.Name.opt()}
	if req.Color.Set {
		in.Color = service.Optional[service.LabelColor]{Set: true, Null: req.Color.Null, Value: service.LabelColor(req.Color.Value)}
	}
	l, err := s.svc.Labels.Update(r.Context(), principal(r).Actor(), r.PathValue("id"), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mapLabel(l))
}

func (s *Server) deleteLabel(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Labels.Delete(r.Context(), principal(r).Actor(), r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	noContent(w)
}
