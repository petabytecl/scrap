package security

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	ErrUnauthenticated  = errors.New("authentication required")
	ErrPermissionDenied = errors.New("permission denied")
)

type principalContextKey struct{}

// Principal is an authenticated caller and its resolved roles.
type Principal struct {
	ID    string
	Roles RoleSet
}

// Authorizer evaluates role requirements against authenticated principals.
type Authorizer struct {
	policy *RolePolicy
}

// NewAuthorizer creates an authorizer backed by policy.
func NewAuthorizer(policy *RolePolicy) *Authorizer {
	return &Authorizer{policy: policy}
}

// NewStaticAuthorizer creates an authorizer for tests or pre-resolved contexts.
func NewStaticAuthorizer() *Authorizer {
	return &Authorizer{}
}

// ContextWithPrincipal attaches a pre-resolved principal to ctx.
func ContextWithPrincipal(ctx context.Context, principal Principal) context.Context {
	principal.Roles = principal.Roles.clone()
	return context.WithValue(ctx, principalContextKey{}, principal)
}

// PrincipalFromContext returns the authenticated principal attached to ctx.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	if !ok {
		return Principal{}, false
	}
	principal.Roles = principal.Roles.clone()
	return principal, true
}

// ContextWithPrincipalID resolves id through the authorizer policy and attaches
// the resolved principal to ctx.
func (a *Authorizer) ContextWithPrincipalID(ctx context.Context, id string) (context.Context, error) {
	if a == nil {
		return ctx, nil
	}
	cleanID, ok := cleanPrincipalID(id)
	if !ok {
		return nil, newAuthorizationError(ErrUnauthenticated, "authentication required")
	}
	if a.policy == nil {
		return ContextWithPrincipal(ctx, Principal{ID: cleanID}), nil
	}
	roles, ok := a.policy.rolesForPrincipal(cleanID)
	if !ok {
		return nil, newAuthorizationError(ErrPermissionDenied, "permission denied")
	}
	return ContextWithPrincipal(ctx, Principal{ID: cleanID, Roles: roles}), nil
}

// ContextWithTLSPrincipal resolves the verified certificate URI principal and
// attaches its roles to ctx.
func (a *Authorizer) ContextWithTLSPrincipal(ctx context.Context, state tls.ConnectionState) (context.Context, error) {
	id, err := PrincipalIDFromTLSState(state)
	if err != nil {
		return nil, err
	}
	return a.ContextWithPrincipalID(ctx, id)
}

// Authorize requires role on the principal attached to ctx.
func (a *Authorizer) Authorize(ctx context.Context, role Role) error {
	if a == nil {
		return nil
	}
	principal, ok := PrincipalFromContext(ctx)
	if !ok {
		return newAuthorizationError(ErrUnauthenticated, "authentication required")
	}
	if !principal.Roles.has(role) {
		return newAuthorizationError(ErrPermissionDenied, "permission denied")
	}
	return nil
}

// PrincipalIDFromTLSState extracts a bounded URI SAN principal from a verified
// client certificate.
func PrincipalIDFromTLSState(state tls.ConnectionState) (string, error) {
	cert, err := verifiedPeerCertificate(state)
	if err != nil {
		return "", newAuthorizationError(ErrUnauthenticated, "authentication required")
	}
	for _, uri := range cert.URIs {
		if uri == nil {
			continue
		}
		if id, ok := cleanPrincipalID(uri.String()); ok {
			return id, nil
		}
	}
	return "", newAuthorizationError(ErrUnauthenticated, "authentication required")
}

// AuthorizeHTTPRequest authorizes an HTTP request, resolving a TLS principal
// from the request when one has not already been attached to the context.
func (a *Authorizer) AuthorizeHTTPRequest(r *http.Request, role Role) error {
	if a == nil {
		return nil
	}
	ctx := r.Context()
	if _, ok := PrincipalFromContext(ctx); !ok && r.TLS != nil {
		var err error
		ctx, err = a.ContextWithTLSPrincipal(ctx, *r.TLS)
		if err != nil {
			return err
		}
	}
	return a.Authorize(ctx, role)
}

// PermissionDeniedError returns a bounded permission-denied authorization error.
func PermissionDeniedError(message string) error {
	return newAuthorizationError(ErrPermissionDenied, message)
}

// UnauthenticatedError returns a bounded unauthenticated authorization error.
func UnauthenticatedError(message string) error {
	return newAuthorizationError(ErrUnauthenticated, message)
}

type AuthorizationError struct {
	cause   error
	message string
}

func newAuthorizationError(cause error, message string) error {
	if message == "" {
		message = cause.Error()
	}
	return &AuthorizationError{cause: cause, message: message}
}

func (e *AuthorizationError) Error() string {
	return e.message
}

func (e *AuthorizationError) Unwrap() error {
	return e.cause
}

func (e *AuthorizationError) GRPCStatus() *status.Status {
	switch {
	case errors.Is(e.cause, ErrUnauthenticated):
		return status.New(codes.Unauthenticated, e.message)
	case errors.Is(e.cause, ErrPermissionDenied):
		return status.New(codes.PermissionDenied, e.message)
	default:
		return status.New(codes.Internal, "authorization failed")
	}
}

// HTTPStatusForAuthorization maps an authorization error to an HTTP status.
func HTTPStatusForAuthorization(err error) int {
	switch {
	case errors.Is(err, ErrUnauthenticated):
		return http.StatusUnauthorized
	case errors.Is(err, ErrPermissionDenied):
		return http.StatusForbidden
	default:
		return http.StatusInternalServerError
	}
}
