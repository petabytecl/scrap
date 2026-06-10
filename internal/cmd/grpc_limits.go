package cmd

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

const (
	grpcMessageOverheadBytes      = 64 << 10
	publicGRPCMaxConnectionIdle   = 5 * time.Minute
	publicGRPCMaxConnectionAge    = 30 * time.Minute
	publicGRPCMaxConnectionGrace  = time.Minute
	publicGRPCKeepaliveTime       = 2 * time.Minute
	publicGRPCKeepaliveTimeout    = 20 * time.Second
	publicGRPCKeepaliveMinPingGap = 30 * time.Second
)

func publicGRPCTransportOptions() []grpc.ServerOption {
	maxMessageBytes := storeapi.MaxClientChunkBytes + grpcMessageOverheadBytes
	return []grpc.ServerOption{
		grpc.MaxRecvMsgSize(maxMessageBytes),
		grpc.MaxSendMsgSize(maxMessageBytes),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle:     publicGRPCMaxConnectionIdle,
			MaxConnectionAge:      publicGRPCMaxConnectionAge,
			MaxConnectionAgeGrace: publicGRPCMaxConnectionGrace,
			Time:                  publicGRPCKeepaliveTime,
			Timeout:               publicGRPCKeepaliveTimeout,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             publicGRPCKeepaliveMinPingGap,
			PermitWithoutStream: false,
		}),
	}
}

type publicRPCLimiter struct {
	writes chan struct{}
	reads  chan struct{}
}

func newPublicRPCLimiter() *publicRPCLimiter {
	return &publicRPCLimiter{
		writes: make(chan struct{}, storeapi.MaxConcurrentWrites),
		reads:  make(chan struct{}, storeapi.MaxConcurrentReads),
	}
}

func (l *publicRPCLimiter) unary(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	release, err := l.acquire(info.FullMethod)
	if err != nil {
		return nil, err
	}
	defer release()
	return handler(ctx, req)
}

func (l *publicRPCLimiter) stream(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	release, err := l.acquire(info.FullMethod)
	if err != nil {
		return err
	}
	defer release()
	return handler(srv, stream)
}

func (l *publicRPCLimiter) acquire(fullMethod string) (func(), error) {
	switch fullMethod {
	case scrapv1.DocumentService_WriteDocument_FullMethodName:
		return acquireRPCLimit(l.writes, storeapi.ResourceExhaustedReasonConcurrentWrites, "too many concurrent write requests")
	case scrapv1.DocumentService_HeadDocument_FullMethodName,
		scrapv1.DocumentService_ReadDocument_FullMethodName,
		scrapv1.DocumentService_FindDocuments_FullMethodName:
		return acquireRPCLimit(l.reads, storeapi.ResourceExhaustedReasonConcurrentReads, "too many concurrent read requests")
	default:
		return func() {}, nil
	}
}

func acquireRPCLimit(ch chan struct{}, reason, message string) (func(), error) {
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	default:
		return nil, resourceExhaustedGRPCStatus(reason, message)
	}
}

func resourceExhaustedGRPCStatus(reason, message string) error {
	st, err := status.New(codes.ResourceExhausted, message).WithDetails(&errdetails.ErrorInfo{Reason: reason})
	if err != nil {
		return status.Error(codes.ResourceExhausted, message)
	}
	return st.Err()
}

func publicGRPCLimitDescription() string {
	return fmt.Sprintf(
		"max_recv=%d max_send=%d max_writes=%d max_reads=%d",
		storeapi.MaxClientChunkBytes+grpcMessageOverheadBytes,
		storeapi.MaxClientChunkBytes+grpcMessageOverheadBytes,
		storeapi.MaxConcurrentWrites,
		storeapi.MaxConcurrentReads,
	)
}
