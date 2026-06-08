package security

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	grpcpeer "google.golang.org/grpc/peer"
)

// PrincipalUnaryServerInterceptor attaches a role-resolved principal to unary
// gRPC requests.
func PrincipalUnaryServerInterceptor(authorizer *Authorizer) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if isGRPCHealthMethod(info.FullMethod) {
			return handler(ctx, req)
		}
		principalCtx, err := contextWithGRPCPrincipal(ctx, authorizer)
		if err != nil {
			return nil, err
		}
		return handler(principalCtx, req)
	}
}

// PrincipalStreamServerInterceptor attaches a role-resolved principal to stream
// gRPC requests.
func PrincipalStreamServerInterceptor(authorizer *Authorizer) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if isGRPCHealthMethod(info.FullMethod) {
			return handler(srv, stream)
		}
		principalCtx, err := contextWithGRPCPrincipal(stream.Context(), authorizer)
		if err != nil {
			return err
		}
		return handler(srv, contextServerStream{ServerStream: stream, ctx: principalCtx})
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
