// Package peerauth maps verified mTLS peer certificate subjects to
// canonical service identities. The parser enforces a single,
// narrowly-defined certificate shape so header-derived identity
// claims cannot be smuggled into the State Registry by an attacker
// controlling any one attribute on the certificate subject.
//
// All exports in this package are derived from the verified peer
// certificate only — never from request headers or query parameters.
// The "withPeerIdentity" middleware in cmd/state-registry is the
// only caller; it strips every inbound X-FlowAI-* identity header
// before invoking Parse so the only authority for those headers is
// the verified cert subject parsed here.
package peerauth

import (
	"crypto/x509"
	"fmt"
	"strings"
)

// Role is the narrow set of canonical service roles the State
// Registry accepts on the verified peer certificate. Roles outside
// this set are rejected by Parse and never produce a trusted header.
type Role string

const (
	RoleAdmin          Role = "admin"
	RoleListener       Role = "listener"
	RoleTeamExecutor   Role = "team-executor"
	RoleSystemExecutor Role = "system-executor"
	RoleGateway        Role = "gateway"
)

// Identity is the canonical service identity derived from a single
// verified peer certificate subject. Every field is read from the
// verified subject only; the only mutating caller (Apply) does NOT
// reach back into any request-derived state.
//
// The empty-string checks for TeamID and SourceSystemID carry the
// role-specific contract:
//
//   - admin / system-executor: TeamID == "" and SourceSystemID == ""
//   - listener: TeamID != "" and SourceSystemID != ""
//   - team-executor / gateway: TeamID != "" and SourceSystemID == ""
type Identity struct {
	Role           Role
	CN             string
	TeamID         string
	SourceSystemID string
}

// Claims is the parse result returned by Parse. Valid=false ALWAYS
// means every header Apply would set must be left unset, regardless
// of what fields are populated. Valid=true guarantees every field
// satisfies the role-specific contract documented on Identity.
type Claims struct {
	Identity Identity
	// Valid reports whether the verified peer certificate subject
	// satisfies the strict shape defined by Parse. When Valid=false
	// the caller MUST NOT promote any header derived from
	// Identity; Reason carries the human-readable reason for logs.
	Valid  bool
	Reason string
}

// roleRequiresTeam reports whether the named role requires a
// non-empty TeamID. Admin and system-executor are team-less by
// design; the other three roles are bound to exactly one team.
func roleRequiresTeam(role Role) bool {
	switch role {
	case RoleAdmin, RoleSystemExecutor:
		return false
	default:
		return true
	}
}

// roleRequiresSourceSystem reports whether the named role requires a
// non-empty source-system identifier. Only the listener role carries
// one — admin / executor / gateway sessions do NOT bind a source
// system.
func roleRequiresSourceSystem(role Role) bool {
	switch role {
	case RoleListener:
		return true
	default:
		return false
	}
}

// Parse enforces a strict, single parser for verified peer
// certificate subjects. The shape:
//
//   - exactly one OrganizationalUnit containing exactly one role
//     drawn from {admin, listener, team-executor, system-executor,
//     gateway};
//   - non-empty CommonName;
//   - at most one Organization (zero for admin / system-executor,
//     exactly one for the other three roles);
//   - listener: the Organization carries the team identifier and
//     Subject.SerialNumber carries the source-system identifier;
//   - team-executor and gateway: the Organization carries the team
//     identifier and Subject.SerialNumber is empty;
//   - admin and system-executor: Organization is empty and
//     Subject.SerialNumber is empty.
//
// Any deviation returns Claims{Valid:false, Reason:...} WITHOUT
// partial identity. The caller (withPeerIdentity) MUST leave every
// X-FlowAI-* identity header unset on Valid=false.
func Parse(cert *x509.Certificate) Claims {
	if cert == nil {
		return Claims{Reason: "no peer certificate"}
	}
	subject := cert.Subject
	if n := len(subject.OrganizationalUnit); n != 1 {
		return Claims{
			Reason: fmt.Sprintf("exactly one OrganizationalUnit required, got %d", n),
		}
	}
	role := Role(strings.TrimSpace(subject.OrganizationalUnit[0]))
	if !knownRole(role) {
		return Claims{
			Reason: fmt.Sprintf("OU role %q is not in the known set", string(role)),
		}
	}
	cn := strings.TrimSpace(subject.CommonName)
	if cn == "" {
		return Claims{Reason: "CN must be non-empty"}
	}
	if n := len(subject.Organization); n > 1 {
		return Claims{
			Reason: fmt.Sprintf("at most one Organization allowed, got %d", n),
		}
	}
	var teamID string
	if len(subject.Organization) == 1 {
		teamID = strings.TrimSpace(subject.Organization[0])
	}
	if roleRequiresTeam(role) && teamID == "" {
		return Claims{
			Reason: fmt.Sprintf("role %q requires exactly one team in Organization", string(role)),
		}
	}
	if !roleRequiresTeam(role) && teamID != "" {
		return Claims{
			Reason: fmt.Sprintf("role %q must not carry a team", string(role)),
		}
	}
	serial := strings.TrimSpace(subject.SerialNumber)
	if roleRequiresSourceSystem(role) && serial == "" {
		return Claims{
			Reason: fmt.Sprintf("role %q requires a source-system identifier in Subject.SerialNumber", string(role)),
		}
	}
	if !roleRequiresSourceSystem(role) && serial != "" {
		return Claims{
			Reason: fmt.Sprintf("role %q must not carry a source-system serial", string(role)),
		}
	}
	return Claims{
		Valid: true,
		Identity: Identity{
			Role:           role,
			CN:             cn,
			TeamID:         teamID,
			SourceSystemID: serial,
		},
	}
}

func knownRole(role Role) bool {
	switch role {
	case RoleAdmin, RoleListener, RoleTeamExecutor, RoleSystemExecutor, RoleGateway:
		return true
	default:
		return false
	}
}

// Apply promotes the canonical identity into the canonical
// X-FlowAI-* request headers. Apply is a no-op on Valid=false so a
// caller that mistakenly forwards an invalid shape produces zero
// identity headers.
//
// The set of headers written here is the SAME set the
// withPeerIdentity middleware in cmd/state-registry/main.go strips
// before Parse; both ends must agree or headers can leak from
// caller-supplied values.
func (c Claims) Apply(headers interface {
	Set(key, value string)
}) {
	if !c.Valid {
		return
	}
	id := c.Identity
	headers.Set("X-FlowAI-Role", string(id.Role))
	switch id.Role {
	case RoleAdmin:
		headers.Set("X-FlowAI-Admin-Subject", id.CN)
	case RoleListener:
		if id.TeamID != "" {
			headers.Set("X-FlowAI-Team-Id", id.TeamID)
		}
		headers.Set("X-FlowAI-Listener-Identity", id.CN)
		if id.SourceSystemID != "" {
			headers.Set("X-FlowAI-Source-System-Id", id.SourceSystemID)
		}
	case RoleTeamExecutor, RoleSystemExecutor:
		if id.TeamID != "" {
			headers.Set("X-FlowAI-Team-Id", id.TeamID)
		}
		headers.Set("X-FlowAI-Executor-Id", id.CN)
	case RoleGateway:
		if id.TeamID != "" {
			headers.Set("X-FlowAI-Team-Id", id.TeamID)
		}
		headers.Set("X-FlowAI-Operator-Id", id.CN)
	}
}
