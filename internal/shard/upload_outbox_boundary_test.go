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
