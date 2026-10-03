package httpapi

// registerCommentRoutes is an extension point (WP6f): GET and POST /tickets/{id}/comments and
// PATCH, DELETE /comments/{id}. Called from registerRoutes; see doc.go ("Adding an endpoint").
func (s *Server) registerCommentRoutes() {}
