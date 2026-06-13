package main

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
)

func TestWriteDocClosesTransientReconnectClient(t *testing.T) {
	originalDial := dialDocumentClient
	t.Cleanup(func() {
		dialDocumentClient = originalDial
	})

	var closeCount atomic.Int64
	dialDocumentClient = func(addr string) (*documentClientLease, error) {
		if addr != "test-addr" {
			t.Fatalf("addr = %q, want test-addr", addr)
		}
		return &documentClientLease{
			client: fakeDocumentClient{},
			closeFn: func() error {
				closeCount.Add(1)
				return nil
			},
		}, nil
	}

	err := writeDoc(
		context.Background(),
		fakeDocumentClient{writeErr: status.Error(codes.Unavailable, "not leader")},
		"test-addr",
		"tx-1",
		"doc.bin",
		[]byte("payload"),
		defaultOpTimeout,
	)
	if err != nil {
		t.Fatalf("writeDoc returned error: %v", err)
	}
	if got := closeCount.Load(); got != 1 {
		t.Fatalf("transient close count = %d, want 1", got)
	}
}

func TestWriteDocUsesLeaderHintBeforeFallbackReconnect(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")

	originalDial := dialDocumentClient
	t.Cleanup(func() {
		dialDocumentClient = originalDial
	})

	var dialed string
	dialDocumentClient = func(addr string) (*documentClientLease, error) {
		dialed = addr
		if addr != "leader:9090" {
			t.Fatalf("addr = %q, want leader:9090", addr)
		}
		return &documentClientLease{
			client:  fakeDocumentClient{},
			closeFn: func() error { return nil },
		}, nil
	}

	err := writeDoc(
		context.Background(),
		fakeDocumentClient{writeErr: unavailableWithLeaderHint(t, "leader:9090")},
		"nodeport:9090",
		"tx-1",
		"doc.bin",
		[]byte("payload"),
		defaultOpTimeout,
	)
	if err != nil {
		t.Fatalf("writeDoc returned error: %v", err)
	}
	if dialed != "leader:9090" {
		t.Fatalf("dialed = %q, want leader:9090", dialed)
	}
}

func TestWriteDocRetriesUnavailableFromSend(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")

	originalDial := dialDocumentClient
	t.Cleanup(func() {
		dialDocumentClient = originalDial
	})

	var dialed string
	dialDocumentClient = func(addr string) (*documentClientLease, error) {
		dialed = addr
		if addr != "leader:9090" {
			t.Fatalf("addr = %q, want leader:9090", addr)
		}
		return &documentClientLease{
			client:  fakeDocumentClient{},
			closeFn: func() error { return nil },
		}, nil
	}

	err := writeDoc(
		context.Background(),
		fakeDocumentClient{sendErr: unavailableWithLeaderHint(t, "leader:9090")},
		"nodeport:9090",
		"tx-1",
		"doc.bin",
		[]byte("payload"),
		defaultOpTimeout,
	)
	if err != nil {
		t.Fatalf("writeDoc returned error: %v", err)
	}
	if dialed != "leader:9090" {
		t.Fatalf("dialed = %q, want leader:9090", dialed)
	}
}

func TestIsUploadPressureRequiresErrorInfoReason(t *testing.T) {
	if isUploadPressure(status.Error(codes.ResourceExhausted, "plain resource exhausted")) {
		t.Fatal("plain ResourceExhausted should not be upload pressure")
	}
	if !isUploadPressure(uploadPressureError(t)) {
		t.Fatal("ResourceExhausted with upload_pressure reason should be upload pressure")
	}
}

func TestHeadAndVerifyUsesOperationTimeout(t *testing.T) {
	doc := &docRef{
		txID:     "tx-1",
		docName:  "doc.bin",
		checksum: "abc",
		size:     3,
	}

	err := headAndVerify(
		context.Background(),
		fakeDocumentClient{
			headFunc: func(ctx context.Context, _ *scrapv1.HeadDocumentRequest) (*scrapv1.HeadDocumentResponse, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		},
		doc,
		10*time.Millisecond,
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("headAndVerify error = %v, want context deadline exceeded", err)
	}
}

func unavailableWithLeaderHint(t *testing.T, leaderAddr string) error {
	t.Helper()
	st, err := status.New(codes.Unavailable, "not shard leader").
		WithDetails(&scrapv1.LeaderHint{LeaderAddr: leaderAddr})
	if err != nil {
		t.Fatalf("build status: %v", err)
	}
	return st.Err()
}

func uploadPressureError(t *testing.T) error {
	t.Helper()
	st, err := status.New(codes.ResourceExhausted, "upload pressure").
		WithDetails(&errdetails.ErrorInfo{Reason: "upload_pressure"})
	if err != nil {
		t.Fatalf("build status: %v", err)
	}
	return st.Err()
}

type fakeDocumentClient struct {
	writeErr error
	sendErr  error
	headFunc func(context.Context, *scrapv1.HeadDocumentRequest) (*scrapv1.HeadDocumentResponse, error)
}

func (c fakeDocumentClient) WriteDocument(ctx context.Context, _ ...grpc.CallOption) (grpc.ClientStreamingClient[scrapv1.WriteDocumentRequest, scrapv1.WriteDocumentResponse], error) {
	return &fakeWriteStream{
		fakeClientStream: fakeClientStream{ctx: ctx},
		sendErr:          c.sendErr,
		closeErr:         c.writeErr,
	}, nil
}

func (c fakeDocumentClient) HeadDocument(ctx context.Context, req *scrapv1.HeadDocumentRequest, _ ...grpc.CallOption) (*scrapv1.HeadDocumentResponse, error) {
	if c.headFunc != nil {
		return c.headFunc(ctx, req)
	}
	return nil, errors.New("not implemented")
}

func (fakeDocumentClient) ReadDocument(context.Context, *scrapv1.ReadDocumentRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[scrapv1.ReadDocumentResponse], error) {
	return nil, errors.New("not implemented")
}

func (fakeDocumentClient) FindDocuments(context.Context, *scrapv1.FindDocumentsRequest, ...grpc.CallOption) (*scrapv1.FindDocumentsResponse, error) {
	return nil, errors.New("not implemented")
}

type fakeWriteStream struct {
	fakeClientStream
	sendErr  error
	closeErr error
}

func (s *fakeWriteStream) Send(*scrapv1.WriteDocumentRequest) error {
	if s.sendErr != nil {
		err := s.sendErr
		s.sendErr = nil
		return err
	}
	return nil
}

func (s *fakeWriteStream) CloseAndRecv() (*scrapv1.WriteDocumentResponse, error) {
	if s.closeErr != nil {
		return nil, s.closeErr
	}
	return &scrapv1.WriteDocumentResponse{}, nil
}

type fakeClientStream struct {
	ctx context.Context
}

func (s fakeClientStream) Header() (metadata.MD, error) {
	return metadata.MD{}, nil
}

func (fakeClientStream) Trailer() metadata.MD {
	return nil
}

func (fakeClientStream) CloseSend() error {
	return nil
}

func (s fakeClientStream) Context() context.Context {
	return s.ctx
}

func (fakeClientStream) SendMsg(any) error {
	return nil
}

func (fakeClientStream) RecvMsg(any) error {
	return io.EOF
}
