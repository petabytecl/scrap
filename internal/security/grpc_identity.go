package security

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	grpcpeer "google.golang.org/grpc/peer"
)

func PeerIdentityUnaryServerInterceptor(opts ...PrincipalInterceptorOption) grpc.UnaryServerInterceptor {
	cfg := newPrincipalInterceptorConfig(opts...)
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		identityCtx, err := contextWithGRPCPeerIdentity(ctx)
		if err != nil {
			principalID := principalIDFromGRPCContext(ctx)
			return nil, cfg.handlePrincipalError(ctx, info.FullMethod, principalID, UnauthenticatedError("verified peer identity is required"))
		}
		return handler(identityCtx, req)
	}
}

func PeerIdentityStreamServerInterceptor(opts ...PrincipalInterceptorOption) grpc.StreamServerInterceptor {
	cfg := newPrincipalInterceptorConfig(opts...)
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		identityCtx, err := contextWithGRPCPeerIdentity(stream.Context())
		if err != nil {
			principalID := principalIDFromGRPCContext(stream.Context())
			return cfg.handlePrincipalError(stream.Context(), info.FullMethod, principalID, UnauthenticatedError("verified peer identity is required"))
		}
		return handler(srv, contextServerStream{ServerStream: stream, ctx: identityCtx})
	}
}

func contextWithGRPCPeerIdentity(ctx context.Context) (context.Context, error) {
	p, ok := grpcpeer.FromContext(ctx)
	if !ok {
		return nil, errMissingCertificate
	}
	var tlsInfo credentials.TLSInfo
	switch authInfo := p.AuthInfo.(type) {
	case credentials.TLSInfo:
		tlsInfo = authInfo
	case *credentials.TLSInfo:
		tlsInfo = *authInfo
	default:
		return nil, errMissingCertificate
	}
	identity, err := ExtractPeerIdentity(tlsInfo.State)
	if err != nil {
		return nil, err
	}
	return ContextWithPeerIdentity(ctx, identity), nil
}

type contextServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s contextServerStream) Context() context.Context {
	return s.ctx
}
