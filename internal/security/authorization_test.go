package security_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/security"
)

const principalA = "spiffe://scrap/cell/cell-a/member/member-a/member-1"

func TestRolePolicyAuthorizesPrincipalRoles(t *testing.T) {
	policy, err := security.ParseRolePolicy([]byte(`{
		"roles": ["document_reader", "peer_member", "admin_reader"],
		"principals": [
			{"id": "spiffe://scrap/cell/cell-a/member/member-a/member-1", "roles": ["peer_member", "admin_reader"]}
		]
	}`))
	if err != nil {
		t.Fatalf("ParseRolePolicy: %v", err)
	}

	authz := security.NewAuthorizer(policy)
	ctx, err := authz.ContextWithPrincipalID(context.Background(), principalA)
	if err != nil {
		t.Fatalf("ContextWithPrincipalID: %v", err)
	}
	if err := authz.Authorize(ctx, security.RolePeerMember); err != nil {
		t.Fatalf("Authorize(peer): %v", err)
	}

	err = authz.Authorize(ctx, security.RoleAdminOperator)
	if !errors.Is(err, security.ErrPermissionDenied) {
		t.Fatalf("Authorize(admin operator) = %v, want permission denied", err)
	}
	if strings.Contains(err.Error(), principalA) {
		t.Fatalf("authorization error leaked principal: %v", err)
	}
}

func TestRolePolicyRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "unknown role",
			body: `{"roles":["document_reader","root"],"principals":[{"id":"` + principalA + `","roles":["document_reader"]}]}`,
		},
		{
			name: "missing principals",
			body: `{"roles":["document_reader"]}`,
		},
		{
			name: "unknown principal role",
			body: `{"roles":["document_reader"],"principals":[{"id":"` + principalA + `","roles":["admin_operator"]}]}`,
		},
		{
			name: "oversized principal",
			body: `{"roles":["document_reader"],"principals":[{"id":"` + strings.Repeat("a", 513) + `","roles":["document_reader"]}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := security.ParseRolePolicy([]byte(tt.body))
			if err == nil {
				t.Fatal("ParseRolePolicy succeeded, want error")
			}
			if got := security.ErrorClass(err); got != security.ClassRolePolicy {
				t.Fatalf("ErrorClass = %q, want role_policy; err=%v", got, err)
			}
			if strings.Contains(err.Error(), principalA) {
				t.Fatalf("policy error leaked principal: %v", err)
			}
		})
	}
}

func TestContextWithPrincipalDeniesUnknownPrincipal(t *testing.T) {
	policy, err := security.ParseRolePolicy([]byte(`{
		"roles": ["document_reader"],
		"principals": [
			{"id": "spiffe://scrap/cell/cell-a/member/member-a/member-1", "roles": ["document_reader"]}
		]
	}`))
	if err != nil {
		t.Fatalf("ParseRolePolicy: %v", err)
	}

	authz := security.NewAuthorizer(policy)
	_, err = authz.ContextWithPrincipalID(context.Background(), "spiffe://scrap/cell/cell-a/member/member-b/member-2")
	if !errors.Is(err, security.ErrPermissionDenied) {
		t.Fatalf("ContextWithPrincipalID = %v, want permission denied", err)
	}
}
