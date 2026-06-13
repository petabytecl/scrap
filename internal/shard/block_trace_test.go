package shard

import (
	"testing"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
)

func TestBlockTraceContextDeterministicAndUnique(t *testing.T) {
	a := blockTraceContext("cell-1", 7, 42)
	b := blockTraceContext("cell-1", 7, 42)
	if !a.Equal(b) {
		t.Fatal("blockTraceContext must be deterministic for the same inputs")
	}
	if !a.IsValid() {
		t.Fatal("block trace context must be valid (non-zero trace and span id)")
	}
	if !a.IsSampled() {
		t.Fatal("block trace context must be sampled so trace 2 is recorded")
	}
	if !a.IsRemote() {
		t.Fatal("block trace context must be remote (it seeds a synthetic parent)")
	}

	// A different block, shard, or cell must yield a different trace id, or the
	// forward links from unrelated writes would collide into one upload trace.
	if a.TraceID() == blockTraceContext("cell-1", 7, 43).TraceID() {
		t.Fatal("different block id must change the trace id")
	}
	if a.TraceID() == blockTraceContext("cell-1", 8, 42).TraceID() {
		t.Fatal("different shard id must change the trace id")
	}
	if a.TraceID() == blockTraceContext("cell-2", 7, 42).TraceID() {
		t.Fatal("different cell id must change the trace id")
	}
}

func TestUploadCommandBlockID(t *testing.T) {
	seal := &scrapv1.RaftCommand{Command: &scrapv1.RaftCommand_SealBlock{
		SealBlock: &scrapv1.SealBlock{BlockId: 7},
	}}
	if id, ok := uploadCommandBlockID(seal); !ok || id != 7 {
		t.Fatalf("seal: got (%d,%v) want (7,true)", id, ok)
	}

	confirm := &scrapv1.RaftCommand{Command: &scrapv1.RaftCommand_ConfirmUpload{
		ConfirmUpload: &scrapv1.ConfirmUpload{BlockId: 9},
	}}
	if id, ok := uploadCommandBlockID(confirm); !ok || id != 9 {
		t.Fatalf("confirm: got (%d,%v) want (9,true)", id, ok)
	}

	commit := &scrapv1.RaftCommand{Command: &scrapv1.RaftCommand_CommitDoc{
		CommitDoc: &scrapv1.CommitDocument{BlockId: 5},
	}}
	if _, ok := uploadCommandBlockID(commit); ok {
		t.Fatal("commit_document is not an upload command")
	}
}
