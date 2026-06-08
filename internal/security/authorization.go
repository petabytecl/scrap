package security

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"sync/atomic"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	ErrUnauthenticated  = errors.New("authentication required")
	ErrPermissionDenied = errors.New("permission denied")
)

type principalContextKey struct{}

const (
	AuthorizationStatusConfigured  = "configured"
	AuthorizationStatusDenied      = "denied"
	AuthorizationStatusMismatch    = "mismatch"
	AuthorizationStatusMissingRole = "missing_role"
)

// Principal is an authenticated caller and its resolved roles.
type Principal struct {
	ID    string
	Roles RoleSet
}

// Authorizer evaluates role requirements against authenticated principals.
type Authorizer struct {
	policy *RolePolicy
	status atomic.Value
}

// NewAuthorizer creates an authorizer backed by policy.
func NewAuthorizer(policy *RolePolicy) *Authorizer {
	authorizer := &Authorizer{policy: policy}
	authorizer.status.Store(AuthorizationStatusConfigured)
	return authorizer
}

// NewStaticAuthorizer creates an authorizer for tests or pre-resolved contexts.
func NewStaticAuthorizer() *Authorizer {
	authorizer := &Authorizer{}
	authorizer.status.Store(AuthorizationStatusConfigured)
	return authorizer
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
		a.RecordAuthorizationStatus(AuthorizationStatusDenied)
		return nil, newAuthorizationError(ErrUnauthenticated, "authentication required")
	}
	if a.policy == nil {
		return ContextWithPrincipal(ctx, Principal{ID: cleanID}), nil
	}
	roles, ok := a.policy.rolesForPrincipal(cleanID)
	if !ok {
		a.RecordAuthorizationStatus(AuthorizationStatusDenied)
		return nil, newAuthorizationError(ErrPermissionDenied, "permission denied")
	}
	return ContextWithPrincipal(ctx, Principal{ID: cleanID, Roles: roles}), nil
}

// ContextWithTLSPrincipal resolves the verified certificate URI principal and
// attaches its roles to ctx.
func (a *Authorizer) ContextWithTLSPrincipal(ctx context.Context, state tls.ConnectionState) (context.Context, error) {
	cert, err := verifiedPeerCertificate(state)
	if err != nil {
		a.RecordAuthorizationStatus(AuthorizationStatusDenied)
		return nil, newAuthorizationError(ErrUnauthenticated, "authentication required")
	}
	if a != nil && a.policy != nil {
		return a.contextWithPolicyTLSPrincipal(ctx, cert)
	}
	id, err := firstPrincipalIDFromCertificate(cert)
	if err != nil {
		a.RecordAuthorizationStatus(AuthorizationStatusDenied)
		return nil, err
	}
	return a.ContextWithPrincipalID(ctx, id)
}

func (a *Authorizer) contextWithPolicyTLSPrincipal(ctx context.Context, cert *x509.Certificate) (context.Context, error) {
	hasUsableURI := false
	for _, uri := range cert.URIs {
		if uri == nil {
			continue
		}
		id, ok := cleanPrincipalID(uri.String())
		if !ok {
			continue
		}
		hasUsableURI = true
		roles, ok := a.policy.rolesForPrincipal(id)
		if ok {
			return ContextWithPrincipal(ctx, Principal{ID: id, Roles: roles}), nil
		}
	}
	if hasUsableURI {
		a.RecordAuthorizationStatus(AuthorizationStatusDenied)
		return nil, newAuthorizationError(ErrPermissionDenied, "permission denied")
	}
	a.RecordAuthorizationStatus(AuthorizationStatusDenied)
	return nil, newAuthorizationError(ErrUnauthenticated, "authentication required")
}

// Authorize requires role on the principal attached to ctx.
func (a *Authorizer) Authorize(ctx context.Context, role Role) error {
	if a == nil {
		return nil
	}
	principal, ok := PrincipalFromContext(ctx)
	if !ok {
		a.RecordAuthorizationStatus(AuthorizationStatusDenied)
		return newAuthorizationError(ErrUnauthenticated, "authentication required")
	}
	if !principal.Roles.has(role) {
		a.RecordAuthorizationStatus(AuthorizationStatusMissingRole)
		return newAuthorizationErrorWithStatus(ErrPermissionDenied, "permission denied", AuthorizationStatusMissingRole)
	}
	return nil
}

// AuthorizationStatus returns the last bounded authorization state.
func (a *Authorizer) AuthorizationStatus() string {
	if a == nil {
		return ""
	}
	status, ok := a.status.Load().(string)
	if !ok || status == "" {
		return AuthorizationStatusConfigured
	}
	return status
}

// RecordAuthorizationStatus records a bounded authorization state for health
// and evidence surfaces.
func (a *Authorizer) RecordAuthorizationStatus(status string) {
	if a == nil || !validAuthorizationStatus(status) {
		return
	}
	a.status.Store(status)
}

func validAuthorizationStatus(status string) bool {
	switch status {
	case AuthorizationStatusConfigured, AuthorizationStatusDenied, AuthorizationStatusMismatch, AuthorizationStatusMissingRole:
		return true
	default:
		return false
	}
}

// PrincipalIDFromTLSState extracts a bounded URI SAN principal from a verified
// client certificate.
func PrincipalIDFromTLSState(state tls.ConnectionState) (string, error) {
	cert, err := verifiedPeerCertificate(state)
	if err != nil {
		return "", newAuthorizationError(ErrUnauthenticated, "authentication required")
	}
	return firstPrincipalIDFromCertificate(cert)
}

func firstPrincipalIDFromCertificate(cert *x509.Certificate) (string, error) {
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

// PermissionDeniedErrorWithStatus returns a permission-denied error with the
// request-local bounded authorization status attached.
func PermissionDeniedErrorWithStatus(message, authzStatus string) error {
	return newAuthorizationErrorWithStatus(ErrPermissionDenied, message, authzStatus)
}

// UnauthenticatedError returns a bounded unauthenticated authorization error.
func UnauthenticatedError(message string) error {
	return newAuthorizationError(ErrUnauthenticated, message)
}

type AuthorizationError struct {
	cause   error
	message string
	status  string
}

func newAuthorizationError(cause error, message string) error {
	return newAuthorizationErrorWithStatus(cause, message, authorizationStatusForCause(cause))
}

func newAuthorizationErrorWithStatus(cause error, message, authzStatus string) error {
	if message == "" {
		message = cause.Error()
	}
	if !validAuthorizationStatus(authzStatus) {
		authzStatus = authorizationStatusForCause(cause)
	}
	return &AuthorizationError{cause: cause, message: message, status: authzStatus}
}

func authorizationStatusForCause(cause error) string {
	switch {
	case errors.Is(cause, ErrUnauthenticated), errors.Is(cause, ErrPermissionDenied):
		return AuthorizationStatusDenied
	default:
		return ""
	}
}

// AuthorizationStatusForError returns the request-local bounded authorization
// status attached to err, when the error was produced by this package.
func AuthorizationStatusForError(err error) string {
	var authErr *AuthorizationError
	if errors.As(err, &authErr) && validAuthorizationStatus(authErr.status) {
		return authErr.status
	}
	return authorizationStatusForCause(err)
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
	case errors.Is(e.cause, ErrRateLimited):
		return status.New(codes.ResourceExhausted, e.message)
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
	case errors.Is(err, ErrRateLimited):
		return http.StatusTooManyRequests
	default:
		return http.StatusInternalServerError
	}
}
