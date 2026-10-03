package httpapi

// registerLabelRoutes is an extension point (WP6e): GET and POST /projects/{id}/labels and
// PATCH, DELETE /labels/{id}. Called from registerRoutes; see doc.go ("Adding an endpoint").
func (s *Server) registerLabelRoutes() {}
