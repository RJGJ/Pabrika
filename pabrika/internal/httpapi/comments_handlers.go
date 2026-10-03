package httpapi

import (
	"net/http"
)

// commentBody is the request body of POST /tickets/{id}/comments and PATCH /comments/{id}.
type commentBody struct {
	Body string `json:"body"`
}

// registerCommentRoutes registers the comment endpoints (spec section 10).
func (s *Server) registerCommentRoutes() {
	s.route("GET /api/v1/tickets/{id}/comments", Authed, false, s.listComments)
	s.route("POST /api/v1/tickets/{id}/comments", Authed, true, s.addComment)
	s.route("PATCH /api/v1/comments/{id}", Authed, true, s.editComment)
	s.route("DELETE /api/v1/comments/{id}", Authed, true, s.deleteComment)
}

func (s *Server) listComments(w http.ResponseWriter, r *http.Request) {
	limit, cursor, ok := pageParams(w, r)
	if !ok {
		return
	}
	page, err := s.svc.Comments.List(r.Context(), principal(r).Actor(), r.PathValue("id"), limit, cursor)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeList(w, mapComments(page.Items), page.NextCursor)
}

func (s *Server) addComment(w http.ResponseWriter, r *http.Request) {
	var in commentBody
	if !decodeJSON(w, r, &in) {
		return
	}
	c, err := s.svc.Comments.Add(r.Context(), principal(r).Actor(), r.PathValue("id"), in.Body)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, mapComment(c))
}

func (s *Server) editComment(w http.ResponseWriter, r *http.Request) {
	var in commentBody
	if !decodeJSON(w, r, &in) {
		return
	}
	c, err := s.svc.Comments.Edit(r.Context(), principal(r).Actor(), r.PathValue("id"), in.Body)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mapComment(c))
}

func (s *Server) deleteComment(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Comments.Delete(r.Context(), principal(r).Actor(), r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	noContent(w)
}
