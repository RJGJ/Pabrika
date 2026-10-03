package service

// ActorType says who is acting.
type ActorType string

const (
	ActorUser     ActorType = "user"
	ActorAPIToken ActorType = "api_token"
)

// Scope is an API token's scope. Only ScopeRead restricts anything: "" behaves as ScopeWrite.
type Scope string

const (
	ScopeRead  Scope = "read"
	ScopeWrite Scope = "write"
)

// Actor is who is acting. For a token, ID is the token id and UserID is the token's owner.
// Role permissions are always evaluated for UserID. Audit columns use (Type, ID).
// Scope and ProjectID carry the token's limits and are enforced by the service (authz.go).
type Actor struct {
	Type      ActorType
	ID        string
	UserID    string
	Scope     Scope
	ProjectID string // ULID; non-empty = token limited to this single project
}

// UserActor is a signed-in user (session): full write scope, no project limit.
func UserActor(userID string) Actor {
	return Actor{Type: ActorUser, ID: userID, UserID: userID, Scope: ScopeWrite}
}

// TokenActor is an API token acting for its owner.
func TokenActor(tokenID, ownerUserID string, scope Scope, projectID string) Actor {
	return Actor{Type: ActorAPIToken, ID: tokenID, UserID: ownerUserID, Scope: scope, ProjectID: projectID}
}

// IsToken reports whether the actor is an API token.
func (a Actor) IsToken() bool { return a.Type == ActorAPIToken }

// valid reports whether Type is one of the two known actor types.
func (a Actor) valid() bool { return a.Type == ActorUser || a.Type == ActorAPIToken }

// readOnly reports whether the actor's scope forbids writes.
func (a Actor) readOnly() bool { return a.Scope == ScopeRead }

// Role is a project membership role.
type Role string

const (
	RoleOwner  Role = "owner"
	RoleEditor Role = "editor"
	RoleViewer Role = "viewer"
)

func (r Role) rank() int {
	switch r {
	case RoleViewer:
		return 1
	case RoleEditor:
		return 2
	case RoleOwner:
		return 3
	}
	return 0
}

// AtLeast reports whether r ranks at or above min (viewer < editor < owner). Invalid roles never qualify.
func (r Role) AtLeast(min Role) bool { return r.rank() > 0 && r.rank() >= min.rank() }

// Valid reports whether r is one of the three roles.
func (r Role) Valid() bool { return r.rank() > 0 }
