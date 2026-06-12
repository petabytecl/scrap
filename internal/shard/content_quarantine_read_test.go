package shard

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestReadDocumentDeniesQuarantinedDocument(t *testing.T) {
	ctx := context.Background()
	s := openQuarantineReadShard(t)

	if _, err := s.WriteDocument(ctx, "tx-read-quarantine", "unsafe.xml", "text/xml", "", bytes.NewReader([]byte("unsafe payload"))); err != nil {
		t.Fatalf("WriteDocument unsafe: %v", err)
	}
	if err := s.applyEntryCommand(quarantineRaftCommandForReadTest("tx-read-quarantine", "unsafe.xml", 1), 77); err != nil {
		t.Fatalf("applyEntryCommand quarantine: %v", err)
	}

	rc, meta, err := s.ReadDocument(ctx, "tx-read-quarantine", "unsafe.xml")
	if !errors.Is(err, storeapi.ErrFailedPrecondition) {
		t.Fatalf("ReadDocument error = %v, want ErrFailedPrecondition", err)
	}
	if rc != nil {
		_ = rc.Close()
		t.Fatal("ReadDocument returned a reader for quarantined Document")
	}
	if meta != (storeapi.DocumentMeta{}) {
		t.Fatalf("ReadDocument metadata = %+v, want zero value", meta)
	}
}

func TestQuarantineMetadataScanStatusStaysAvailable(t *testing.T) {
	ctx := context.Background()
	s := openQuarantineReadShard(t)

	if _, err := s.WriteDocument(ctx, "tx-read-quarantine", "unsafe.xml", "text/xml", "", bytes.NewReader([]byte("unsafe payload"))); err != nil {
		t.Fatalf("WriteDocument unsafe: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-read-quarantine", "other.xml", "text/xml", "", bytes.NewReader([]byte("other payload"))); err != nil {
		t.Fatalf("WriteDocument other: %v", err)
	}
	if err := s.applyEntryCommand(quarantineRaftCommandForReadTest("tx-read-quarantine", "unsafe.xml", 1), 77); err != nil {
		t.Fatalf("applyEntryCommand quarantine: %v", err)
	}

	head, err := s.HeadDocument(ctx, "tx-read-quarantine", "unsafe.xml")
	if err != nil {
		t.Fatalf("HeadDocument quarantined: %v", err)
	}
	if head.ScanStatus != storeapi.ScanStatusQuarantined {
		t.Fatalf("HeadDocument scan status = %v, want quarantined", head.ScanStatus)
	}

	docs, err := s.FindDocuments(ctx, "tx-read-quarantine")
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}
	statuses := scanStatusesByDocument(docs)
	if statuses["unsafe.xml"] != storeapi.ScanStatusQuarantined {
		t.Fatalf("FindDocuments unsafe status = %v, want quarantined", statuses["unsafe.xml"])
	}
	if statuses["other.xml"] != storeapi.ScanStatusUnscanned {
		t.Fatalf("FindDocuments other status = %v, want unscanned", statuses["other.xml"])
	}
}

func TestReadDocumentDeniedAfterQuarantineRaftReplay(t *testing.T) {
	ctx := context.Background()
	s := openQuarantineReadShard(t)

	if _, err := s.WriteDocument(ctx, "tx-replay-quarantine", "unsafe.xml", "text/xml", "", bytes.NewReader([]byte("unsafe payload"))); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	data, err := proto.Marshal(quarantineRaftCommandForReadTest("tx-replay-quarantine", "unsafe.xml", 1))
	if err != nil {
		t.Fatalf("marshal quarantine command: %v", err)
	}
	if err := s.applyEntries([]raftpb.Entry{{
		Type:  raftpb.EntryNormal,
		Index: 88,
		Data:  data,
	}}, 0); err != nil {
		t.Fatalf("applyEntries quarantine replay: %v", err)
	}

	rc, _, err := s.ReadDocument(ctx, "tx-replay-quarantine", "unsafe.xml")
	if !errors.Is(err, storeapi.ErrFailedPrecondition) {
		t.Fatalf("ReadDocument error = %v, want ErrFailedPrecondition", err)
	}
	if rc != nil {
		_ = rc.Close()
		t.Fatal("ReadDocument returned a reader after quarantine replay")
	}
}

func TestReadDocumentFailsClosedForCorruptQuarantineState(t *testing.T) {
	ctx := context.Background()
	s := openQuarantineReadShard(t)

	if _, err := s.WriteDocument(ctx, "tx-corrupt-quarantine", "unsafe.xml", "text/xml", "", bytes.NewReader([]byte("unsafe payload"))); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	if err := s.idx.CorruptContentQuarantineForTest("tx-corrupt-quarantine", "unsafe.xml", nil); err != nil {
		t.Fatalf("CorruptContentQuarantineForTest: %v", err)
	}

	rc, _, err := s.ReadDocument(ctx, "tx-corrupt-quarantine", "unsafe.xml")
	if !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("ReadDocument error = %v, want ErrDataLoss", err)
	}
	if rc != nil {
		_ = rc.Close()
		t.Fatal("ReadDocument returned a reader for corrupt quarantine state")
	}
}

func openQuarantineReadShard(t *testing.T) *Shard {
	t.Helper()

	s, err := Open(Config{
		DataDir:      t.TempDir(),
		ShardID:      0,
		RaftID:       1,
		Peers:        map[uint64]string{1: "localhost:9091"},
		TickInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.IsLeader() {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("shard did not become leader")
	return nil
}

func quarantineRaftCommandForReadTest(txID, docName string, blockID uint64) *scrapv1.RaftCommand {
	return &scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_QuarantineDoc{
			QuarantineDoc: &scrapv1.QuarantineDocument{
				TransactionId: txID,
				DocumentName:  docName,
				BlockId:       blockID,
				DetectedAtUs:  1716700001000000,
				ScanType:      scrapv1.QuarantineScanType_QUARANTINE_SCAN_TYPE_INITIAL,
				Reason:        scrapv1.QuarantineReason_QUARANTINE_REASON_SCANNER_DETECTION,
			},
		},
	}
}

func scanStatusesByDocument(docs []storeapi.DocumentMeta) map[string]storeapi.ScanStatus {
	statuses := make(map[string]storeapi.ScanStatus, len(docs))
	for _, doc := range docs {
		statuses[doc.Name] = doc.ScanStatus
	}
	return statuses
}
