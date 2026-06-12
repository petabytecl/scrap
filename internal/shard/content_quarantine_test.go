package shard

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/avscan"
	"github.com/petabytecl/scrap/internal/index"
)

func TestShardReportDetectionsProposesQuarantineCommand(t *testing.T) {
	raft := &quarantineProposalRaft{}
	idx := openApplyTestIndex(t)
	s := shardForApplyTest(t, idx)
	s.raft = raft
	raft.onPropose = func(data []byte) {
		applyQuarantineProposal(t, s, data)
	}

	err := s.ReportDetections(context.Background(), avscan.Block{BlockID: 42}, []avscan.Detection{{
		TransactionID: "tx-quarantine",
		DocumentName:  "detected.xml",
		DetectedAtUs:  1716700001000000,
		ScanType:      avscan.DetectionScanTypeInitial,
		Reason:        avscan.DetectionReasonScannerDetection,
	}})
	if err != nil {
		t.Fatalf("ReportDetections: %v", err)
	}
	if len(raft.proposed) != 1 {
		t.Fatalf("proposed commands = %d, want 1", len(raft.proposed))
	}
	cmd := &scrapv1.RaftCommand{}
	if err := proto.Unmarshal(raft.proposed[0], cmd); err != nil {
		t.Fatalf("unmarshal proposed command: %v", err)
	}
	quarantine := cmd.GetQuarantineDoc()
	if quarantine == nil {
		t.Fatalf("proposed command = %T, want QuarantineDocument", cmd.GetCommand())
	}
	assertQuarantineCommand(t, quarantine)
}

func TestShardReportDetectionsWaitsForQuarantineApply(t *testing.T) {
	raft := &quarantineProposalRaft{
		proposedCh: make(chan []byte, 1),
	}
	idx := openApplyTestIndex(t)
	s := shardForApplyTest(t, idx)
	s.raft = raft

	done := make(chan error, 1)
	go func() {
		done <- s.ReportDetections(context.Background(), avscan.Block{BlockID: 42}, []avscan.Detection{{
			TransactionID: "tx-quarantine",
			DocumentName:  "detected.xml",
			DetectedAtUs:  1716700001000000,
			ScanType:      avscan.DetectionScanTypeInitial,
			Reason:        avscan.DetectionReasonScannerDetection,
		}})
	}()

	var data []byte
	select {
	case data = <-raft.proposedCh:
	case <-time.After(time.Second):
		t.Fatal("ReportDetections did not propose quarantine command")
	}
	select {
	case err := <-done:
		t.Fatalf("ReportDetections completed before apply: %v", err)
	default:
	}

	applyQuarantineProposal(t, s, data)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ReportDetections after apply: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ReportDetections did not complete after apply")
	}
}

func TestShardReportDetectionsValidatesBatchBeforeProposal(t *testing.T) {
	raft := &quarantineProposalRaft{}
	s := &Shard{raft: raft}

	err := s.ReportDetections(context.Background(), avscan.Block{BlockID: 42}, []avscan.Detection{
		{
			TransactionID: "tx-quarantine",
			DocumentName:  "detected.xml",
			DetectedAtUs:  1716700001000000,
			ScanType:      avscan.DetectionScanTypeInitial,
			Reason:        avscan.DetectionReasonScannerDetection,
		},
		{
			TransactionID: "tx-quarantine",
			DocumentName:  "missing-time.xml",
			ScanType:      avscan.DetectionScanTypeInitial,
			Reason:        avscan.DetectionReasonScannerDetection,
		},
	})
	if err == nil {
		t.Fatal("ReportDetections succeeded for invalid batch")
	}
	if len(raft.proposed) != 0 {
		t.Fatalf("proposed commands = %d, want 0", len(raft.proposed))
	}
}

func TestApplyQuarantineDocumentStoresProjectionState(t *testing.T) {
	idx := openApplyTestIndex(t)
	s := shardForApplyTest(t, idx)

	if err := s.applyEntryCommand(quarantineRaftCommandForTest(), 77); err != nil {
		t.Fatalf("applyEntryCommand: %v", err)
	}

	got, err := idx.GetContentQuarantine("tx-quarantine", "detected.xml")
	if err != nil {
		t.Fatalf("GetContentQuarantine: %v", err)
	}
	want := index.ContentQuarantine{
		TransactionID: "tx-quarantine",
		DocumentName:  "detected.xml",
		BlockID:       42,
		DetectedAtUs:  1716700001000000,
		ScanType:      index.ContentQuarantineScanTypeInitial,
		Reason:        index.ContentQuarantineReasonScannerDetection,
	}
	if got != want {
		t.Fatalf("ContentQuarantine = %+v, want %+v", got, want)
	}
}

