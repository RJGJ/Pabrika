package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"unicode/utf8"

	"github.com/RJGJ/Pabrika/internal/auth"
)

// registerAuthRoutes registers the account endpoints under /api/v1 (phase 2 spec section 10).
// GET /healthz is built in core (server.go).
func (s *Server) registerAuthRoutes() {
	s.route("GET /api/v1/auth/config", Public, false, s.authConfig)
	s.route("POST /api/v1/auth/signup", Public, false, s.signup)
	s.route("POST /api/v1/auth/login", Public, false, s.login)
	s.route("POST /api/v1/auth/logout", SessionOnly, false, s.logout)
	s.route("GET /api/v1/auth/me", Authed, false, s.me)
	s.route("PATCH /api/v1/auth/me", SessionOnly, false, s.updateMe)
	s.route("POST /api/v1/auth/me/password", SessionOnly, false, s.changePassword)
}

type credentialsRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type signupRequest struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

type updateMeRequest struct {
	DisplayName string `json:"display_name"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

const invalidCredsMessage = "Invalid email or password"

func (s *Server) authConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		SignupEnabled bool `json:"signup_enabled"`
	}{s.cfg.AllowSignup})
}

// peekEmail reads the body leniently to learn the rate-limit email (empty when the body cannot
// be decoded or has no string email) and puts the bytes back so decodeJSON still sees the
// original body and reports its own errors.
func peekEmail(r *http.Request) string {
	if !bodyPresent(r) {
		return ""
	}
	raw, err := io.ReadAll(r.Body)
	r.Body = io.NopCloser(&replay{r: bytes.NewReader(raw), err: err})
	if err != nil {
		return ""
	}
	var probe struct {
		Email string `json:"email"`
	}
	if json.Unmarshal(raw, &probe) != nil {
		return ""
	}
	return probe.Email
}

// replay serves bytes already read, then the original read error (if any) instead of EOF.
type replay struct {
	r   *bytes.Reader
	err error
}

func (p *replay) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if err == io.EOF && p.err != nil {
		return n, p.err
	}
	return n, err
}

// limitByEmail counts the attempt against the (IP, email) bucket before any decoding or argon2
// work; it writes the 429 and returns false when the bucket is full.
func (s *Server) limitByEmail(w http.ResponseWriter, r *http.Request, l *auth.Limiter) bool {
	ok, retry := l.Allow(auth.LoginKey(s.clientIP(r), peekEmail(r)))
	if !ok {
		writeRateLimited(w, retry)
	}
	return ok
}

// startSession creates a fresh session (deleting the one the request presented, if any),
// sets the cookie and returns the account.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, userID string) (auth.AuthUser, error) {
	old := ""
	if c, err := r.Cookie(auth.SessionCookieName); err == nil {
		old = c.Value
	}
	value, _, err := s.sessions.Rotate(r.Context(), userID, old)
	if err != nil {
		return auth.AuthUser{}, err
	}
	rec, err := s.sessions.Lookup(r.Context(), auth.HashSessionValue(value))
	if err != nil {
		return auth.AuthUser{}, err
	}
	http.SetCookie(w, s.sessions.Cookie(value))
	return rec.User, nil
}

func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	// Signup off: indistinguishable from an unknown route, before decode, limiter and validation.
	if !s.cfg.AllowSignup {
		WriteError(w, http.StatusNotFound, CodeNotFound, "Not found")
		return
	}
	if !s.limitByEmail(w, r, s.signupLimiter) {
		return
	}
	var req signupRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	fields := map[string]string{}
	if verr := s.svc.Users.ValidateNew(req.Email, req.DisplayName); verr != nil {
		for k, v := range verr.Fields {
			fields[k] = v
		}
	}
	if msg := auth.PasswordProblem(req.Password); msg != "" {
		fields["password"] = msg
	}
	if len(fields) > 0 {
		writeValidation(w, fields)
		return
	}
	hash, err := s.hasher.Hash(r.Context(), req.Password)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	u, err := s.svc.Users.Create(r.Context(), req.Email, req.DisplayName, hash)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	user, err := s.startSession(w, r, u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, authUserEnvelope{User: mapAuthUser(user)})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.limitByEmail(w, r, s.loginLimiter) {
		return
	}
	var req credentialsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	userID, ok, err := s.checkCredentials(r.Context(), req)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !ok {
		WriteError(w, http.StatusUnauthorized, CodeInvalidCreds, invalidCredsMessage)
		return
	}
	user, err := s.startSession(w, r, userID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, authUserEnvelope{User: mapAuthUser(user)})
}

func passwordTooLong(pw string) bool { return utf8.RuneCountInString(pw) > auth.MaxPasswordLen }

// checkCredentials verifies email and password. Every failure path burns one argon2
// verification (a dummy one when there is no hash to check) so timing does not depend on the
// input. The error is non-nil only for infrastructure failures.
func (s *Server) checkCredentials(ctx context.Context, req credentialsRequest) (userID string, ok bool, err error) {
	var id, hash string
	if req.Email != "" && req.Password != "" {
		id, hash, err = s.svc.Users.Credentials(ctx, req.Email)
		if err != nil {
			if e, _ := mapError(err); e.Status != http.StatusNotFound {
				return "", false, err
			}
			hash = ""
		}
	}
	if hash == "" || passwordTooLong(req.Password) {
		return "", false, s.hasher.DummyVerify(ctx, "")
	}
	match, err := s.hasher.Verify(ctx, req.Password, hash)
	if err != nil || !match {
		return "", false, err
	}
	return id, true, nil
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if p.Session == nil {
		WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "Authentication required")
		return
	}
	if err := s.sessions.Delete(r.Context(), p.Session.TokenHash); err != nil {
		s.fail(w, r, err)
		return
	}
	http.SetCookie(w, s.sessions.ClearCookie())
	noContent(w)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, mapMe(principal(r)))
}

func (s *Server) updateMe(w http.ResponseWriter, r *http.Request) {
	var req updateMeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	u, err := s.svc.Users.UpdateProfile(r.Context(), principal(r).Actor(), req.DisplayName)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, authUserEnvelope{User: mapServiceUser(u)})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if ok, retry := s.passwordLimiter.Allow(auth.PasswordKey(p.User.ID, s.clientIP(r))); !ok {
		writeRateLimited(w, retry)
		return
	}
	var req changePasswordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	cur, err := s.svc.Users.PasswordHash(r.Context(), p.User.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	match := false
	if !passwordTooLong(req.CurrentPassword) {
		if match, err = s.hasher.Verify(r.Context(), req.CurrentPassword, cur); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if !match {
		writeValidation(w, map[string]string{"current_password": "Incorrect password"})
		return
	}
	if msg := auth.PasswordProblem(req.NewPassword); msg != "" {
		writeValidation(w, map[string]string{"new_password": msg})
		return
	}
	hash, err := s.hasher.Hash(r.Context(), req.NewPassword)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.svc.Users.SetPassword(r.Context(), p.User.ID, hash, p.Session.TokenHash); err != nil {
		s.fail(w, r, err)
		return
	}
	noContent(w)
}
