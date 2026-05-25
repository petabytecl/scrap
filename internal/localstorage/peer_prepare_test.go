package localstorage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"testing"

	"github.com/petabytecl/scrap/internal/blockstore"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/replication"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestPeerPreparerInstallsVerifiedRangeDurably(t *testing.T) {
	app := openTestApplication(t)
	data := []byte("peer prepared durable bytes")
	sum := sha256.Sum256(data)
	blockID, err := identity.NewUUIDv7()
	testutil.RequireNoErrorf(t, err, "new block id")
	document := replication.PreparedDocument{
		Identity: identity.Document{
			TenantID:      "tenant-a",
			TransactionID: "txn-peer",
			DocumentName:  "receipt.txt",
		},
		PriorityClass: metastore.PriorityClassNormal,
		BlockID:       blockID,
		StoredOffset:  blockstore.HeaderLength,
		StoredLength:  uint64(len(data)),
		LogicalSHA256: sum,
		StoredSHA256:  sum,
	}

	receipt, err := PeerPreparer(app).PrepareDocument(context.Background(), replication.PrepareRequest{
		Document: document,
		Source: replication.ByteSourceFunc(func(_ context.Context, _ replication.PreparedDocument, writer io.Writer) error {
			_, err := writer.Write(data)
			return err
		}),
	})
	testutil.RequireNoErrorf(t, err, "prepare peer document")
	testutil.RequireEqualf(t, receipt.MemberID, app.MemberID(), "receipt member id")
	testutil.RequireEqualf(t, receipt.BlockID, blockID, "receipt block id")

	var got bytes.Buffer
	err = app.blocks.ReadRange(context.Background(), blockstore.Record{
		BlockID:       blockID,
		StoredOffset:  blockstore.HeaderLength,
		StoredLength:  uint64(len(data)),
		LogicalSHA256: sum,
		StoredSHA256:  sum,
	}, 0, nil, &got)
	testutil.RequireNoErrorf(t, err, "read installed peer range")
	testutil.RequireDeepEqualf(t, got.Bytes(), data, "installed peer bytes")
}

func TestConfigurePeerPreparationCopiesTargets(t *testing.T) {
	app := openTestApplication(t)
	targets := []replication.Target{{MemberID: "member-1", Preparer: PeerPreparer(app)}}
	ConfigurePeerPreparation(app, PeerPreparationOptions{
		Targets: targets,
		Policy:  replication.Policy{TargetReplicaCount: 2, QuorumReplicaCount: 2},
	})
	targets[0].MemberID = "mutated"

	testutil.RequireEqualf(t, app.peerPrepareTargets[0].MemberID, "member-1", "copied target member")
	testutil.RequireEqualf(t, app.peerPreparePolicy.TargetReplicaCount, 2, "target replicas")
	ConfigurePeerPreparation(nil, PeerPreparationOptions{Targets: targets})
}

func TestPeerPreparerRejectsInvalidInputs(t *testing.T) {
	_, err := PeerPreparer(nil).PrepareDocument(context.Background(), replication.PrepareRequest{})
	testutil.RequireErrorIsf(t, err, replication.ErrReceiptMismatch, "nil application")
	app := openTestApplication(t)
	_, err = PeerPreparer(app).PrepareDocument(context.Background(), replication.PrepareRequest{})
	testutil.RequireErrorIsf(t, err, replication.ErrMissingByteSource, "missing byte source")
}
