// Package auth implements tt's server-side identity: the JWT claims every
// API request carries, the issuer that signs them, and macaroon API keys
// that holders can narrow offline for sub-agents.
package auth

import (
	"context"
	"fmt"
	"slices"

	"github.com/golang-jwt/jwt/v5"
)

// Kind is the type of principal behind a request.
type Kind string

const (
	KindUser    Kind = "user"
	KindAgent   Kind = "agent"
	KindService Kind = "service"
)

// Role is a principal's role in a project. Roles are ordered: each grants
// everything the previous one does.
type Role string

const (
	RoleViewer Role = "viewer"
	RoleMember Role = "member"
	RoleOwner  Role = "owner"
)

var roleRank = map[Role]int{RoleViewer: 1, RoleMember: 2, RoleOwner: 3}

func ParseRole(s string) (Role, error) {
	r := Role(s)
	if roleRank[r] == 0 {
		return "", fmt.Errorf("invalid role %q: must be viewer, member or owner", s)
	}
	return r, nil
}

// MinRole returns the lesser of two roles; an empty role means no limit.
func MinRole(a, b Role) Role {
	if a == "" {
		return b
	}
	if b == "" || roleRank[a] <= roleRank[b] {
		return a
	}
	return b
}

// Action is a single grantable permission.
type Action string

const (
	ActTaskRead      Action = "task:read"
	ActTaskCreate    Action = "task:create"
	ActTaskEdit      Action = "task:edit"
	ActTaskStatus    Action = "task:status"
	ActTaskAssign    Action = "task:assign"
	ActTaskClaim     Action = "task:claim"
	ActCommentCreate Action = "comment:create"
	ActRelationWrite Action = "relation:write"
	ActImport        Action = "import"
	ActKeyCreate     Action = "key:create"
	ActProjectAdmin  Action = "project:admin"
)

var memberActions = []Action{
	ActTaskRead, ActTaskCreate, ActTaskEdit, ActTaskStatus, ActTaskAssign,
	ActTaskClaim, ActCommentCreate, ActRelationWrite, ActImport,
}

// RoleActions lists what each role grants.
var RoleActions = map[Role][]Action{
	RoleViewer: {ActTaskRead},
	RoleMember: memberActions,
	RoleOwner:  append(slices.Clone(memberActions), ActKeyCreate, ActProjectAdmin),
}

func ParseAction(s string) (Action, error) {
	a := Action(s)
	if !slices.Contains(RoleActions[RoleOwner], a) {
		return "", fmt.Errorf("unknown action %q", s)
	}
	return a, nil
}

// Claims is the verified identity attached to every API request, whatever
// credential the caller presented.
type Claims struct {
	jwt.RegisteredClaims

	Kind       Kind            `json:"kind"`
	Handle     string          `json:"handle"`
	OnBehalfOf string          `json:"on_behalf_of,omitempty"`
	Projects   map[string]Role `json:"projects"`
	// Admin is set for server administrators, who can create projects.
	Admin bool `json:"admin,omitempty"`

	// Set only for tokens derived from an attenuated API key.
	TaskScope []string `json:"task_scope,omitempty"`
	Actions   []Action `json:"actions,omitempty"`
	Label     string   `json:"label,omitempty"`

	// Restricted is set when the key behind the token is narrower than its
	// principal: scoped by project or role on its record, or by any caveat.
	Restricted bool `json:"restricted,omitempty"`

	// KeyID is the API key the token came from, for revocation checks.
	KeyID string `json:"key_id,omitempty"`
}

// Can reports whether the claims allow action in project. Task-scope limits
// are checked separately, since they need the task tree.
func (c *Claims) Can(project string, action Action) bool {
	role, ok := c.Projects[project]
	if !ok || !slices.Contains(RoleActions[role], action) {
		return false
	}
	return c.Actions == nil || slices.Contains(c.Actions, action)
}

// Attribution is how writes by this principal are shown, e.g.
// "claude/opus4.7 (sub-reviewer) on behalf of jrdn".
func (c *Claims) Attribution(ownerHandle string) string {
	s := c.Handle
	if c.Label != "" {
		s += " (" + c.Label + ")"
	}
	if ownerHandle != "" {
		s += " on behalf of " + ownerHandle
	}
	return s
}

type claimsKey struct{}

func WithClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, claimsKey{}, c)
}

// FromContext returns the request's claims, or nil if unauthenticated.
func FromContext(ctx context.Context) *Claims {
	c, _ := ctx.Value(claimsKey{}).(*Claims)
	return c
}
