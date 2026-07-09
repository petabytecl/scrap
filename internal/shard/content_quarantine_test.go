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
	"github.com/petabytecl/scrap/internal/quarantine"
	storeapi "github.com/petabytecl/scrap/internal/store"
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

func TestApplyConfirmQuarantineSurvivesProjectionReopen(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	s := shardForApplyTest(t, idx)
	if err := s.applyEntryCommand(quarantineRaftCommandForTest(), 77); err != nil {
		t.Fatalf("apply quarantine: %v", err)
	}
	if err := s.applyEntryCommand(confirmQuarantineRaftCommandForTest(), 78); err != nil {
		t.Fatalf("apply confirm: %v", err)
	}
	if err := idx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := index.Open(dir)
	if err != nil {
		t.Fatalf("reopen index after confirm: %v", err)
	}
	confirmed, err := reopened.GetContentQuarantine("tx-quarantine", "detected.xml")
	if err != nil {
		t.Fatalf("GetContentQuarantine after confirm reopen: %v", err)
	}
	if confirmed.ConfirmedAtUs != 1716700003000000 {
		t.Fatalf("ConfirmedAtUs after reopen = %d, want 1716700003000000", confirmed.ConfirmedAtUs)
	}
	_ = reopened.Close()
}

func TestApplyReleaseQuarantineSurvivesProjectionReopen(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	s := shardForApplyTest(t, idx)
	if err := s.applyEntryCommand(quarantineRaftCommandForTest(), 77); err != nil {
		t.Fatalf("apply quarantine: %v", err)
	}
	if err := idx.Close(); err != nil {
		t.Fatalf("Close before release: %v", err)
	}

	reopened, err := index.Open(dir)
	if err != nil {
		t.Fatalf("reopen index before release: %v", err)
	}
	s = shardForApplyTest(t, reopened)
	if err := s.applyEntryCommand(releaseQuarantineRaftCommandForTest(), 79); err != nil {
		t.Fatalf("apply release: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("Close after release: %v", err)
	}

	reopened, err = index.Open(dir)
	if err != nil {
		t.Fatalf("reopen index after release: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	if _, err := reopened.GetContentQuarantine("tx-quarantine", "detected.xml"); !errors.Is(err, index.ErrContentQuarantineNotFound) {
		t.Fatalf("GetContentQuarantine after release reopen = %v, want ErrContentQuarantineNotFound", err)
	}
}

func TestApplyQuarantineLifecycleRebuildsFreshProjectionFromRaftReplay(t *testing.T) {
	idx := openApplyTestIndex(t)
	s := shardForApplyTest(t, idx)
	entries := []raftpb.Entry{
		raftEntryForCommand(t, 77, quarantineRaftCommandForTest()),
		raftEntryForCommand(t, 78, confirmQuarantineRaftCommandForTest()),
	}

	if err := s.applyEntries(entries, 0); err != nil {
		t.Fatalf("applyEntries confirm: %v", err)
	}
	confirmed, err := idx.GetContentQuarantine("tx-quarantine", "detected.xml")
	if err != nil {
		t.Fatalf("GetContentQuarantine after confirm replay: %v", err)
	}
	if confirmed.ConfirmedAtUs != 1716700003000000 {
		t.Fatalf("ConfirmedAtUs after confirm replay = %d, want 1716700003000000", confirmed.ConfirmedAtUs)
	}

	releaseEntry := raftEntryForCommand(t, 79, releaseQuarantineRaftCommandForTest())
	if err := s.applyEntries([]raftpb.Entry{releaseEntry}, 0); err != nil {
		t.Fatalf("applyEntries release: %v", err)
	}
	if _, err := idx.GetContentQuarantine("tx-quarantine", "detected.xml"); !errors.Is(err, index.ErrContentQuarantineNotFound) {
		t.Fatalf("GetContentQuarantine after release replay = %v, want ErrContentQuarantineNotFound", err)
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

func TestShardInspectAndListContentQuarantine(t *testing.T) {
	idx := openApplyTestIndex(t)
	s := shardForApplyTest(t, idx)
	if err := s.applyEntryCommand(quarantineRaftCommandForTest(), 77); err != nil {
		t.Fatalf("apply quarantine: %v", err)
	}

	identity := quarantine.Identity{TransactionID: "tx-quarantine", DocumentName: "detected.xml"}
	got, err := s.InspectContentQuarantine(context.Background(), identity)
	if err != nil {
		t.Fatalf("InspectContentQuarantine: %v", err)
	}
	if got.TransactionID != identity.TransactionID || got.DocumentName != identity.DocumentName || got.Lifecycle != quarantine.LifecycleActive {
		t.Fatalf("inspect result = %+v", got)
	}
	list, err := s.ListContentQuarantines(context.Background(), quarantine.ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListContentQuarantines: %v", err)
	}
	if len(list) != 1 || list[0].DocumentName != identity.DocumentName {
		t.Fatalf("list result = %+v", list)
	}
}

func TestApplyConfirmAndReleaseContentQuarantine(t *testing.T) {
	idx := openApplyTestIndex(t)
	s := shardForApplyTest(t, idx)
	if err := s.applyEntryCommand(quarantineRaftCommandForTest(), 77); err != nil {
		t.Fatalf("apply quarantine: %v", err)
	}
	identity := quarantine.Identity{TransactionID: "tx-quarantine", DocumentName: "detected.xml"}
	if err := s.applyEntryCommand(confirmQuarantineRaftCommandForTest(), 78); err != nil {
		t.Fatalf("apply confirm: %v", err)
	}
	confirmed, err := s.InspectContentQuarantine(context.Background(), identity)
	if err != nil {
		t.Fatalf("InspectContentQuarantine confirmed: %v", err)
	}
	if confirmed.Lifecycle != quarantine.LifecycleConfirmed || confirmed.ConfirmedAt == nil {
		t.Fatalf("confirmed inspect result = %+v", confirmed)
	}

	if err := s.applyEntryCommand(releaseQuarantineRaftCommandForTest(), 79); err != nil {
		t.Fatalf("apply release: %v", err)
	}
	if _, err := s.InspectContentQuarantine(context.Background(), identity); !errors.Is(err, quarantine.ErrNotFound) {
		t.Fatalf("InspectContentQuarantine after release = %v, want ErrNotFound", err)
	}
}

// H-06 / ADR 0025: a second release (or confirm after release) must not fail
// apply — Raft replay of concurrent lifecycle commands must be deterministic.
func TestApplyReleaseQuarantineMissingIsIdempotent(t *testing.T) {
	idx := openApplyTestIndex(t)
	s := shardForApplyTest(t, idx)
	if err := s.applyEntryCommand(quarantineRaftCommandForTest(), 77); err != nil {
		t.Fatalf("apply quarantine: %v", err)
	}
	if err := s.applyEntryCommand(releaseQuarantineRaftCommandForTest(), 78); err != nil {
		t.Fatalf("apply first release: %v", err)
	}
	if err := s.applyEntryCommand(releaseQuarantineRaftCommandForTest(), 79); err != nil {
		t.Fatalf("apply second release = %v, want nil", err)
	}
}

func TestApplyConfirmQuarantineAfterReleaseIsIdempotent(t *testing.T) {
	idx := openApplyTestIndex(t)
	s := shardForApplyTest(t, idx)
	if err := s.applyEntryCommand(quarantineRaftCommandForTest(), 77); err != nil {
		t.Fatalf("apply quarantine: %v", err)
	}
	if err := s.applyEntryCommand(releaseQuarantineRaftCommandForTest(), 78); err != nil {
		t.Fatalf("apply release: %v", err)
	}
	if err := s.applyEntryCommand(confirmQuarantineRaftCommandForTest(), 79); err != nil {
		t.Fatalf("apply confirm after release = %v, want nil", err)
	}
}

func TestShardConfirmContentQuarantineWaitsForApply(t *testing.T) {
	raft := &quarantineProposalRaft{proposedCh: make(chan []byte, 1)}
	idx := openApplyTestIndex(t)
	s := shardForApplyTest(t, idx)
	s.raft = raft
	if err := s.applyEntryCommand(quarantineRaftCommandForTest(), 77); err != nil {
		t.Fatalf("apply quarantine: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		result, err := s.ConfirmContentQuarantine(context.Background(), quarantine.Identity{
			TransactionID: "tx-quarantine",
			DocumentName:  "detected.xml",
		})
		if result.Status != quarantine.StatusOK || !result.Changed {
			done <- errors.New("confirm result was not ok/changed")
			return
		}
		done <- err
	}()

	data := waitQuarantineProposal(t, raft, "ConfirmContentQuarantine")
	assertQuarantineOperationPending(t, done, "ConfirmContentQuarantine")

	applyQuarantineProposal(t, s, data)
	waitQuarantineOperationDone(t, done, "ConfirmContentQuarantine")
}

func TestShardConfirmContentQuarantineFailureReportsActiveRecord(t *testing.T) {
	raft := &quarantineProposalRaft{err: storeapi.ErrUnavailable}
	idx := openApplyTestIndex(t)
	s := shardForApplyTest(t, idx)
	s.raft = raft
	if err := s.applyEntryCommand(quarantineRaftCommandForTest(), 77); err != nil {
		t.Fatalf("apply quarantine: %v", err)
	}

	result, err := s.ConfirmContentQuarantine(context.Background(), quarantine.Identity{
		TransactionID: "tx-quarantine",
		DocumentName:  "detected.xml",
	})
	if !errors.Is(err, storeapi.ErrUnavailable) {
		t.Fatalf("ConfirmContentQuarantine error = %v, want ErrUnavailable", err)
	}
	if result.Status != quarantine.StatusFailed || result.Document == nil {
		t.Fatalf("confirm result = %+v, want failed with active document", result)
	}
	if result.Document.Lifecycle != quarantine.LifecycleActive || result.Document.ConfirmedAt != nil {
		t.Fatalf("confirm failure document = %+v, want active/unconfirmed", result.Document)
	}
}

func TestShardConfirmContentQuarantineAlreadyConfirmedIsIdempotent(t *testing.T) {
	raft := &quarantineProposalRaft{}
	idx := openApplyTestIndex(t)
	s := shardForApplyTest(t, idx)
	s.raft = raft
	if err := s.applyEntryCommand(quarantineRaftCommandForTest(), 77); err != nil {
		t.Fatalf("apply quarantine: %v", err)
	}
	if err := s.applyEntryCommand(confirmQuarantineRaftCommandForTest(), 78); err != nil {
		t.Fatalf("apply confirm: %v", err)
	}

	result, err := s.ConfirmContentQuarantine(context.Background(), quarantine.Identity{
		TransactionID: "tx-quarantine",
		DocumentName:  "detected.xml",
	})
	if err != nil {
		t.Fatalf("ConfirmContentQuarantine already confirmed: %v", err)
	}
	if result.Status != quarantine.StatusOK || result.Changed {
		t.Fatalf("confirm result = %+v, want ok unchanged", result)
	}
	if result.Document == nil || result.Document.Lifecycle != quarantine.LifecycleConfirmed {
		t.Fatalf("confirm document = %+v, want confirmed", result.Document)
	}
	if len(raft.proposed) != 0 {
		t.Fatalf("proposed commands = %d, want 0", len(raft.proposed))
	}
}

func TestShardReleaseContentQuarantineResultMarksReleased(t *testing.T) {
	raft := &quarantineProposalRaft{}
	idx := openApplyTestIndex(t)
	s := shardForApplyTest(t, idx)
	s.raft = raft
	raft.onPropose = func(data []byte) {
		applyQuarantineProposal(t, s, data)
	}
	if err := s.applyEntryCommand(quarantineRaftCommandForTest(), 77); err != nil {
		t.Fatalf("apply quarantine: %v", err)
	}

	result, err := s.ReleaseContentQuarantine(context.Background(), quarantine.Identity{
		TransactionID: "tx-quarantine",
		DocumentName:  "detected.xml",
	})
	if err != nil {
		t.Fatalf("ReleaseContentQuarantine: %v", err)
	}
	if result.Status != quarantine.StatusOK || !result.Changed {
		t.Fatalf("release result = %+v, want ok changed", result)
	}
	if result.Document == nil || result.Document.Lifecycle != quarantine.LifecycleReleased {
		t.Fatalf("release document = %+v, want released", result.Document)
	}
}

func TestShardReleaseKeepsReadGateClosedUntilApply(t *testing.T) {
	raft := &quarantineProposalRaft{proposedCh: make(chan []byte, 1)}
	idx := openApplyTestIndex(t)
	s := shardForApplyTest(t, idx)
	s.raft = raft
	if err := s.applyEntryCommand(quarantineRaftCommandForTest(), 77); err != nil {
		t.Fatalf("apply quarantine: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		result, err := s.ReleaseContentQuarantine(context.Background(), quarantine.Identity{
			TransactionID: "tx-quarantine",
			DocumentName:  "detected.xml",
		})
		if result.Status != quarantine.StatusOK || !result.Changed {
			done <- errors.New("release result was not ok/changed")
			return
		}
		done <- err
	}()

	data := waitQuarantineProposal(t, raft, "ReleaseContentQuarantine")
	s.mu.Lock()
	readErr := s.ensureContentReadAllowedLocked("tx-quarantine", "detected.xml")
	s.mu.Unlock()
	if !errors.Is(readErr, storeapi.ErrFailedPrecondition) {
		t.Fatalf("read gate before release apply = %v, want ErrFailedPrecondition", readErr)
	}

	applyQuarantineProposal(t, s, data)
	waitQuarantineOperationDone(t, done, "ReleaseContentQuarantine")
	s.mu.Lock()
	readErr = s.ensureContentReadAllowedLocked("tx-quarantine", "detected.xml")
	s.mu.Unlock()
	if readErr != nil {
		t.Fatalf("read gate after release apply = %v, want nil", readErr)
	}
}

func waitQuarantineProposal(t *testing.T, raft *quarantineProposalRaft, operation string) []byte {
	t.Helper()
	select {
	case data := <-raft.proposedCh:
		return data
	case <-time.After(time.Second):
		t.Fatalf("%s did not propose command", operation)
		return nil
	}
}

func assertQuarantineOperationPending(t *testing.T, done <-chan error, operation string) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("%s completed before apply: %v", operation, err)
	default:
	}
}

