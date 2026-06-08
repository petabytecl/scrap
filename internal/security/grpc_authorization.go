package security

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	grpcpeer "google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/petabytecl/scrap/internal/audit"
)

// GRPCAuditInfo classifies a bounded security audit decision for a gRPC method.
type GRPCAuditInfo struct {
	Role      Role
	Operation string
	Target    string
}

type GRPCAuditClassifier func(fullMethod string) (GRPCAuditInfo, bool)

type PrincipalInterceptorOption func(*principalInterceptorConfig)

type principalInterceptorConfig struct {
	auditSink   audit.Sink
	rateLimiter *RateLimiter
	surface     RateLimitSurface
	classify    GRPCAuditClassifier
}

// WithPrincipalAudit configures the principal interceptor to audit and
// rate-limit denials that happen before RPC handlers run.
func WithPrincipalAudit(sink audit.Sink, limiter *RateLimiter, surface RateLimitSurface, classify GRPCAuditClassifier) PrincipalInterceptorOption {
	return func(cfg *principalInterceptorConfig) {
		cfg.auditSink = sink
		cfg.rateLimiter = limiter
		cfg.surface = surface
		cfg.classify = classify
	}
}

// PrincipalUnaryServerInterceptor attaches a role-resolved principal to unary
// gRPC requests.
func PrincipalUnaryServerInterceptor(authorizer *Authorizer, opts ...PrincipalInterceptorOption) grpc.UnaryServerInterceptor {
	cfg := newPrincipalInterceptorConfig(opts...)
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if isGRPCHealthMethod(info.FullMethod) {
			return handler(ctx, req)
		}
		principalID := principalIDFromGRPCContext(ctx)
		principalCtx, err := contextWithGRPCPrincipal(ctx, authorizer)
		if err != nil {
			return nil, cfg.handlePrincipalError(ctx, info.FullMethod, principalID, err)
		}
		return handler(principalCtx, req)
	}
}

// PrincipalStreamServerInterceptor attaches a role-resolved principal to stream
// gRPC requests.
func PrincipalStreamServerInterceptor(authorizer *Authorizer, opts ...PrincipalInterceptorOption) grpc.StreamServerInterceptor {
	cfg := newPrincipalInterceptorConfig(opts...)
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if isGRPCHealthMethod(info.FullMethod) {
			return handler(srv, stream)
		}
		principalID := principalIDFromGRPCContext(stream.Context())
		principalCtx, err := contextWithGRPCPrincipal(stream.Context(), authorizer)
		if err != nil {
			return cfg.handlePrincipalError(stream.Context(), info.FullMethod, principalID, err)
		}
		return handler(srv, contextServerStream{ServerStream: stream, ctx: principalCtx})
	}
}

func newPrincipalInterceptorConfig(opts ...PrincipalInterceptorOption) principalInterceptorConfig {
	cfg := principalInterceptorConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return cfg
}

func (cfg principalInterceptorConfig) handlePrincipalError(ctx context.Context, fullMethod, principalID string, principalErr error) error {
	info, ok := cfg.auditInfo(fullMethod)
	if !ok {
		return principalErr
	}
	if cfg.rateLimiter != nil {
		decision := cfg.rateLimiter.Allow(ctx, cfg.surface, principalID, info.Operation)
		if decision.Limited {
			if err := cfg.recordAudit(ctx, principalID, info, audit.ResultRateLimited, audit.ReasonRateLimited); err != nil {
				return status.Error(codes.Internal, "audit event failed")
			}
			return RateLimitedError()
		}
	}
	if err := cfg.recordAudit(ctx, principalID, info, audit.ResultDenied, auditReasonForAuthorizationError(principalErr)); err != nil {
		return status.Error(codes.Internal, "audit event failed")
	}
	return principalErr
}

func (cfg principalInterceptorConfig) auditInfo(fullMethod string) (GRPCAuditInfo, bool) {
	if cfg.classify == nil || cfg.auditSink == nil || !validRateLimitSurface(cfg.surface) {
		return GRPCAuditInfo{}, false
	}
	info, ok := cfg.classify(fullMethod)
	if !ok {
		return GRPCAuditInfo{}, false
	}
	if info.Role == "" {
		info.Role = RoleUnknown
	}
	return info, true
}

func (cfg principalInterceptorConfig) recordAudit(ctx context.Context, principalID string, info GRPCAuditInfo, result, reason string) error {
	event, err := audit.NewEvent(audit.EventInput{
		PrincipalID: principalID,
		Role:        string(info.Role),
		Surface:     string(cfg.surface),
		Operation:   info.Operation,
		Target:      info.Target,
		Result:      result,
		Reason:      reason,
	})
	if err != nil {
		return err
	}
	return cfg.auditSink.Record(ctx, event)
}

func principalIDFromGRPCContext(ctx context.Context) string {
	p, ok := grpcpeer.FromContext(ctx)
	if !ok {
		return ""
	}
	switch authInfo := p.AuthInfo.(type) {
	case credentials.TLSInfo:
		id, err := PrincipalIDFromTLSState(authInfo.State)
		if err == nil {
			return id
		}
	case *credentials.TLSInfo:
		id, err := PrincipalIDFromTLSState(authInfo.State)
		if err == nil {
			return id
		}
	}
	return ""
}

func auditReasonForAuthorizationError(err error) string {
	switch {
	case errors.Is(err, ErrUnauthenticated):
		return audit.ReasonUnauthenticated
	case errors.Is(err, ErrPermissionDenied):
		if AuthorizationStatusForError(err) == AuthorizationStatusMissingRole {
			return audit.ReasonMissingRole
		}
		return audit.ReasonPermissionDenied
	default:
		return audit.ReasonInternalError
	}
}

func contextWithGRPCPrincipal(ctx context.Context, authorizer *Authorizer) (context.Context, error) {
	if authorizer == nil {
		return ctx, nil
	}
	p, ok := grpcpeer.FromContext(ctx)
	if !ok {
		return nil, UnauthenticatedError("authentication required")
	}
	var tlsInfo credentials.TLSInfo
	switch authInfo := p.AuthInfo.(type) {
	case credentials.TLSInfo:
		tlsInfo = authInfo
	case *credentials.TLSInfo:
		tlsInfo = *authInfo
	default:
		return nil, UnauthenticatedError("authentication required")
	}
	return authorizer.ContextWithTLSPrincipal(ctx, tlsInfo.State)
}

func isGRPCHealthMethod(fullMethod string) bool {
	switch fullMethod {
	case "/grpc.health.v1.Health/Check", "/grpc.health.v1.Health/Watch":
		return true
	default:
		return false
	}
}
