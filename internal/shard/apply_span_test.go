package shard

import (
	"math"
	"testing"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/telemetry"
)

func assertApplySpan(t *testing.T, cmd *scrapv1.RaftCommand, wantOp string, wantKeys []string) {
	t.Helper()
	op, attrs := applySpanInfo(cmd, telemetry.HashIdentifiers)
	if op != wantOp {
		t.Fatalf("op: got %q want %q", op, wantOp)
	}
	gotKeys := make(map[string]bool, len(attrs))
	for _, a := range attrs {
		gotKeys[string(a.Key)] = true
	}
	for _, k := range wantKeys {
		if !gotKeys[k] {
			t.Fatalf("missing attr %q in %v", k, gotKeys)
		}
	}
	// Raw identifiers must never leak onto apply spans (ADR 0012: hashed by default).
	if gotKeys["scrap.transaction_id"] || gotKeys["scrap.document_name"] {
		t.Fatalf("raw identifier leaked into apply span attrs: %v", gotKeys)
	}
}

func TestApplySpanInfo(t *testing.T) {
	tests := []struct {
		name     string
		cmd      *scrapv1.RaftCommand
		wantOp   string
		wantKeys []string
	}{
		{
			name: "commit_document carries hashed identity and block_id",
			cmd: &scrapv1.RaftCommand{Command: &scrapv1.RaftCommand_CommitDoc{
				CommitDoc: &scrapv1.CommitDocument{TransactionId: "tx", DocumentName: "doc", BlockId: 42},
			}},
			wantOp:   "commit_document",
			wantKeys: []string{"scrap.transaction.hash", "scrap.document.hash", "scrap.block_id"},
		},
		{
			name: "seal_block carries block_id",
			cmd: &scrapv1.RaftCommand{Command: &scrapv1.RaftCommand_SealBlock{
				SealBlock: &scrapv1.SealBlock{BlockId: 7},
			}},
			wantOp:   "seal_block",
			wantKeys: []string{"scrap.block_id"},
		},
		{
			name: "rewrap_document carries hashed identity and block_id",
			cmd: &scrapv1.RaftCommand{Command: &scrapv1.RaftCommand_RewrapDoc{
				RewrapDoc: &scrapv1.RewrapDocumentEnvelope{TransactionId: "tx", DocumentName: "doc", BlockId: 11},
			}},
			wantOp:   "rewrap_document",
			wantKeys: []string{"scrap.transaction.hash", "scrap.document.hash", "scrap.block_id"},
		},
		{
			name: "confirm_upload carries block_id",
			cmd: &scrapv1.RaftCommand{Command: &scrapv1.RaftCommand_ConfirmUpload{
				ConfirmUpload: &scrapv1.ConfirmUpload{BlockId: 9},
			}},
			wantOp:   "confirm_upload",
			wantKeys: []string{"scrap.block_id"},
		},
		{
			name: "consistency_check has no attributes",
			cmd: &scrapv1.RaftCommand{Command: &scrapv1.RaftCommand_ConsistencyCheck{
				ConsistencyCheck: &scrapv1.RequestConsistencyCheck{ScrubId: "s"},
			}},
			wantOp: "consistency_check",
		},
		{
			name:   "empty command yields no span",
			cmd:    &scrapv1.RaftCommand{},
			wantOp: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertApplySpan(t, tt.cmd, tt.wantOp, tt.wantKeys)
		})
	}
}

func TestApplySpanInfoRawModeForLocalDebug(t *testing.T) {
	cmd := &scrapv1.RaftCommand{Command: &scrapv1.RaftCommand_CommitDoc{
		CommitDoc: &scrapv1.CommitDocument{TransactionId: "tx", DocumentName: "doc", BlockId: 42},
	}}
	op, attrs := applySpanInfo(cmd, telemetry.RawIdentifiersForLocalDebug)
	if op != "commit_document" {
		t.Fatalf("op: got %q want commit_document", op)
	}
	gotKeys := make(map[string]bool, len(attrs))
	for _, a := range attrs {
		gotKeys[string(a.Key)] = true
	}
	// In the local-debug mode the apply span carries raw identifiers, not hashes.
	for _, k := range []string{"scrap.transaction_id", "scrap.document_name", "scrap.block_id"} {
		if !gotKeys[k] {
			t.Fatalf("missing attr %q in raw mode: %v", k, gotKeys)
		}
	}
	if gotKeys["scrap.transaction.hash"] || gotKeys["scrap.document.hash"] {
		t.Fatalf("hashed identifier present in raw mode: %v", gotKeys)
	}
}

func TestBlockIDAttributeClampsOverflow(t *testing.T) {
	overflow := blockIDAttribute(uint64(math.MaxInt64) + 5)
	if overflow.Value.AsInt64() != math.MaxInt64 {
		t.Fatalf("expected clamp to MaxInt64, got %d", overflow.Value.AsInt64())
	}
	if string(overflow.Key) != "scrap.block_id" {
		t.Fatalf("key: got %q want scrap.block_id", overflow.Key)
	}
	normal := blockIDAttribute(42)
	if normal.Value.AsInt64() != 42 {
		t.Fatalf("expected 42, got %d", normal.Value.AsInt64())
	}
}
