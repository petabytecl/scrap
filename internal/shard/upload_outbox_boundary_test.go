package shard

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/index"
)

func TestUploadOutboxAppliesBlockSealedEvent(t *testing.T) {
	idx := openApplyTestIndex(t)
	outbox := newUploadOutbox(t.TempDir(), idx, newBlockUploadLifecycle())

	event := blockSealedEvent{
		BlockID:         uploadApplyTestBlockID,
		ShardID:         7,
		SealedSizeBytes: 4096,
		SealedAtUs:      1716700000000000,
	}
	if err := outbox.ApplyBlockSealed(event); err != nil {
		t.Fatalf("ApplyBlockSealed: %v", err)
	}

	got, err := idx.GetPendingUpload(uploadApplyTestBlockID)
	if err != nil {
		t.Fatalf("GetPendingUpload: %v", err)
	}
	if got != event.pendingUpload() {
		t.Fatalf("pending upload = %+v, want %+v", got, event.pendingUpload())
	}
}

func TestUploadOutboxAppliesDuplicateBlockSealedEventIdempotently(t *testing.T) {
	idx := openApplyTestIndex(t)
	lifecycle := newBlockUploadLifecycle()
	outbox := newUploadOutbox(t.TempDir(), idx, lifecycle)
	event := blockSealedEvent{
		BlockID:         uploadApplyTestBlockID,
		ShardID:         7,
		SealedSizeBytes: 4096,
		SealedAtUs:      1716700000000000,
	}

	outbox.RecordBlockSealed(event)
	if err := outbox.ApplyBlockSealed(event); err != nil {
		t.Fatalf("first ApplyBlockSealed: %v", err)
	}
	if err := outbox.ApplyBlockSealed(event); err != nil {
		t.Fatalf("duplicate ApplyBlockSealed: %v", err)
	}

	uploads, err := collectPendingUploads(idx)
	if err != nil {
		t.Fatalf("collectPendingUploads: %v", err)
	}
	if len(uploads) != 1 {
		t.Fatalf("pending uploads = %d, want 1", len(uploads))
	}
	if uploads[0] != event.pendingUpload() {
		t.Fatalf("pending upload = %+v, want %+v", uploads[0], event.pendingUpload())
	}
	controller := newUploadController(nil, UploadConfig{}, 7, nil, nil, nil)
	if err := outbox.RefreshPressure(controller); err != nil {
		t.Fatalf("RefreshPressure: %v", err)
	}
	if got := controller.snapshot(); got.PendingBlocks != 1 || got.PendingBytes != event.SealedSizeBytes {
		t.Fatalf("pressure snapshot = %+v, want one committed pending Block", got)
	}
}

func TestUploadOutboxRefreshPressureCombinesCommittedAndLocalObligations(t *testing.T) {
	idx := openApplyTestIndex(t)
	lifecycle := newBlockUploadLifecycle()
	outbox := newUploadOutbox(t.TempDir(), idx, lifecycle)

	committed := blockSealedEvent{
		BlockID:         uploadApplyTestBlockID,
		ShardID:         7,
		SealedSizeBytes: 100,
		SealedAtUs:      1716700000000000,
	}
	if err := outbox.ApplyBlockSealed(committed); err != nil {
		t.Fatalf("ApplyBlockSealed: %v", err)
	}
	outbox.RecordBlockSealed(blockSealedEvent{
		BlockID:         uploadApplyTestBlockID,
		ShardID:         7,
		SealedSizeBytes: 999,
		SealedAtUs:      1716700001000000,
	})
	outbox.RecordBlockSealed(blockSealedEvent{
		BlockID:         uploadApplyTestBlockID + 1,
		ShardID:         7,
		SealedSizeBytes: 20,
		SealedAtUs:      1716700002000000,
	})

	controller := newUploadController(nil, UploadConfig{
		Pressure: UploadPressureConfig{
			BudgetBytes: 100,
			WarnPct:     0.50,
			PressurePct: 0.75,
			CriticalPct: 0.95,
		},
	}, 7, nil, nil, nil)
	if err := outbox.RefreshPressure(controller); err != nil {
		t.Fatalf("RefreshPressure: %v", err)
	}
	got := controller.snapshot()
	if got.PendingBlocks != 2 {
		t.Fatalf("pending blocks = %d, want committed plus local obligations", got.PendingBlocks)
	}
	if got.PendingBytes != 120 {
		t.Fatalf("pending bytes = %d, want committed size plus unique local size", got.PendingBytes)
	}
	if got.Level != UploadPressureLevelCritical {
		t.Fatalf("pressure level = %s, want %s", got.Level, UploadPressureLevelCritical)
	}
}

func TestUploadOutboxAppliesUploadConfirmedEvent(t *testing.T) {
	idx := openApplyTestIndex(t)
	sealed := blockSealedEvent{
		BlockID:         uploadApplyTestBlockID,
		ShardID:         7,
		SealedSizeBytes: 67108864,
		SealedAtUs:      1716700000000000,
	}
	outbox := newUploadOutbox(t.TempDir(), idx, newBlockUploadLifecycle())
	if err := outbox.ApplyBlockSealed(sealed); err != nil {
		t.Fatalf("ApplyBlockSealed: %v", err)
	}

	confirmed, err := outbox.ApplyUploadConfirmed(uploadConfirmedEventFromUpload(confirmedUploadForApplyTest(
		1716700001000000,
		shardApplyValidationValue("block"),
		shardApplyValidationValue("index"),
	)))
	if err != nil {
		t.Fatalf("ApplyUploadConfirmed: %v", err)
	}

	if _, err := idx.GetPendingUpload(uploadApplyTestBlockID); !errors.Is(err, index.ErrPendingUploadNotFound) {
		t.Fatalf("GetPendingUpload error = %v, want ErrPendingUploadNotFound", err)
	}
	got, err := idx.GetConfirmedUpload(uploadApplyTestBlockID)
	if err != nil {
		t.Fatalf("GetConfirmedUpload: %v", err)
	}
	if got != confirmed {
		t.Fatalf("confirmed upload = %+v, want %+v", got, confirmed)
	}
}

