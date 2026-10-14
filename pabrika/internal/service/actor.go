package service

// ActorType says who is acting.
type ActorType string

const (
	ActorUser     ActorType = "user"
	ActorAPIToken ActorType = "api_token"
)

// Scope is an API token's scope: read (reads only), comment (reads plus adding and editing
// one's own comments) or write (everything the owner's role allows). "" behaves as ScopeWrite
// (an Actor built by hand); any other unknown value fails closed and is treated as read.
type Scope string

const (
	ScopeRead    Scope = "read"
	ScopeComment Scope = "comment"
	ScopeWrite   Scope = "write"
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

// canWrite reports whether the scope allows general writes (tickets, labels, projects, ...).
// Fail closed: only write and the empty scope qualify; unknown values do not.
func (a Actor) canWrite() bool { return a.Scope == ScopeWrite || a.Scope == "" }

// canComment reports whether the scope allows adding and editing own comments.
func (a Actor) canComment() bool { return a.canWrite() || a.Scope == ScopeComment }

// readOnly reports whether the actor may not write in general: read, comment and unknown scopes.
// (A comment actor's one exception, comments, goes through scopeNeed in authz.go.)
func (a Actor) readOnly() bool { return !a.canWrite() }

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
