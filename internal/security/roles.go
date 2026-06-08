package security

// Role is a bounded S.C.R.A.P. authorization role from ADR 0019.
type Role string

const (
	RoleDocumentWriter  Role = "document_writer"
	RoleDocumentReader  Role = "document_reader"
	RolePeerMember      Role = "peer_member"
	RoleAdminReader     Role = "admin_reader"
	RoleAdminOperator   Role = "admin_operator"
	RoleAdminBreakGlass Role = "admin_break_glass"
)

var knownRoles = map[Role]struct{}{
	RoleDocumentWriter:  {},
	RoleDocumentReader:  {},
	RolePeerMember:      {},
	RoleAdminReader:     {},
	RoleAdminOperator:   {},
	RoleAdminBreakGlass: {},
}

// RoleSet is an immutable-by-convention set of authorization roles.
type RoleSet map[Role]struct{}

// NewRoleSet returns a role set containing roles.
func NewRoleSet(roles ...Role) RoleSet {
	set := make(RoleSet, len(roles))
	for _, role := range roles {
		set[role] = struct{}{}
	}
	return set
}

func (s RoleSet) has(role Role) bool {
	_, ok := s[role]
	return ok
}

func (s RoleSet) clone() RoleSet {
	if len(s) == 0 {
		return nil
	}
	out := make(RoleSet, len(s))
	for role := range s {
		out[role] = struct{}{}
	}
	return out
}

func parseKnownRole(raw string) (Role, bool) {
	role := Role(cleanRole(raw))
	if role == "" {
		return "", false
	}
	_, ok := knownRoles[role]
	return role, ok
}

func cleanRole(raw string) string {
	return trimSpace(raw)
}
