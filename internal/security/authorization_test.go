package security_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/petabytecl/scrap/internal/security"
	securityfixture "github.com/petabytecl/scrap/test/fixtures/security"
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
	if got := security.AuthorizationStatusForError(err); got != security.AuthorizationStatusMissingRole {
		t.Fatalf("AuthorizationStatusForError = %q, want missing_role", got)
	}
	if got := authz.AuthorizationStatus(); got != security.AuthorizationStatusMissingRole {
		t.Fatalf("AuthorizationStatus = %q, want missing_role", got)
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
		{
			name: "duplicate declared role",
			body: `{"roles":["document_reader","document_reader"],"principals":[{"id":"` + principalA + `","roles":["document_reader"]}]}`,
		},
		{
			name: "duplicate principal",
			body: `{"roles":["document_reader"],"principals":[{"id":"` + principalA + `","roles":["document_reader"]},{"id":"` + principalA + `","roles":["document_reader"]}]}`,
		},
		{
			name: "duplicate principal role",
			body: `{"roles":["document_reader"],"principals":[{"id":"` + principalA + `","roles":["document_reader","document_reader"]}]}`,
		},
		{
			name: "space in principal id",
			body: `{"roles":["document_reader"],"principals":[{"id":"spiffe://scrap/cell/cell-a/member/member-a/member 1","roles":["document_reader"]}]}`,
		},
		{
			name: "extra document",
			body: `{"roles":["document_reader"],"principals":[{"id":"` + principalA + `","roles":["document_reader"]}]} {}`,
		},
		{
			name: "unknown field",
			body: `{"roles":["document_reader"],"principals":[{"id":"` + principalA + `","roles":["document_reader"]}],"debug":true}`,
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

func TestLoadRolePolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roles.json")
	if err := os.WriteFile(path, []byte(`{
		"roles": ["admin_reader"],
		"principals": [
			{"id": "spiffe://scrap/cell/cell-a/member/member-a/member-1", "roles": ["admin_reader"]}
		]
	}`), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	policy, err := security.LoadRolePolicy(path)
	if err != nil {
		t.Fatalf("LoadRolePolicy: %v", err)
	}
	authz := security.NewAuthorizer(policy)
	ctx, err := authz.ContextWithPrincipalID(context.Background(), principalA)
	if err != nil {
		t.Fatalf("ContextWithPrincipalID: %v", err)
	}
	if err := authz.Authorize(ctx, security.RoleAdminReader); err != nil {
		t.Fatalf("Authorize(admin reader): %v", err)
	}

	_, err = security.LoadRolePolicy("")
	if security.ErrorClass(err) != security.ClassRolePolicy {
		t.Fatalf("LoadRolePolicy(empty) class = %q, want role_policy; err=%v", security.ErrorClass(err), err)
	}
	_, err = security.LoadRolePolicy(filepath.Join(t.TempDir(), "missing.json"))
	if security.ErrorClass(err) != security.ClassRolePolicy {
		t.Fatalf("LoadRolePolicy(missing) class = %q, want role_policy; err=%v", security.ErrorClass(err), err)
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

func TestAuthorizationContextUsesDefensiveRoleCopies(t *testing.T) {
	authz := security.NewStaticAuthorizer()
	roles := security.NewRoleSet(security.RoleAdminReader)
	ctx := security.ContextWithPrincipal(context.Background(), security.Principal{
		ID:    "admin",
		Roles: roles,
	})

	roles[security.RoleAdminOperator] = struct{}{}
	if err := authz.Authorize(ctx, security.RoleAdminOperator); !errors.Is(err, security.ErrPermissionDenied) {
		t.Fatalf("Authorize after mutating input roles = %v, want permission denied", err)
	}

	principal, ok := security.PrincipalFromContext(ctx)
	if !ok {
		t.Fatal("principal missing from context")
	}
	principal.Roles[security.RoleAdminBreakGlass] = struct{}{}
	if err := authz.Authorize(ctx, security.RoleAdminBreakGlass); !errors.Is(err, security.ErrPermissionDenied) {
		t.Fatalf("Authorize after mutating returned roles = %v, want permission denied", err)
	}
}

func TestAuthorizeHTTPRequestResolvesTLSPrincipal(t *testing.T) {
	policy, err := security.ParseRolePolicy([]byte(`{
		"roles": ["admin_reader"],
		"principals": [
			{"id": "spiffe://scrap/cell/cell-a/member/member-a/member-1", "roles": ["admin_reader"]}
		]
	}`))
	if err != nil {
		t.Fatalf("ParseRolePolicy: %v", err)
	}
	authz := security.NewAuthorizer(policy)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil)
	req.TLS = verifiedTLSState(t)

	if err := authz.AuthorizeHTTPRequest(req, security.RoleAdminReader); err != nil {
		t.Fatalf("AuthorizeHTTPRequest(reader): %v", err)
	}
	err = authz.AuthorizeHTTPRequest(req, security.RoleAdminOperator)
	if !errors.Is(err, security.ErrPermissionDenied) {
		t.Fatalf("AuthorizeHTTPRequest(operator) = %v, want permission denied", err)
	}
	if got := security.HTTPStatusForAuthorization(err); got != http.StatusForbidden {
		t.Fatalf("HTTPStatusForAuthorization(permission denied) = %d, want 403", got)
	}

	req = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil)
	err = authz.AuthorizeHTTPRequest(req, security.RoleAdminReader)
	if !errors.Is(err, security.ErrUnauthenticated) {
		t.Fatalf("AuthorizeHTTPRequest without TLS = %v, want unauthenticated", err)
	}
}

func TestContextWithTLSPrincipalTriesAllPolicyMappedURIs(t *testing.T) {
	policy, err := security.ParseRolePolicy([]byte(`{
		"roles": ["admin_reader"],
		"principals": [
			{"id": "spiffe://scrap/cell/cell-a/member/member-a/member-1", "roles": ["admin_reader"]}
		]
	}`))
	if err != nil {
		t.Fatalf("ParseRolePolicy: %v", err)
	}
	auxURI, err := url.Parse("spiffe://scrap/auxiliary")
	if err != nil {
		t.Fatalf("parse auxiliary URI: %v", err)
	}
	roleURI, err := url.Parse(principalA)
	if err != nil {
		t.Fatalf("parse role URI: %v", err)
	}
	state := *verifiedTLSState(t)
	cert := *state.VerifiedChains[0][0]
	cert.URIs = []*url.URL{auxURI, roleURI}
	state.VerifiedChains = [][]*x509.Certificate{{&cert}}

	authz := security.NewAuthorizer(policy)
	ctx, err := authz.ContextWithTLSPrincipal(context.Background(), state)
	if err != nil {
		t.Fatalf("ContextWithTLSPrincipal: %v", err)
	}
	if err := authz.Authorize(ctx, security.RoleAdminReader); err != nil {
		t.Fatalf("Authorize(admin reader): %v", err)
	}
}

func TestContextWithTLSPrincipalRejectsAmbiguousMappedSANs(t *testing.T) {
	const principalB = "spiffe://scrap/cell/cell-b/member/member-b/member-2"
	policy, err := security.ParseRolePolicy([]byte(`{
		"roles": ["admin_reader"],
		"principals": [
			{"id": "spiffe://scrap/cell/cell-a/member/member-a/member-1", "roles": ["admin_reader"]},
			{"id": "spiffe://scrap/cell/cell-b/member/member-b/member-2", "roles": ["admin_reader"]}
		]
	}`))
	if err != nil {
		t.Fatalf("ParseRolePolicy: %v", err)
	}
	uriA, err := url.Parse(principalA)
	if err != nil {
		t.Fatalf("parse principalA URI: %v", err)
	}
	uriB, err := url.Parse(principalB)
	if err != nil {
		t.Fatalf("parse principalB URI: %v", err)
	}
	state := *verifiedTLSState(t)
	cert := *state.VerifiedChains[0][0]
	cert.URIs = []*url.URL{uriA, uriB}
	state.VerifiedChains = [][]*x509.Certificate{{&cert}}

	authz := security.NewAuthorizer(policy)
	_, err = authz.ContextWithTLSPrincipal(context.Background(), state)
	if !errors.Is(err, security.ErrPermissionDenied) {
		t.Fatalf("ContextWithTLSPrincipal with two policy-mapped SANs = %v, want permission denied", err)
	}
}

func TestNilAuthorizerAllowsBoundaryCompatibility(t *testing.T) {
	var authz *security.Authorizer
	ctx, err := authz.ContextWithPrincipalID(context.Background(), principalA)
	if err != nil {
		t.Fatalf("nil ContextWithPrincipalID: %v", err)
	}
	if ctx == nil {
		t.Fatal("nil ContextWithPrincipalID returned nil context")
	}
	if err := authz.Authorize(ctx, security.RoleAdminReader); err != nil {
		t.Fatalf("nil Authorize: %v", err)
	}
}

func TestAuthorizationErrorsMapToTransportCodes(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantGRPC   codes.Code
		wantStatus int
	}{
		{
			name:       "unauthenticated",
			err:        security.UnauthenticatedError("authentication required"),
			wantGRPC:   codes.Unauthenticated,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "permission denied",
			err:        security.PermissionDeniedError("permission denied"),
			wantGRPC:   codes.PermissionDenied,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "unknown",
			err:        errors.New("boom"),
			wantGRPC:   codes.Unknown,
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := status.Code(tt.err); got != tt.wantGRPC {
				t.Fatalf("status.Code = %v, want %v", got, tt.wantGRPC)
			}
			if got := security.HTTPStatusForAuthorization(tt.err); got != tt.wantStatus {
				t.Fatalf("HTTPStatusForAuthorization = %d, want %d", got, tt.wantStatus)
			}
		})
	}
}

func TestPrincipalIDFromTLSStateRequiresVerifiedURI(t *testing.T) {
	state := verifiedTLSState(t)
	got, err := security.PrincipalIDFromTLSState(*state)
	if err != nil {
		t.Fatalf("PrincipalIDFromTLSState: %v", err)
	}
	if got != principalA {
		t.Fatalf("PrincipalIDFromTLSState = %q, want %q", got, principalA)
	}

	_, err = security.PrincipalIDFromTLSState(tls.ConnectionState{})
	if !errors.Is(err, security.ErrUnauthenticated) {
		t.Fatalf("PrincipalIDFromTLSState without verified cert = %v, want unauthenticated", err)
	}
}

func verifiedTLSState(t *testing.T) *tls.ConnectionState {
	t.Helper()
	bundle := securityfixture.WriteCertBundle(t, t.TempDir(), securityfixture.CertOptions{
		ClientURI: principalA,
	})
	return &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{bundle.ClientCertificate},
		VerifiedChains:   [][]*x509.Certificate{{bundle.ClientCertificate}},
	}
}
