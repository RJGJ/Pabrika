package httpapi

// registerAuthRoutes is an extension point (WP6a): register GET /auth/config, POST /auth/signup,
// POST /auth/login, POST /auth/logout, GET /auth/me, PATCH /auth/me and POST /auth/me/password
// under /api/v1. GET /healthz is built in core (server.go) and is not part of this file.
// Called from registerRoutes; see doc.go ("Adding an endpoint").
func (s *Server) registerAuthRoutes() {}
