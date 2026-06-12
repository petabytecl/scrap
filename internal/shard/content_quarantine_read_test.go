package shard

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

const quarantineReadTestDocumentName = "unsafe.xml"

func TestReadDocumentDeniesQuarantinedDocument(t *testing.T) {
	ctx := context.Background()
	s := openQuarantineReadShard(t)

	if _, err := s.WriteDocument(ctx, "tx-read-quarantine", quarantineReadTestDocumentName, "text/xml", "", bytes.NewReader([]byte("unsafe payload"))); err != nil {
		t.Fatalf("WriteDocument unsafe: %v", err)
	}
	if err := s.applyEntryCommand(quarantineRaftCommandForReadTest("tx-read-quarantine"), 77); err != nil {
		t.Fatalf("applyEntryCommand quarantine: %v", err)
	}

	rc, meta, err := s.ReadDocument(ctx, "tx-read-quarantine", quarantineReadTestDocumentName)
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

	if _, err := s.WriteDocument(ctx, "tx-read-quarantine", quarantineReadTestDocumentName, "text/xml", "", bytes.NewReader([]byte("unsafe payload"))); err != nil {
		t.Fatalf("WriteDocument unsafe: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-read-quarantine", "other.xml", "text/xml", "", bytes.NewReader([]byte("other payload"))); err != nil {
		t.Fatalf("WriteDocument other: %v", err)
	}
	if err := s.applyEntryCommand(quarantineRaftCommandForReadTest("tx-read-quarantine"), 77); err != nil {
		t.Fatalf("applyEntryCommand quarantine: %v", err)
	}

	head, err := s.HeadDocument(ctx, "tx-read-quarantine", quarantineReadTestDocumentName)
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
	if statuses[quarantineReadTestDocumentName] != storeapi.ScanStatusQuarantined {
		t.Fatalf("FindDocuments unsafe status = %v, want quarantined", statuses[quarantineReadTestDocumentName])
	}
	if statuses["other.xml"] != storeapi.ScanStatusUnscanned {
		t.Fatalf("FindDocuments other status = %v, want unscanned", statuses["other.xml"])
	}
}

func TestReadDocumentDeniedAfterQuarantineRaftReplay(t *testing.T) {
	ctx := context.Background()
	s := openQuarantineReadShard(t)

	if _, err := s.WriteDocument(ctx, "tx-replay-quarantine", quarantineReadTestDocumentName, "text/xml", "", bytes.NewReader([]byte("unsafe payload"))); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	data, err := proto.Marshal(quarantineRaftCommandForReadTest("tx-replay-quarantine"))
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

	rc, _, err := s.ReadDocument(ctx, "tx-replay-quarantine", quarantineReadTestDocumentName)
	if !errors.Is(err, storeapi.ErrFailedPrecondition) {
		t.Fatalf("ReadDocument error = %v, want ErrFailedPrecondition", err)
	}
	if rc != nil {
		_ = rc.Close()
		t.Fatal("ReadDocument returned a reader after quarantine replay")
	}
}

func TestContentQuarantineReadCloserDeniesBytesAfterConcurrentQuarantine(t *testing.T) {
	idx := openApplyTestIndex(t)
	s := shardForApplyTest(t, idx)
	inner := &quarantiningReadCloser{
		payload: []byte("unsafe payload"),
		beforeReturn: func() {
			if err := s.applyEntryCommand(quarantineRaftCommandForReadTest("tx-race-quarantine"), 77); err != nil {
				t.Fatalf("applyEntryCommand quarantine: %v", err)
			}
		},
	}
	rc := contentQuarantineReadCloser{
		inner:   inner,
		shard:   s,
		txID:    "tx-race-quarantine",
		docName: quarantineReadTestDocumentName,
	}

	buf := make([]byte, len(inner.payload))
	n, err := rc.Read(buf)
	if !errors.Is(err, storeapi.ErrFailedPrecondition) {
		t.Fatalf("Read error = %v, want ErrFailedPrecondition", err)
	}
	if n != 0 {
		t.Fatalf("Read returned n=%d bytes=%q, want no bytes", n, buf[:n])
	}
}

func TestReleaseQuarantineAllowsReadOnlyAfterCommittedApply(t *testing.T) {
	ctx := context.Background()
	s := openQuarantineReadShard(t)

	if _, err := s.WriteDocument(ctx, "tx-quarantine", "detected.xml", "text/xml", "", bytes.NewReader([]byte("safe after release"))); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	if err := s.applyEntryCommand(quarantineRaftCommandForTest(), 77); err != nil {
		t.Fatalf("apply quarantine: %v", err)
	}
	if _, _, err := s.ReadDocument(ctx, "tx-quarantine", "detected.xml"); !errors.Is(err, storeapi.ErrFailedPrecondition) {
		t.Fatalf("ReadDocument after quarantine = %v, want ErrFailedPrecondition", err)
	}
	if err := s.applyEntryCommand(confirmQuarantineRaftCommandForTest("proposal-confirm"), 78); err != nil {
		t.Fatalf("apply confirm: %v", err)
	}
	if _, _, err := s.ReadDocument(ctx, "tx-quarantine", "detected.xml"); !errors.Is(err, storeapi.ErrFailedPrecondition) {
		t.Fatalf("ReadDocument after confirm = %v, want ErrFailedPrecondition", err)
	}
	if err := s.applyEntryCommand(releaseQuarantineRaftCommandForTest("proposal-release"), 79); err != nil {
		t.Fatalf("apply release: %v", err)
	}

	rc, _, err := s.ReadDocument(ctx, "tx-quarantine", "detected.xml")
	if err != nil {
		t.Fatalf("ReadDocument after release: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadDocument body after release: %v", err)
	}
	if string(got) != "safe after release" {
		t.Fatalf("ReadDocument body = %q, want safe after release", got)
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

type quarantiningReadCloser struct {
	payload      []byte
	beforeReturn func()
}

func (r *quarantiningReadCloser) Read(dst []byte) (int, error) {
	if r.beforeReturn != nil {
		r.beforeReturn()
	}
	return copy(dst, r.payload), nil
}

func (r *quarantiningReadCloser) Close() error {
	return nil
}

func quarantineRaftCommandForReadTest(txID string) *scrapv1.RaftCommand {
	return &scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_QuarantineDoc{
			QuarantineDoc: &scrapv1.QuarantineDocument{
				TransactionId: txID,
				DocumentName:  quarantineReadTestDocumentName,
				BlockId:       1,
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
