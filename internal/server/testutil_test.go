package server_test

import (
	"context"
	"io"
	"net"
	"testing"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/server"
	storeapi "github.com/petabytecl/scrap/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type notLeaderStore struct {
	leaderAddr string
}

func (s *notLeaderStore) WriteDocument(_ context.Context, _, _, _, _ string, _ io.Reader) (storeapi.WriteResult, error) {
	return storeapi.WriteResult{}, &storeapi.NotLeaderError{LeaderAddr: s.leaderAddr}
}

func (s *notLeaderStore) HeadDocument(_ context.Context, _, _ string) (storeapi.DocumentMeta, error) {
	return storeapi.DocumentMeta{}, &storeapi.NotLeaderError{LeaderAddr: s.leaderAddr}
}

func (s *notLeaderStore) ReadDocument(_ context.Context, _, _ string) (io.ReadCloser, storeapi.DocumentMeta, error) {
	return nil, storeapi.DocumentMeta{}, &storeapi.NotLeaderError{LeaderAddr: s.leaderAddr}
}

func (s *notLeaderStore) FindDocuments(_ context.Context, _ string) ([]storeapi.DocumentMeta, error) {
	return nil, &storeapi.NotLeaderError{LeaderAddr: s.leaderAddr}
}

func startNotLeaderServer(t *testing.T, leaderAddr string) scrapv1.DocumentServiceClient {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	gs := grpc.NewServer()
	server.Register(gs, &notLeaderStore{leaderAddr: leaderAddr})
	go gs.Serve(lis)
	t.Cleanup(gs.GracefulStop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	return scrapv1.NewDocumentServiceClient(conn)
}
