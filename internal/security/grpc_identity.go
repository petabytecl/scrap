package security

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	grpcpeer "google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func PeerIdentityUnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		identityCtx, err := contextWithGRPCPeerIdentity(ctx)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "verified peer identity is required")
		}
		return handler(identityCtx, req)
	}
}

func PeerIdentityStreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		identityCtx, err := contextWithGRPCPeerIdentity(stream.Context())
		if err != nil {
			return status.Error(codes.Unauthenticated, "verified peer identity is required")
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