func TestApplyQuarantineDocumentSurvivesProjectionReopen(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	s := shardForApplyTest(t, idx)
	if err := s.applyEntryCommand(quarantineRaftCommandForTest(), 77); err != nil {
		t.Fatalf("applyEntryCommand: %v", err)
	}
	if err := idx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := index.Open(dir)
	if err != nil {
		t.Fatalf("reopen index: %v", err)
	}
	defer func() { _ = reopened.Close() }()

	got, err := reopened.GetContentQuarantine("tx-quarantine", "detected.xml")
	if err != nil {
		t.Fatalf("GetContentQuarantine after reopen: %v", err)
	}
	if got.BlockID != 42 || got.ScanType != index.ContentQuarantineScanTypeInitial {
		t.Fatalf("ContentQuarantine after reopen = %+v", got)
	}
}

func TestApplyQuarantineDocumentRebuildsFreshProjectionFromRaftReplay(t *testing.T) {
	idx := openApplyTestIndex(t)
	s := shardForApplyTest(t, idx)
	data, err := proto.Marshal(quarantineRaftCommandForTest())
	if err != nil {
		t.Fatalf("marshal command: %v", err)
	}

	err = s.applyEntries([]raftpb.Entry{{
		Type:  raftpb.EntryNormal,
		Index: 77,
		Data:  data,
	}}, 0)
	if err != nil {
		t.Fatalf("applyEntries: %v", err)
	}

	if _, err := idx.GetContentQuarantine("tx-quarantine", "detected.xml"); err != nil {
		t.Fatalf("GetContentQuarantine after replay: %v", err)
	}
}

func TestApplyQuarantineDocumentIsDuplicateSafe(t *testing.T) {
	idx := openApplyTestIndex(t)
	s := shardForApplyTest(t, idx)

	if err := s.applyEntryCommand(quarantineRaftCommandForTest(), 77); err != nil {
		t.Fatalf("first applyEntryCommand: %v", err)
	}
	if err := s.applyEntryCommand(quarantineRaftCommandForTest(), 78); err != nil {
		t.Fatalf("second applyEntryCommand: %v", err)
	}

	if _, err := idx.GetContentQuarantine("tx-quarantine", "detected.xml"); err != nil {
		t.Fatalf("GetContentQuarantine after duplicate apply: %v", err)
	}
}

func TestApplyQuarantineDocumentRejectsInvalidMetadata(t *testing.T) {
	idx := openApplyTestIndex(t)
	s := shardForApplyTest(t, idx)

	err := s.applyEntryCommand(&scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_QuarantineDoc{
			QuarantineDoc: &scrapv1.QuarantineDocument{
				TransactionId: "",
				DocumentName:  "detected.xml",
				BlockId:       42,
				DetectedAtUs:  1716700001000000,
				ScanType:      scrapv1.QuarantineScanType_QUARANTINE_SCAN_TYPE_INITIAL,
				Reason:        scrapv1.QuarantineReason_QUARANTINE_REASON_SCANNER_DETECTION,
			},
		},
	}, 77)
	if err == nil {
		t.Fatal("applyEntryCommand succeeded for invalid metadata")
	}
	if _, getErr := idx.GetContentQuarantine("tx-quarantine", "detected.xml"); !errors.Is(getErr, index.ErrContentQuarantineNotFound) {
		t.Fatalf("GetContentQuarantine after invalid apply = %v, want ErrContentQuarantineNotFound", getErr)
	}
}

