package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/petabytecl/scrap/internal/authz"
	"github.com/petabytecl/scrap/internal/blockstore"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/replication"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestPeerReplicationServerPreparesDocument(t *testing.T) {
	data := []byte("peer rpc bytes")
	document := testPreparedDocument(t, data)
	preparer := &recordingPeerReplicationPreparer{memberID: "scrapd-1"}

	resp, err := NewPeerReplicationServer(preparer).PrepareDocument(
		context.Background(),
		peerPrepareRequestToProto(document, data),
	)
	testutil.RequireNoErrorf(t, err, "prepare peer document")
	testutil.RequireDeepEqualf(t, preparer.bytes, data, "prepared bytes")
	testutil.RequireEqualf(t, resp.GetReceipt().GetMemberId(), "scrapd-1", "receipt member id")
	testutil.RequireEqualf(t, resp.GetReceipt().GetBlockId(), document.BlockID, "receipt block id")
}

func TestPeerReplicationServerRejectsInvalidPreparedBytes(t *testing.T) {
	data := []byte("peer rpc bytes")
	document := testPreparedDocument(t, data)
	req := peerPrepareRequestToProto(document, append([]byte(nil), data...))
	req.Data = append(req.GetData(), 'x')

	_, err := NewPeerReplicationServer(&recordingPeerReplicationPreparer{memberID: "scrapd-1"}).PrepareDocument(context.Background(), req)
	testutil.RequireEqualf(t, status.Code(err), codes.InvalidArgument, "invalid prepared byte code")
}

func TestPeerReplicationServerRequiresConfiguredPreparer(t *testing.T) {
	_, err := NewPeerReplicationServer(nil).PrepareDocument(context.Background(), peerPrepareRequestToProto(testPreparedDocument(t, []byte("data")), []byte("data")))
	testutil.RequireEqualf(t, status.Code(err), codes.Unimplemented, "nil preparer code")
}

func TestPeerReplicationClientPreparerSendsPrepareRPC(t *testing.T) {
	data := []byte("peer client bytes")
	document := testPreparedDocument(t, data)
	preparer := &recordingPeerReplicationPreparer{memberID: "scrapd-2"}
	address, cleanup := startPeerReplicationServer(t, NewPeerReplicationServer(preparer))
	defer cleanup()
	client := NewPeerReplicationClientPreparer(address, "local-operator")
	defer func() { testutil.RequireNoErrorf(t, client.Close(), "close peer client") }()

	receipt, err := client.PrepareDocument(context.Background(), testPrepareRequest(document, data))
	testutil.RequireNoErrorf(t, err, "first prepare peer through client")
	_, err = client.PrepareDocument(context.Background(), testPrepareRequest(document, data))
	testutil.RequireNoErrorf(t, err, "second prepare peer through reused client")
	testutil.RequireEqualf(t, receipt.MemberID, "scrapd-2", "receipt member")
	testutil.RequireDeepEqualf(t, preparer.bytes, data, "client transferred bytes")
	testutil.RequireEqualf(t, preparer.workloadIdentity, "local-operator", "workload metadata")
	testutil.RequireEqualf(t, preparer.prepareCount, 2, "prepare call count")
}

func TestPeerReplicationClientPreparerRejectsInvalidLocalRequest(t *testing.T) {
	_, err := NewPeerReplicationClientPreparer("", "").PrepareDocument(context.Background(), replication.PrepareRequest{})
	testutil.RequireErrorIsf(t, err, replication.ErrReceiptMismatch, "empty client address")
	data := []byte("peer client bytes")
	document := testPreparedDocument(t, data)
	document.StoredLength++
	_, err = NewPeerReplicationClientPreparer("127.0.0.1:1", "").PrepareDocument(context.Background(), replication.PrepareRequest{
		Document: document,
		Source:   testByteSource(data),
	})
	testutil.RequireErrorIsf(t, err, replication.ErrTransferMismatch, "invalid prepared bytes")
}

func TestPeerReplicationServerRejectsInvalidProtoRequests(t *testing.T) {
	data := []byte("peer rpc bytes")
	valid := peerPrepareRequestToProto(testPreparedDocument(t, data), data)
	tests := []struct {
		name string
		req  *adminv1.PrepareDocumentRequest
	}{
		{name: "nil request", req: nil},
		{name: "missing identity", req: clonePrepareRequest(valid, func(req *adminv1.PrepareDocumentRequest) { req.Identity = nil })},
		{name: "invalid logical sha", req: clonePrepareRequest(valid, func(req *adminv1.PrepareDocumentRequest) { req.LogicalSha256 = []byte("short") })},
		{name: "invalid stored sha", req: clonePrepareRequest(valid, func(req *adminv1.PrepareDocumentRequest) { req.StoredSha256 = []byte("short") })},
		{name: "nil frame", req: clonePrepareRequest(valid, func(req *adminv1.PrepareDocumentRequest) { req.Frames = []*adminv1.PreparedFrame{nil} })},
		{name: "zero frame segment", req: clonePrepareRequest(valid, func(req *adminv1.PrepareDocumentRequest) {
			req.Frames = []*adminv1.PreparedFrame{{SegmentLength: 0, Sha256: valid.GetStoredSha256()}}
		})},
		{name: "data length mismatch", req: clonePrepareRequest(valid, func(req *adminv1.PrepareDocumentRequest) { req.StoredLength++ })},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewPeerReplicationServer(&recordingPeerReplicationPreparer{memberID: "scrapd-1"}).PrepareDocument(context.Background(), tt.req)
			testutil.RequireEqualf(t, status.Code(err), codes.InvalidArgument, "invalid proto code")
		})
	}
}

