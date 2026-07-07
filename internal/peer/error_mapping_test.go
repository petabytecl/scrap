package peer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

// Deferred peer-transport hardening: consumer failures previously collapsed
// into codes.Internal regardless of cause, and os error strings (with local
// Block paths) crossed the peer surface verbatim.

func TestMapReplicateConsumeErrorPreservesTaxonomy(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want codes.Code
	}{
		{name: "canceled", err: fmt.Errorf("sink: %w", context.Canceled), want: codes.Canceled},
		{name: "deadline", err: fmt.Errorf("sink: %w", context.DeadlineExceeded), want: codes.DeadlineExceeded},
		{name: "invalid argument", err: fmt.Errorf("%w: bad name", storeapi.ErrInvalidArgument), want: codes.InvalidArgument},
		{name: "already exists", err: fmt.Errorf("%w: duplicate", storeapi.ErrAlreadyExists), want: codes.AlreadyExists},
		{name: "resource exhausted", err: storeapi.NewResourceExhausted(storeapi.ResourceExhaustedReasonDocumentTooLarge, "too large"), want: codes.ResourceExhausted},
		{name: "enospc", err: &os.PathError{Op: "write", Path: "/data/shards/1/blocks/x.blk", Err: syscall.ENOSPC}, want: codes.ResourceExhausted},
		{name: "data loss", err: fmt.Errorf("%w: sha mismatch", storeapi.ErrDataLoss), want: codes.DataLoss},
		{name: "unavailable", err: storeapi.NewUnavailable(storeapi.UnavailableReasonCryptoUnavailable, "no key"), want: codes.Unavailable},
		{name: "unknown is internal", err: errors.New("boom"), want: codes.Internal},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := mapReplicateConsumeError("append", tt.err)
			if code := status.Code(got); code != tt.want {
				t.Fatalf("code = %v, want %v (err: %v)", code, tt.want, got)
			}
		})
	}
}

func TestMapReplicateConsumeErrorStripsFilesystemPaths(t *testing.T) {
	const leakedPath = "/data/shards/0000000000000007/blocks/000000000000002a.blk"
	err := fmt.Errorf("block append: %w", &os.PathError{Op: "write", Path: leakedPath, Err: syscall.EIO})

	got := mapReplicateConsumeError("append", err)

	if code := status.Code(got); code != codes.Internal {
		t.Fatalf("code = %v, want Internal", code)
	}
	msg := status.Convert(got).Message()
	if strings.Contains(msg, leakedPath) {
		t.Fatalf("peer status message leaks the Block path: %q", msg)
	}
	if !strings.Contains(msg, syscall.EIO.Error()) {
		t.Fatalf("peer status message lost the errno class: %q", msg)
	}
}

func TestMapReplicateReceiveErrorClassifiesCallerFailures(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want codes.Code
	}{
		{name: "context canceled", err: context.Canceled, want: codes.Canceled},
		{name: "status canceled", err: status.Error(codes.Canceled, "client hung up"), want: codes.Canceled},
		{name: "context deadline", err: context.DeadlineExceeded, want: codes.DeadlineExceeded},
		{name: "status deadline", err: status.Error(codes.DeadlineExceeded, "deadline"), want: codes.DeadlineExceeded},
		{name: "transport fault stays internal", err: errors.New("connection reset"), want: codes.Internal},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := mapReplicateReceiveError(tt.err)
			if code := status.Code(got); code != tt.want {
				t.Fatalf("code = %v, want %v", code, tt.want)
			}
		})
	}
}

// The mapper must be wired into the consumer path, not just exist: a sink
// failure carrying the store taxonomy has to reach the peer surface with its
// code intact.
func TestConsumeReplicatedDocumentMapsSinkErrors(t *testing.T) {
	srv := &Server{replicationSink: failingSink{err: fmt.Errorf("%w: quota", storeapi.NewResourceExhausted(storeapi.ResourceExhaustedReasonDocumentTooLarge, "quota"))}}

	_, err := srv.consumeReplicatedDocument(context.Background(), nil, strings.NewReader(""))
	if code := status.Code(err); code != codes.ResourceExhausted {
		t.Fatalf("code = %v, want ResourceExhausted", code)
	}
}

type failingSink struct {
	err error
}

func (s failingSink) AppendReplicatedDocument(context.Context, *scrapv1.ReplicateDocumentInit, io.Reader) ([]byte, error) {
	return nil, s.err
}