func TestApplyQuarantineDocumentRejectsMissingDetectedTime(t *testing.T) {
	idx := openApplyTestIndex(t)
	s := shardForApplyTest(t, idx)

	err := s.applyEntryCommand(&scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_QuarantineDoc{
			QuarantineDoc: &scrapv1.QuarantineDocument{
				TransactionId: "tx-quarantine",
				DocumentName:  "detected.xml",
				BlockId:       42,
				ScanType:      scrapv1.QuarantineScanType_QUARANTINE_SCAN_TYPE_INITIAL,
				Reason:        scrapv1.QuarantineReason_QUARANTINE_REASON_SCANNER_DETECTION,
			},
		},
	}, 77)
	if err == nil {
		t.Fatal("applyEntryCommand succeeded without detected_at_us")
	}
	if _, getErr := idx.GetContentQuarantine("tx-quarantine", "detected.xml"); !errors.Is(getErr, index.ErrContentQuarantineNotFound) {
		t.Fatalf("GetContentQuarantine after invalid apply = %v, want ErrContentQuarantineNotFound", getErr)
	}
}

func TestApplySpanInfoForQuarantineDocumentRedactsIdentity(t *testing.T) {
	assertApplySpan(t, quarantineRaftCommandForTest(), "quarantine_document", []string{
		"scrap.transaction.hash",
		"scrap.document.hash",
		"scrap.block_id",
	})
}

func quarantineRaftCommandForTest() *scrapv1.RaftCommand {
	return &scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_QuarantineDoc{
			QuarantineDoc: &scrapv1.QuarantineDocument{
				TransactionId: "tx-quarantine",
				DocumentName:  "detected.xml",
				BlockId:       42,
				DetectedAtUs:  1716700001000000,
				ScanType:      scrapv1.QuarantineScanType_QUARANTINE_SCAN_TYPE_INITIAL,
				Reason:        scrapv1.QuarantineReason_QUARANTINE_REASON_SCANNER_DETECTION,
			},
		},
	}
}

func assertQuarantineCommand(t *testing.T, quarantine *scrapv1.QuarantineDocument) {
	t.Helper()

	if quarantine.GetTransactionId() != "tx-quarantine" {
		t.Fatalf("transaction_id = %q, want tx-quarantine", quarantine.GetTransactionId())
	}
	if quarantine.GetDocumentName() != "detected.xml" {
		t.Fatalf("document_name = %q, want detected.xml", quarantine.GetDocumentName())
	}
	if quarantine.GetBlockId() != 42 {
		t.Fatalf("block_id = %d, want 42", quarantine.GetBlockId())
	}
	if quarantine.GetDetectedAtUs() != 1716700001000000 {
		t.Fatalf("detected_at_us = %d, want 1716700001000000", quarantine.GetDetectedAtUs())
	}
	if quarantine.GetScanType() != scrapv1.QuarantineScanType_QUARANTINE_SCAN_TYPE_INITIAL {
		t.Fatalf("scan_type = %v, want INITIAL", quarantine.GetScanType())
	}
	if quarantine.GetReason() != scrapv1.QuarantineReason_QUARANTINE_REASON_SCANNER_DETECTION {
		t.Fatalf("reason = %v, want SCANNER_DETECTION", quarantine.GetReason())
	}
}

type quarantineProposalRaft struct {
	proposed   [][]byte
	proposedCh chan []byte
	onPropose  func([]byte)
}

func (r *quarantineProposalRaft) Propose(_ context.Context, data []byte) error {
	copied := append([]byte(nil), data...)
	r.proposed = append(r.proposed, copied)
	if r.proposedCh != nil {
		r.proposedCh <- copied
	}
	if r.onPropose != nil {
		r.onPropose(copied)
	}
	return nil
}

func (r *quarantineProposalRaft) ReadIndex(context.Context) (uint64, error) { return 0, nil }

func (r *quarantineProposalRaft) Step(context.Context, raftpb.Message) error { return nil }

func (r *quarantineProposalRaft) IsLeader() bool { return true }

func (r *quarantineProposalRaft) LeaderID() uint64 { return 1 }

func (r *quarantineProposalRaft) AppliedIndex() uint64 { return 0 }

func (r *quarantineProposalRaft) CommitIndex() uint64 { return 0 }

func (r *quarantineProposalRaft) WithStableLeadership(run func() error) error { return run() }

func (r *quarantineProposalRaft) Stop() {}

func applyQuarantineProposal(t *testing.T, s *Shard, data []byte) {
	t.Helper()

	cmd := &scrapv1.RaftCommand{}
	if err := proto.Unmarshal(data, cmd); err != nil {
		t.Fatalf("unmarshal proposed command: %v", err)
	}
	if err := s.applyEntryCommand(cmd, 77); err != nil {
		t.Fatalf("applyEntryCommand: %v", err)
	}
}