func TestUploadOutboxAppliesDuplicateUploadConfirmedEventIdempotently(t *testing.T) {
	idx := openApplyTestIndex(t)
	outbox := newUploadOutbox(t.TempDir(), idx, newBlockUploadLifecycle())
	sealed := blockSealedEvent{
		BlockID:         uploadApplyTestBlockID,
		ShardID:         7,
		SealedSizeBytes: 67108864,
		SealedAtUs:      1716700000000000,
	}
	if err := outbox.ApplyBlockSealed(sealed); err != nil {
		t.Fatalf("ApplyBlockSealed: %v", err)
	}
	event := uploadConfirmedEventFromUpload(confirmedUploadForApplyTest(
		1716700001000000,
		shardApplyValidationValue("block"),
		shardApplyValidationValue("index"),
	))

	first, err := outbox.ApplyUploadConfirmed(event)
	if err != nil {
		t.Fatalf("first ApplyUploadConfirmed: %v", err)
	}
	second, err := outbox.ApplyUploadConfirmed(event)
	if err != nil {
		t.Fatalf("duplicate ApplyUploadConfirmed: %v", err)
	}
	if second != first {
		t.Fatalf("duplicate confirmed upload = %+v, want %+v", second, first)
	}
	if _, err := idx.GetPendingUpload(uploadApplyTestBlockID); !errors.Is(err, index.ErrPendingUploadNotFound) {
		t.Fatalf("GetPendingUpload error = %v, want ErrPendingUploadNotFound", err)
	}
}

func TestUploadOutboxRejectsStaleUploadConfirmedGeneration(t *testing.T) {
	idx := openApplyTestIndex(t)
	if err := idx.PutPendingUpload(index.PendingUpload{
		BlockID:          uploadApplyTestBlockID,
		ShardID:          7,
		SealedSizeBytes:  67108864,
		SealedAtUs:       1716700000000000,
		UploadGeneration: 1716700002000000,
	}); err != nil {
		t.Fatalf("PutPendingUpload: %v", err)
	}
	stale := confirmedUploadForApplyTest(
		1716700001000000,
		shardApplyValidationValue("block"),
		shardApplyValidationValue("index"),
	)
	stale.UploadGeneration = 1716700001000000
	outbox := newUploadOutbox(t.TempDir(), idx, newBlockUploadLifecycle())

	_, err := outbox.ApplyUploadConfirmed(uploadConfirmedEventFromUpload(stale))
	if !errors.Is(err, errConfirmUploadGenerationMismatch) {
		t.Fatalf("ApplyUploadConfirmed error = %v, want generation mismatch", err)
	}
	pending, err := idx.GetPendingUpload(uploadApplyTestBlockID)
	if err != nil {
		t.Fatalf("GetPendingUpload: %v", err)
	}
	if pending.UploadGeneration != 1716700002000000 {
		t.Fatalf("pending generation = %d, want 1716700002000000", pending.UploadGeneration)
	}
	if _, err := idx.GetConfirmedUpload(uploadApplyTestBlockID); !errors.Is(err, index.ErrConfirmedUploadNotFound) {
		t.Fatalf("GetConfirmedUpload error = %v, want ErrConfirmedUploadNotFound", err)
	}
}

func TestUploadOutboxProposesUploadConfirmedEvent(t *testing.T) {
	proposer := &capturingUploadProposer{}
	event := uploadConfirmedEventFromUpload(confirmedUploadForApplyTest(
		1716700001000000,
		shardApplyValidationValue("block"),
		shardApplyValidationValue("index"),
	))

	if err := proposeUploadConfirmedEvent(context.Background(), proposer, "cell-a", 7, event); err != nil {
		t.Fatalf("proposeUploadConfirmedEvent: %v", err)
	}

	confirm := proposer.confirmUpload(t)
	if confirm.GetBlockId() != event.BlockID {
		t.Fatalf("confirmed BlockID = %d, want %d", confirm.GetBlockId(), event.BlockID)
	}
	if confirm.GetUploadGeneration() != event.UploadGeneration {
		t.Fatalf("confirmed generation = %d, want %d", confirm.GetUploadGeneration(), event.UploadGeneration)
	}
	if confirm.GetBlockObject().GetKey() != event.BlockObject.Key {
		t.Fatalf("confirmed block key = %q, want %q", confirm.GetBlockObject().GetKey(), event.BlockObject.Key)
	}
}

type capturingUploadProposer struct {
	data [][]byte
}

func (p *capturingUploadProposer) Propose(_ context.Context, data []byte) error {
	p.data = append(p.data, append([]byte(nil), data...))
	return nil
}

func (p *capturingUploadProposer) confirmUpload(t *testing.T) *scrapv1.ConfirmUpload {
	t.Helper()

	if len(p.data) != 1 {
		t.Fatalf("proposal count = %d, want 1", len(p.data))
	}
	var cmd scrapv1.RaftCommand
	if err := proto.Unmarshal(p.data[0], &cmd); err != nil {
		t.Fatalf("unmarshal proposal: %v", err)
	}
	confirm := cmd.GetConfirmUpload()
	if confirm == nil {
		t.Fatal("proposal did not contain ConfirmUpload")
	}
	return confirm
}
