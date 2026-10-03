package httpapi

// registerTokenRoutes is an extension point (WP6b): GET and POST /tokens, DELETE /tokens/{id}
// (all SessionOnly, none Write). Called from registerRoutes; see doc.go ("Adding an endpoint").
func (s *Server) registerTokenRoutes() {}
