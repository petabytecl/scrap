package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/server"
)

func TestNotLeaderHeadDocumentReturnsUnavailableWithHint(t *testing.T) {
	client := startNotLeaderServer(t, "scrapd-2.scrap-headless.ns.svc:9090")

	_, err := client.HeadDocument(context.Background(), &scrapv1.HeadDocumentRequest{
		TransactionId: "tx-001",
		DocumentName:  "doc.xml",
	})
	if err == nil {
		t.Fatal("expected error from non-leader")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.Unavailable {
		t.Fatalf("expected UNAVAILABLE, got %v", st.Code())
	}

	var hint *scrapv1.LeaderHint
	for _, detail := range st.Details() {
		if h, ok := detail.(*scrapv1.LeaderHint); ok {
			hint = h
			break
		}
	}
	if hint == nil {
		t.Fatal("expected LeaderHint in status details")
	}
	if hint.GetLeaderAddr() != "scrapd-2.scrap-headless.ns.svc:9090" {
		t.Fatalf("LeaderAddr: got %q", hint.GetLeaderAddr())
	}
}

func TestNotLeaderLogsRedirectWithOperationalIdentity(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})).
		With(
			"service.instance.id", "scrapd-1",
			"scrap.cell_id", "local",
			"scrap.member_slot_id", "scrapd-1",
			"scrap.member_id", "member-123",
			"scrap.shard_id", "0",
			"scrap.raft_id", "2",
		)

	client := startServerWith(t,
		&notLeaderStore{leaderAddr: "scrapd-0.scrap-headless.scrap.svc.cluster.local:9090"},
		server.WithLogger(logger),
	)
	_, err := client.HeadDocument(context.Background(), &scrapv1.HeadDocumentRequest{
		TransactionId: "tx-001",
		DocumentName:  "doc.xml",
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("HeadDocument status = %v, want Unavailable", status.Code(err))
	}

	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &entry); err != nil {
		t.Fatalf("decode log entry %q: %v", logs.String(), err)
	}

	assertLogField(t, entry, "level", "DEBUG")
	assertLogField(t, entry, "msg", "request redirected to shard leader")
	assertLogField(t, entry, "component", "server")
	assertLogField(t, entry, "rpc.service", "scrap.v1.DocumentService")
	assertLogField(t, entry, "rpc.method", "HeadDocument")
	assertLogField(t, entry, "rpc.grpc.status_code", "Unavailable")
	assertLogField(t, entry, "reason", "not_leader")
	assertLogField(t, entry, "scrap.role", "follower")
	assertLogField(t, entry, "leader_addr", "scrapd-0.scrap-headless.scrap.svc.cluster.local:9090")
	assertLogField(t, entry, "service.instance.id", "scrapd-1")
	assertLogField(t, entry, "scrap.cell_id", "local")
	assertLogField(t, entry, "scrap.member_slot_id", "scrapd-1")
	assertLogField(t, entry, "scrap.member_id", "member-123")
	assertLogField(t, entry, "scrap.shard_id", "0")
	assertLogField(t, entry, "scrap.raft_id", "2")
	assertLogFieldAbsent(t, entry, "transaction_id")
	assertLogFieldAbsent(t, entry, "document_name")
}

func assertLogField(t *testing.T, entry map[string]any, key, want string) {
	t.Helper()

	got, ok := entry[key]
	if !ok {
		t.Fatalf("log field %q missing in %v", key, entry)
	}
	if got != want {
		t.Fatalf("log field %q = %q, want %q", key, got, want)
	}
}

func assertLogFieldAbsent(t *testing.T, entry map[string]any, key string) {
	t.Helper()

	if _, ok := entry[key]; ok {
		t.Fatalf("log field %q unexpectedly present in %v", key, entry)
	}
}
