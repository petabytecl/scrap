package peer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/grpc"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
)

func TestForwardRaftStreamLogsMalformedMessagesWithoutRouting(t *testing.T) {
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
	if !errors.Is(err, io.EOF) {
		t.Fatalf("ForwardRaftStream error = %v, want EOF", err)
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
		if got := shouldLogMalformedRaftCount(tt.count); got != tt.want {
			t.Fatalf("shouldLogMalformedRaftCount(%d) = %v, want %v", tt.count, got, tt.want)
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
