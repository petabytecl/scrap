package security

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"unicode"
)

const maxPrincipalIDBytes = 512

// RolePolicy maps authenticated principal identifiers to bounded role sets.
type RolePolicy struct {
	roles      RoleSet
	principals map[string]RoleSet
}

type rolePolicyFile struct {
	Roles      []string              `json:"roles"`
	Principals []rolePolicyPrincipal `json:"principals"`
}

type rolePolicyPrincipal struct {
	ID    string   `json:"id"`
	Roles []string `json:"roles"`
}

// LoadRolePolicy loads and validates a role policy JSON file.
func LoadRolePolicy(path string) (*RolePolicy, error) {
	if strings.TrimSpace(path) == "" {
		return nil, newGateError(ClassRolePolicy, "SCRAP_ROLE_POLICY_FILE", "policy path is required")
	}
	data, err := os.ReadFile(path) //nolint:gosec // Operator-configured role policy path is the intended startup input.
	if err != nil {
		return nil, newGateError(ClassRolePolicy, "SCRAP_ROLE_POLICY_FILE", "policy file is unreadable")
	}
	return ParseRolePolicy(data)
}

// ParseRolePolicy parses and validates a role policy JSON document.
func ParseRolePolicy(data []byte) (*RolePolicy, error) {
	var doc rolePolicyFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, newGateError(ClassRolePolicy, "SCRAP_ROLE_POLICY_FILE", "policy file must be valid JSON")
	}
	if err := dec.Decode(&struct{}{}); !errorsIsEOF(err) {
		return nil, newGateError(ClassRolePolicy, "SCRAP_ROLE_POLICY_FILE", "policy file must contain one JSON document")
	}
	return rolePolicyFromFile(doc)
}

func errorsIsEOF(err error) bool {
	return errors.Is(err, io.EOF)
}

func rolePolicyFromFile(doc rolePolicyFile) (*RolePolicy, error) {
	roles, err := declaredRoles(doc.Roles)
	if err != nil {
		return nil, err
	}
	principals, err := principalRoles(doc.Principals, roles)
	if err != nil {
		return nil, err
	}
	return &RolePolicy{roles: roles, principals: principals}, nil
}

func declaredRoles(rawRoles []string) (RoleSet, error) {
	if len(rawRoles) == 0 {
		return nil, newGateError(ClassRolePolicy, "SCRAP_ROLE_POLICY_FILE", "role policy must contain at least one role")
	}
	roles := make(RoleSet, len(rawRoles))
	for _, raw := range rawRoles {
		role, ok := parseKnownRole(raw)
		if !ok {
			return nil, newGateError(ClassRolePolicy, "SCRAP_ROLE_POLICY_FILE", "role policy contains an unknown role")
		}
		if roles.has(role) {
			return nil, newGateError(ClassRolePolicy, "SCRAP_ROLE_POLICY_FILE", "role policy contains a duplicate role")
		}
		roles[role] = struct{}{}
	}
	return roles, nil
}

func principalRoles(rawPrincipals []rolePolicyPrincipal, declared RoleSet) (map[string]RoleSet, error) {
	if len(rawPrincipals) == 0 {
		return nil, newGateError(ClassRolePolicy, "SCRAP_ROLE_POLICY_FILE", "role policy must contain at least one principal")
	}
	principals := make(map[string]RoleSet, len(rawPrincipals))
	for _, raw := range rawPrincipals {
		id, ok := cleanPrincipalID(raw.ID)
		if !ok {
			return nil, newGateError(ClassRolePolicy, "SCRAP_ROLE_POLICY_FILE", "principal id is invalid")
		}
		if _, exists := principals[id]; exists {
			return nil, newGateError(ClassRolePolicy, "SCRAP_ROLE_POLICY_FILE", "role policy contains a duplicate principal")
		}
		roles, err := rolesForPrincipal(raw.Roles, declared)
		if err != nil {
			return nil, err
		}
		principals[id] = roles
	}
	return principals, nil
}

func rolesForPrincipal(rawRoles []string, declared RoleSet) (RoleSet, error) {
	if len(rawRoles) == 0 {
		return nil, newGateError(ClassRolePolicy, "SCRAP_ROLE_POLICY_FILE", "principal must contain at least one role")
	}
	roles := make(RoleSet, len(rawRoles))
	for _, raw := range rawRoles {
		role, ok := parseKnownRole(raw)
		if !ok || !declared.has(role) {
			return nil, newGateError(ClassRolePolicy, "SCRAP_ROLE_POLICY_FILE", "principal role is not declared")
		}
		if roles.has(role) {
			return nil, newGateError(ClassRolePolicy, "SCRAP_ROLE_POLICY_FILE", "principal contains a duplicate role")
		}
		roles[role] = struct{}{}
	}
	return roles, nil
}

func (p *RolePolicy) rolesForPrincipal(id string) (RoleSet, bool) {
	if p == nil {
		return nil, false
	}
	cleanID, ok := cleanPrincipalID(id)
	if !ok {
		return nil, false
	}
	roles, ok := p.principals[cleanID]
	return roles.clone(), ok
}

func cleanPrincipalID(raw string) (string, bool) {
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > maxPrincipalIDBytes {
		return "", false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", false
		}
	}
	return value, true
}

func trimSpace(raw string) string {
	return strings.TrimSpace(raw)
}
