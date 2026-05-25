package localstorage

import (
	"context"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/petabytecl/scrap/internal/storageapp"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestAdminInspectReportsLocalShardDocumentBlockAndCapacity(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	doc := testDocumentIdentity()
	stored := writeOperationTargetDocument(t, app, doc, "admin inspect bytes")

	summary, err := Inspect(app).GetAdminClusterSummary(ctx)
	testutil.RequireNoErrorf(t, err, "cluster summary")
	testutil.RequireEqualf(t, summary.GetShardCount(), uint32(1), "cluster shard count")
	shard, err := Inspect(app).GetAdminShard(ctx, "local")
	testutil.RequireNoErrorf(t, err, "local shard")
	testutil.RequireEqualf(t, shard.GetLeaderMemberId(), "local", "local shard leader")
	if _, err := Inspect(app).GetAdminShard(ctx, "remote"); err == nil {
		t.Fatal("remote shard error = nil, want not found")
	}

	adminDoc, err := Inspect(app).GetAdminDocument(ctx, doc)
	testutil.RequireNoErrorf(t, err, "admin document")
	testutil.RequireEqualf(t, adminDoc.GetDocument().GetDocumentName(), doc.DocumentName, "admin document name")
	block, err := Inspect(app).GetAdminBlock(ctx, storageapp.BlockTarget{ShardID: "local", BlockID: stored.Location.BlockID})
	testutil.RequireNoErrorf(t, err, "admin block")
	testutil.RequireEqualf(t, block.GetBlockId(), stored.Location.BlockID, "admin block id")
	if _, err := Inspect(app).GetAdminBlock(ctx, storageapp.BlockTarget{ShardID: "remote", BlockID: stored.Location.BlockID}); err == nil {
		t.Fatal("remote block error = nil, want not found")
	}

	runway, err := Inspect(app).GetAdminCapacityRunway(ctx, "")
	testutil.RequireNoErrorf(t, err, "default capacity runway")
	testutil.RequireEqualf(t, runway.GetCapacityProfileId(), localCapacityProfileID, "capacity profile id")
	if _, err := Inspect(app).GetAdminCapacityRunway(ctx, "remote"); err == nil {
		t.Fatal("remote capacity profile error = nil, want not found")
	}
}

func TestDiskStatsHelpersHandleEdgeCases(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.bin"), []byte("1234"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	size, err := directorySize(dir)
	testutil.RequireNoErrorf(t, err, "directory size")
	testutil.RequireEqualf(t, size, uint64(4), "directory size")

	entry, err := os.ReadDir(dir)
	testutil.RequireNoErrorf(t, err, "read dir")
	fileSize, err := entrySize(entry[0], nil)
	testutil.RequireNoErrorf(t, err, "entry size")
	testutil.RequireEqualf(t, fileSize, int64(4), "file entry size")
	dirEntry, err := os.ReadDir(filepath.Dir(dir))
	testutil.RequireNoErrorf(t, err, "read parent dir")
	for _, candidate := range dirEntry {
		if candidate.Name() == filepath.Base(dir) {
			dirSize, err := entrySize(candidate, nil)
			testutil.RequireNoErrorf(t, err, "directory entry size")
			testutil.RequireEqualf(t, dirSize, int64(0), "directory entry size")
			break
		}
	}
	_, err = entrySize(nil, fs.ErrNotExist)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("entry error = %v, want %v", err, fs.ErrNotExist)
	}
	testutil.RequireEqualf(t, statfsBytes(10, -1), uint64(0), "negative block size")
	testutil.RequireEqualf(t, statfsBytes(math.MaxUint64, 2), uint64(math.MaxUint64), "overflow statfs bytes")
}
