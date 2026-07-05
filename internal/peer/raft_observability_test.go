package peer

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
)

func TestForwardRaftStreamRejectsMalformedMessagesWithoutRouting(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	srv := NewServer(t.TempDir(), WithLogger(logger))
	defer func() { _ = srv.Close() }()

	router := &recordingRaftRouter{}
	srv.SetRaftRouter(router)
	stream := &forwardRaftStream{
		ctx: context.Background(),
		requests: []*scrapv1.ForwardRaftStreamRequest{
			{ShardId: 7, Message: []byte("not a raft message")},
		},
	}

	err := srv.ForwardRaftStream(stream)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ForwardRaftStream malformed error = %v (%s), want InvalidArgument", err, status.Code(err))
	}
	if router.calls != 0 {
		t.Fatalf("router calls = %d, want 0", router.calls)
	}
	if got := srv.malformedRaftMsgs.Load(); got != 1 {
		t.Fatalf("malformed raft messages = %d, want 1", got)
	}
	if got := logs.String(); !strings.Contains(got, "peer received malformed raft message") ||
		!strings.Contains(got, `"scrap.shard_id":7`) ||
		!strings.Contains(got, `"malformed_raft_messages":1`) {
		t.Fatalf("malformed raft log missing expected fields: %s", got)
	}
	// AC-2.8.3: malformed-message log output is bounded and redacted.
	if got := logs.String(); strings.Contains(got, "not a raft message") {
		t.Fatalf("malformed raft log leaked raw message bytes: %s", got)
	}
}

// TestForwardRaftMalformedRejectionParity proves AC-2.8.4: unary ForwardRaft
// and streaming ForwardRaftStream reject malformed Raft bytes with the same
// observable gRPC code and neither routes a side effect.
func TestForwardRaftMalformedRejectionParity(t *testing.T) {
	malformed := []byte("not a raft message")

	streamSrv := NewServer(t.TempDir())
	defer func() { _ = streamSrv.Close() }()
	streamRouter := &recordingRaftRouter{}
	streamSrv.SetRaftRouter(streamRouter)
	streamErr := streamSrv.ForwardRaftStream(&forwardRaftStream{
		ctx:      context.Background(),
		requests: []*scrapv1.ForwardRaftStreamRequest{{ShardId: 7, Message: malformed}},
	})

	unarySrv := NewServer(t.TempDir())
	defer func() { _ = unarySrv.Close() }()
	unaryRouter := &recordingRaftRouter{}
	unarySrv.SetRaftRouter(unaryRouter)
	_, unaryErr := unarySrv.ForwardRaft(context.Background(), &scrapv1.ForwardRaftRequest{ShardId: 7, Message: malformed})

	if status.Code(streamErr) != codes.InvalidArgument {
		t.Fatalf("ForwardRaftStream malformed = %v (%s), want InvalidArgument", streamErr, status.Code(streamErr))
	}
	if status.Code(unaryErr) != codes.InvalidArgument {
		t.Fatalf("ForwardRaft malformed = %v (%s), want InvalidArgument", unaryErr, status.Code(unaryErr))
	}
	if status.Code(streamErr) != status.Code(unaryErr) {
		t.Fatalf("malformed rejection codes differ: stream=%s unary=%s", status.Code(streamErr), status.Code(unaryErr))
	}
	if streamRouter.calls != 0 || unaryRouter.calls != 0 {
		t.Fatalf("malformed input routed: stream=%d unary=%d, want 0/0", streamRouter.calls, unaryRouter.calls)
	}
}

func TestShouldLogMalformedRaftCountSamplesPowersOfTwo(t *testing.T) {
	tests := []struct {
		count uint64
		want  bool
	}{
		{count: 1, want: true},
		{count: 2, want: true},
		{count: 3, want: false},
		{count: 4, want: true},
		{count: 8, want: true},
		{count: 9, want: false},
	}

	for _, tt := range tests {
		if got := shouldLogPowerOfTwoCount(tt.count); got != tt.want {
			t.Fatalf("shouldLogPowerOfTwoCount(%d) = %v, want %v", tt.count, got, tt.want)
		}
	}
}

type forwardRaftStream struct {
	grpc.ServerStream
	ctx      context.Context
	requests []*scrapv1.ForwardRaftStreamRequest
	next     int
}

func (s *forwardRaftStream) Context() context.Context {
	return s.ctx
}

func (s *forwardRaftStream) Recv() (*scrapv1.ForwardRaftStreamRequest, error) {
	if s.next >= len(s.requests) {
		return nil, io.EOF
	}
	req := s.requests[s.next]
	s.next++
	return req, nil
}

func (s *forwardRaftStream) Send(*scrapv1.ForwardRaftStreamResponse) error {
	return nil
}
