package block_test

import (
	"io"
	"os"
	"testing"

	"github.com/petabytecl/scrap/internal/block"
)

// M-01: ReadDocumentTwoPass must fully verify before returning a reader, and
// mutating the Block after the reader is obtained must not affect served bytes.
func TestReadDocumentTwoPassServesImmutableVerifiedSnapshot(t *testing.T) {
	dir := t.TempDir()
	data := []byte("immutable-spool-payload")
	blkPath, _, entry := writeSingleDocBlock(t, dir, data)

	rc, err := block.ReadDocumentTwoPass(blkPath, entry)
	if err != nil {
		t.Fatalf("ReadDocumentTwoPass: %v", err)
	}
	defer func() { _ = rc.Close() }()

	if err := os.WriteFile(blkPath, []byte("corrupted-after-verify"), 0o600); err != nil {
		t.Fatalf("corrupt block after verify: %v", err)
	}

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("payload = %q, want verified snapshot %q", got, data)
	}
}
