package localstorage

import (
	"context"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/petabytecl/scrap/internal/blockstore"
	"github.com/petabytecl/scrap/internal/metastore"
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
	testutil.RequireEqualf(t, len(summary.GetStorageMembers()), 1, "cluster storage members")
	testutil.RequireEqualf(t, summary.GetStorageMembers()[0].GetStorageMemberId(), "local", "cluster storage member id")
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

func TestAdminInspectReportsConfiguredRuntimeIdentity(t *testing.T) {
	ctx := context.Background()
	app, err := OpenWithOptions(ctx, t.TempDir(), OpenOptions{
		CellID:   "scrap-cell-a",
		MemberID: "scrapd-0",
	})
	testutil.RequireNoErrorf(t, err, "open app with runtime identity")
	t.Cleanup(func() {
		testutil.RequireNoErrorf(t, app.Close(), "close app")
	})

	testutil.RequireEqualf(t, app.CellID(), "scrap-cell-a", "application cell id")
	testutil.RequireEqualf(t, app.MemberID(), "scrapd-0", "application member id")

	member, err := Inspect(app).GetAdminMember(ctx, "scrapd-0")
	testutil.RequireNoErrorf(t, err, "configured admin member")
	testutil.RequireEqualf(t, member.GetStorageMemberId(), "scrapd-0", "admin member id")
	testutil.RequireEqualf(t, member.GetCellId(), "scrap-cell-a", "admin cell id")
	if _, err := Inspect(app).GetAdminMember(ctx, "local"); err == nil {
		t.Fatal("legacy local member lookup error = nil, want not found for configured identity")
	}

	shard, err := Inspect(app).GetAdminShard(ctx, "local")
	testutil.RequireNoErrorf(t, err, "configured admin shard")
	testutil.RequireEqualf(t, shard.GetLeaderMemberId(), "scrapd-0", "admin shard leader")
	testutil.RequireEqualf(t, shard.GetVoterMemberIds()[0], "scrapd-0", "admin shard voter")

	cordoned, err := Members(app).CordonMember(ctx, storageapp.MemberMutationRequest{StorageMember: "scrapd-0"})
	testutil.RequireNoErrorf(t, err, "cordon configured member")
	testutil.RequireTruef(t, cordoned.GetCordoned(), "configured member was not cordoned")
	if _, err := Members(app).UncordonMember(ctx, storageapp.MemberMutationRequest{StorageMember: "local"}); err == nil {
		t.Fatal("legacy local uncordon error = nil, want not found for configured identity")
	}

	safety, err := Members(app).GetEvictionSafety(ctx, "scrapd-0")
	testutil.RequireNoErrorf(t, err, "configured eviction safety")
	testutil.RequireEqualf(t, safety.GetStorageMember().GetStorageMemberId(), "scrapd-0", "eviction safety member")
}

func TestConfiguredMetadataAuthorityReportsVotersAndRejectsFollowerWrites(t *testing.T) {
	ctx := context.Background()
	memberIDs := []string{"scrapd-0", "scrapd-1", "scrapd-2"}
	leader, err := OpenWithOptions(ctx, t.TempDir(), OpenOptions{
		CellID:                    "scrap-cell-a",
		MemberID:                  "scrapd-0",
		AuthorityMemberIDs:        memberIDs,
		MetadataAuthorityMemberID: "scrapd-0",
	})
	testutil.RequireNoErrorf(t, err, "open leader")
	t.Cleanup(func() {
		testutil.RequireNoErrorf(t, leader.Close(), "close leader")
	})
	follower, err := OpenWithOptions(ctx, t.TempDir(), OpenOptions{
		CellID:                    "scrap-cell-a",
		MemberID:                  "scrapd-1",
		AuthorityMemberIDs:        memberIDs,
		MetadataAuthorityMemberID: "scrapd-0",
	})
	testutil.RequireNoErrorf(t, err, "open follower")
	t.Cleanup(func() {
		testutil.RequireNoErrorf(t, follower.Close(), "close follower")
	})

	leaderShard, err := Inspect(leader).GetAdminShard(ctx, "local")
	testutil.RequireNoErrorf(t, err, "leader shard")
	testutil.RequireEqualf(t, leaderShard.GetLeaderMemberId(), "scrapd-0", "leader member id")
	testutil.RequireDeepEqualf(t, leaderShard.GetVoterMemberIds(), memberIDs, "leader voters")
	followerShard, err := Inspect(follower).GetAdminShard(ctx, "local")
	testutil.RequireNoErrorf(t, err, "follower shard")
	testutil.RequireEqualf(t, followerShard.GetLeaderMemberId(), "scrapd-0", "follower leader member id")
	testutil.RequireDeepEqualf(t, followerShard.GetVoterMemberIds(), memberIDs, "follower voters")
	testutil.RequireNoErrorf(t, Health(follower).CheckReadiness(ctx), "follower readiness")

	doc := testDocumentIdentity()
	doc.DocumentName = "authority-owned.xml"
	_, err = leader.WriteDocument(ctx, storageapp.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{[]byte("leader accepts the write")}))
	testutil.RequireNoErrorf(t, err, "leader write")

	followerDoc := testDocumentIdentity()
	followerDoc.DocumentName = "follower-rejected.xml"
	_, err = follower.WriteDocument(ctx, storageapp.WriteDocumentInit{
		Identity:         followerDoc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{[]byte("follower must not append")}))
	requireCode(t, err, codes.Unavailable)
	_, err = follower.HeadDocument(ctx, storageapp.HeadDocumentRequest{Identity: doc})
	requireCode(t, err, codes.FailedPrecondition)
	testutil.RequireEqualf(t, follower.blocks.CurrentBlockLength(), blockstore.HeaderLength, "follower block length")
	if _, headErr := follower.metadata.HeadDocument(followerDoc); !errors.Is(headErr, metastore.ErrNotFound) {
		t.Fatalf("follower metadata after rejected write = %v, want %v", headErr, metastore.ErrNotFound)
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
