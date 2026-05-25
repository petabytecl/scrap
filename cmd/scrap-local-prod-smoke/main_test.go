package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestRunReportsReplicatedACKSuccess(t *testing.T) {
	publicAddress, stopPublic := startSmokePublicServer(t, smokePublicServer{
		response: &scrapv1.WriteDocumentResponse{
			DesiredReplicaCount:  3,
			AchievedReplicaCount: 3,
		},
	})
	defer stopPublic()
	adminAddress, stopAdmin := startSmokeAdminServer(t, smokeAdminServer{
		blockIDs:         []string{"block-1"},
		replicaMemberIDs: []string{"scrapd-2", "scrapd-0", "scrapd-1"},
	})
	defer stopAdmin()
	stdout := tempOutput(t)

	err := run(context.Background(), []string{
		"--public-addr", publicAddress,
		"--admin-addr", adminAddress,
		"--expected-replica-count", "3",
		"--timeout", "5s",
	}, stdout)
	testutil.RequireNoErrorf(t, err, "run success smoke")

	report := readReport(t, stdout)
	testutil.RequireEqualf(t, report.Mode, "replicated_ack_success", "report mode")
	testutil.RequireEqualf(t, report.ConfiguredTarget, uint64(3), "target replicas")
	testutil.RequireEqualf(t, report.Write.AchievedReplicaCount, uint32(3), "achieved replicas")
	testutil.RequireDeepEqualf(t, report.Block.ReplicaMemberID, []string{"scrapd-0", "scrapd-1", "scrapd-2"}, "sorted replica members")
}

func TestRunReportsFailClosedUnavailable(t *testing.T) {
	publicAddress, stopPublic := startSmokePublicServer(t, smokePublicServer{
		err: status.Error(codes.Unavailable, "required peer replicas could not be prepared"),
	})
	defer stopPublic()
	stdout := tempOutput(t)

	err := run(context.Background(), []string{
		"--public-addr", publicAddress,
		"--admin-addr", "127.0.0.1:1",
		"--expected-replica-count", "3",
		"--expect-failure",
		"--timeout", "5s",
	}, stdout)
	testutil.RequireNoErrorf(t, err, "run fail-closed smoke")

	report := readReport(t, stdout)
	testutil.RequireEqualf(t, report.Mode, "replicated_ack_fail_closed", "report mode")
	testutil.RequireEqualf(t, report.ObservedFailureCode, codes.Unavailable.String(), "observed code")
}

func TestParseOptionsRejectsInvalidInput(t *testing.T) {
	_, err := parseOptions([]string{"--expected-replica-count", "0"})
	requireError(t, err, "zero replica count")
	_, err = parseOptions([]string{"extra"})
	requireError(t, err, "unexpected arg")
}

func tempOutput(t *testing.T) *os.File {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "smoke-*.json")
	testutil.RequireNoErrorf(t, err, "create temp output")
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func readReport(t *testing.T, file *os.File) report {
	t.Helper()
	_, err := file.Seek(0, io.SeekStart)
	testutil.RequireNoErrorf(t, err, "rewind report")
	var out report
	testutil.RequireNoErrorf(t, json.NewDecoder(file).Decode(&out), "decode report")
	return out
}

type smokePublicServer struct {
	scrapv1.UnimplementedDocumentServiceServer
	response *scrapv1.WriteDocumentResponse
	err      error
}

func (s smokePublicServer) WriteDocument(stream grpc.ClientStreamingServer[scrapv1.WriteDocumentRequest, scrapv1.WriteDocumentResponse]) error {
	for {
		_, err := stream.Recv()
		if err == nil {
			continue
		}
		if !errors.Is(err, io.EOF) {
			return err
		}
		if s.err != nil {
			return s.err
		}
		return stream.SendAndClose(s.response)
	}
}

type smokeAdminServer struct {
	adminv1.UnimplementedInspectServiceServer
	blockIDs         []string
	replicaMemberIDs []string
}

func (s smokeAdminServer) GetDocument(context.Context, *adminv1.GetDocumentRequest) (*adminv1.GetDocumentResponse, error) {
	return &adminv1.GetDocumentResponse{
		Document: &adminv1.AdminDocument{BlockIds: append([]string(nil), s.blockIDs...)},
	}, nil
}

func (s smokeAdminServer) GetBlock(context.Context, *adminv1.GetBlockRequest) (*adminv1.GetBlockResponse, error) {
	return &adminv1.GetBlockResponse{
		Block: &adminv1.Block{
			BlockId:          "block-1",
			ReplicaMemberIds: append([]string(nil), s.replicaMemberIDs...),
		},
	}, nil
}

func startSmokePublicServer(t *testing.T, handler smokePublicServer) (string, func()) {
	t.Helper()
	listener, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	testutil.RequireNoErrorf(t, err, "listen public smoke server")
	server := grpc.NewServer()
	scrapv1.RegisterDocumentServiceServer(server, handler)
	go func() { _ = server.Serve(listener) }()
	return listener.Addr().String(), func() {
		server.Stop()
		_ = listener.Close()
	}
}

func startSmokeAdminServer(t *testing.T, handler smokeAdminServer) (string, func()) {
	t.Helper()
	listener, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	testutil.RequireNoErrorf(t, err, "listen admin smoke server")
	server := grpc.NewServer()
	adminv1.RegisterInspectServiceServer(server, handler)
	go func() { _ = server.Serve(listener) }()
	return listener.Addr().String(), func() {
		server.Stop()
		_ = listener.Close()
	}
}

func TestWriteFailureReportRejectsUnexpectedOutcomes(t *testing.T) {
	stdout := tempOutput(t)
	opts := options{expectedReplicaCount: 3}
	doc := smokeDocument(options{tenantID: "tenant-a"})
	requireError(t, writeFailureReport(stdout, opts, doc, nil), "unexpected success")
	requireError(t, writeFailureReport(stdout, opts, doc, status.Error(codes.Internal, "boom")), "wrong status")
}

func TestRunRejectsMismatchedReplicaCounts(t *testing.T) {
	publicAddress, stopPublic := startSmokePublicServer(t, smokePublicServer{
		response: &scrapv1.WriteDocumentResponse{
			DesiredReplicaCount:  3,
			AchievedReplicaCount: 2,
		},
	})
	defer stopPublic()
	stdout := tempOutput(t)
	err := run(context.Background(), []string{
		"--public-addr", publicAddress,
		"--admin-addr", "127.0.0.1:1",
		"--expected-replica-count", "3",
		"--timeout", (5 * time.Second).String(),
	}, stdout)
	requireError(t, err, "mismatched achieved replicas")
}

func requireError(t testing.TB, err error, msg string) {
	t.Helper()
	if err == nil {
		t.Fatal(msg + ": expected error")
	}
}