func TestPeerPrepareProtoRoundTripsPriorityAndReceiptValidation(t *testing.T) {
	data := []byte("peer rpc bytes")
	document := testPreparedDocument(t, data)
	document.PriorityClass = metastore.PriorityClassCriticalIngest
	req := peerPrepareRequestToProto(document, data)
	testutil.RequireEqualf(t, req.GetPriorityClass(), scrapv1.PriorityClass_PRIORITY_CLASS_CRITICAL_INGEST, "critical priority")
	parsed, _, err := peerPrepareRequestFromProto(req)
	testutil.RequireNoErrorf(t, err, "parse critical request")
	testutil.RequireEqualf(t, parsed.PriorityClass, metastore.PriorityClassCriticalIngest, "parsed priority")

	receipt := peerReplicaReceiptToProto(replication.ReceiptFromPreparedDocument("scrapd-1", document))
	receipt.LogicalSha256 = []byte("short")
	_, err = peerReplicaReceiptFromProto(receipt)
	testutil.RequireEqualf(t, status.Code(err), codes.InvalidArgument, "invalid receipt code")
}

func testPreparedDocument(t *testing.T, data []byte) replication.PreparedDocument {
	t.Helper()
	sum := sha256.Sum256(data)
	blockID, err := identity.NewUUIDv7()
	testutil.RequireNoErrorf(t, err, "new block id")
	return replication.PreparedDocument{
		Identity: identity.Document{
			TenantID:      "tenant-a",
			TransactionID: "txn-rpc",
			DocumentName:  "replica.txt",
		},
		PriorityClass: metastore.PriorityClassNormal,
		BlockID:       blockID,
		StoredOffset:  blockstore.HeaderLength,
		StoredLength:  uint64(len(data)),
		LogicalSHA256: sum,
		StoredSHA256:  sum,
	}
}

type recordingPeerReplicationPreparer struct {
	memberID         string
	bytes            []byte
	workloadIdentity string
	prepareCount     int
}

func (p *recordingPeerReplicationPreparer) PrepareDocument(ctx context.Context, request replication.PrepareRequest) (replication.Receipt, error) {
	var data bytes.Buffer
	if err := request.WriteBytes(ctx, &data); err != nil {
		return replication.Receipt{}, err
	}
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		values := md.Get(authz.WorkloadIdentityMetadataKey)
		if len(values) > 0 {
			p.workloadIdentity = values[0]
		}
	}
	p.prepareCount++
	p.bytes = append(p.bytes[:0], data.Bytes()...)
	return replication.ReceiptFromPreparedDocument(p.memberID, request.Document), nil
}

func testPrepareRequest(document replication.PreparedDocument, data []byte) replication.PrepareRequest {
	return replication.PrepareRequest{
		Document: document,
		Source:   testByteSource(data),
	}
}

func testByteSource(data []byte) replication.ByteSource {
	return replication.ByteSourceFunc(func(_ context.Context, _ replication.PreparedDocument, writer io.Writer) error {
		_, err := writer.Write(data)
		return err
	})
}

func startPeerReplicationServer(t *testing.T, handler *PeerReplicationServer) (string, func()) {
	t.Helper()
	listener, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	testutil.RequireNoErrorf(t, err, "listen peer replication server")
	server := grpc.NewServer()
	RegisterPeerReplicationServer(server, handler)
	go func() { _ = server.Serve(listener) }()
	return listener.Addr().String(), func() {
		server.Stop()
		_ = listener.Close()
	}
}

func clonePrepareRequest(req *adminv1.PrepareDocumentRequest, mutate func(*adminv1.PrepareDocumentRequest)) *adminv1.PrepareDocumentRequest {
	out := peerPrepareRequestToProto(replication.PreparedDocument{
		Identity: identity.Document{
			TenantID:      req.GetIdentity().GetTenantId(),
			TransactionID: req.GetIdentity().GetTransactionId(),
			DocumentName:  req.GetIdentity().GetDocumentName(),
		},
		PriorityClass: peerPreparePriorityFromProto(req.GetPriorityClass()),
		BlockID:       req.GetBlockId(),
		StoredOffset:  req.GetStoredOffset(),
		StoredLength:  req.GetStoredLength(),
		LogicalSHA256: mustSHA256(req.GetLogicalSha256()),
		StoredSHA256:  mustSHA256(req.GetStoredSha256()),
	}, req.GetData())
	mutate(out)
	return out
}

func mustSHA256(value []byte) [32]byte {
	var out [32]byte
	copy(out[:], value)
	return out
}
