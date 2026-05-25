package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/petabytecl/scrap/internal/blockstore"
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
	memberID string
	bytes    []byte
}

func (p *recordingPeerReplicationPreparer) PrepareDocument(ctx context.Context, request replication.PrepareRequest) (replication.Receipt, error) {
	var data bytes.Buffer
	if err := request.WriteBytes(ctx, &data); err != nil {
		return replication.Receipt{}, err
	}
	p.bytes = append(p.bytes[:0], data.Bytes()...)
	return replication.ReceiptFromPreparedDocument(p.memberID, request.Document), nil
}
