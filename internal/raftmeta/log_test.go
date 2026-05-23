package raftmeta

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	metastorev1 "github.com/petabytecl/scrap/internal/gen/scrap/metastore/v1"
	"github.com/petabytecl/scrap/internal/metastore"
)

func TestLogAppendReplayAndContinueAfterReopen(t *testing.T) {
	dir := t.TempDir()
	log, err := OpenLog(dir)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	first, err := log.Append(sampleCommand("cmd-1", "invoice-1.xml"))
	if err != nil {
		t.Fatalf("append first: %v", err)
	}
	second, err := log.Append(sampleCommand("cmd-2", "invoice-2.xml"))
	if err != nil {
		t.Fatalf("append second: %v", err)
	}
	if first.Index != 1 || second.Index != 2 {
		t.Fatalf("indexes = %d, %d, want 1, 2", first.Index, second.Index)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("close log: %v", err)
	}

	reopened, err := OpenLog(dir)
	if err != nil {
		t.Fatalf("reopen log: %v", err)
	}
	defer reopened.Close()
	entries, err := reopened.Replay()
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(entries) != 2 || entries[0].Command.GetCommandId() != "cmd-1" || entries[1].Command.GetCommandId() != "cmd-2" {
		t.Fatalf("entries = %#v, want two replayed commands", entries)
	}
	third, err := reopened.Append(sampleCommand("cmd-3", "invoice-3.xml"))
	if err != nil {
		t.Fatalf("append third: %v", err)
	}
	if third.Index != 3 {
		t.Fatalf("third index = %d, want 3", third.Index)
	}
}

func TestLogRejectsUnsupportedCommandVersion(t *testing.T) {
	log, err := OpenLog(t.TempDir())
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer log.Close()

	command := sampleCommand("cmd-1", "invoice.xml")
	command.SchemaVersion = metastore.CurrentSchemaVersion + 1
	if _, err := log.Append(command); err == nil {
		t.Fatal("expected unsupported command version error")
	}
	entries, err := log.Replay()
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %#v, want no append after rejected command", entries)
	}
}

func TestLogRejectsCorruptRecord(t *testing.T) {
	dir := t.TempDir()
	log, err := OpenLog(dir)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	if _, err := log.Append(sampleCommand("cmd-1", "invoice.xml")); err != nil {
		t.Fatalf("append command: %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("close log: %v", err)
	}

	path := filepath.Join(dir, commandLogName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	corrupted := bytes.Clone(data)
	corrupted[len(corrupted)-1] ^= 0xff
	if err := os.WriteFile(path, corrupted, 0o600); err != nil {
		t.Fatalf("write corrupt log: %v", err)
	}
	if _, err := OpenLog(dir); err == nil {
		t.Fatal("opened corrupt command log")
	}
}

func sampleCommand(commandID, documentName string) *metastorev1.ShardCommand {
	return &metastorev1.ShardCommand{
		SchemaVersion: metastore.CurrentSchemaVersion,
		ShardId:       "tenant-txn",
		CommandId:     commandID,
		ProposedAt:    timestamppb.New(time.Unix(100, 0).UTC()),
		Command: &metastorev1.ShardCommand_CommitDocument{
			CommitDocument: &metastorev1.CommitDocumentCommand{
				Document: &metastorev1.DocumentRecord{
					SchemaVersion: metastore.CurrentSchemaVersion,
					TenantId:      "tenant",
					TransactionId: "txn",
					DocumentName:  documentName,
					DocumentClass: uint32(metastore.DocumentClassPermanent),
					PriorityClass: uint32(metastore.PriorityClassNormal),
					Length:        42,
					LogicalSha256: bytes.Repeat([]byte{1}, 32),
					StoredSha256:  bytes.Repeat([]byte{2}, 32),
					DocumentIdentityFingerprint: bytes.Repeat(
						[]byte{4},
						16,
					),
					CreatedByService: "billing-etl",
					CreatedAt:        timestamppb.New(time.Unix(100, 0).UTC()),
					FinalizedAt:      timestamppb.New(time.Unix(100, 0).UTC()),
					Availability:     uint32(metastore.AvailabilityHot),
					LifecycleState:   uint32(metastore.LifecycleStateActive),
					RestoreState:     metastorev1.RestoreState_RESTORE_STATE_HOT,
					UploadState:      metastorev1.UploadState_UPLOAD_STATE_PENDING,
					Location: &metastorev1.Location{
						BlockId:      "block-1",
						StoredOffset: 64,
						StoredLength: 42,
						Frames: []*metastorev1.FrameRecord{
							{
								FrameOffset:   64,
								SegmentOffset: 64,
								SegmentLength: 42,
								Sha256:        bytes.Repeat([]byte{3}, 32),
							},
						},
					},
				},
			},
		},
	}
}