func waitQuarantineOperationDone(t *testing.T, done <-chan error, operation string) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s after apply: %v", operation, err)
		}
	case <-time.After(time.Second):
		t.Fatalf("%s did not complete after apply", operation)
	}
}

func TestApplySpanInfoForQuarantineDocumentRedactsIdentity(t *testing.T) {
	assertApplySpan(t, quarantineRaftCommandForTest(), "quarantine_document", []string{
		"scrap.transaction.hash",
		"scrap.document.hash",
		"scrap.block_id",
	})
}

func TestApplySpanInfoForQuarantineLifecycleRedactsIdentity(t *testing.T) {
	assertApplySpan(t, confirmQuarantineRaftCommandForTest(), "confirm_quarantine", []string{
		"scrap.transaction.hash",
		"scrap.document.hash",
	})
	assertApplySpan(t, releaseQuarantineRaftCommandForTest(), "release_quarantine", []string{
		"scrap.transaction.hash",
		"scrap.document.hash",
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

func confirmQuarantineRaftCommandForTest() *scrapv1.RaftCommand {
	return &scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_ConfirmQuarantine{
			ConfirmQuarantine: &scrapv1.ConfirmQuarantine{
				TransactionId: "tx-quarantine",
				DocumentName:  "detected.xml",
				ConfirmedAtUs: 1716700003000000,
				ProposalId:    "proposal-confirm",
			},
		},
	}
}

func releaseQuarantineRaftCommandForTest() *scrapv1.RaftCommand {
	return &scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_ReleaseQuarantine{
			ReleaseQuarantine: &scrapv1.ReleaseQuarantine{
				TransactionId: "tx-quarantine",
				DocumentName:  "detected.xml",
				ReleasedAtUs:  1716700004000000,
				ProposalId:    "proposal-release",
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
	err        error
}

func (r *quarantineProposalRaft) Propose(_ context.Context, data []byte) error {
	if r.err != nil {
		return r.err
	}
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

func raftEntryForCommand(t *testing.T, index uint64, cmd *scrapv1.RaftCommand) raftpb.Entry {
	t.Helper()
	data, err := proto.Marshal(cmd)
	if err != nil {
		t.Fatalf("marshal command: %v", err)
	}
	return raftpb.Entry{
		Type:  raftpb.EntryNormal,
		Index: index,
		Data:  data,
	}
}

func (r *quarantineProposalRaft) ReadIndex(context.Context) (uint64, error) { return 0, nil }

func (r *quarantineProposalRaft) Step(context.Context, raftpb.Message) error { return nil }

func (r *quarantineProposalRaft) IsLeader() bool { return true }

func (r *quarantineProposalRaft) LeaderID() uint64 { return 1 }

func (r *quarantineProposalRaft) Term() uint64 { return 1 }

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
