package localstorage

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"syscall"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/petabytecl/scrap/internal/closeutil"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/raftmeta"
	"github.com/petabytecl/scrap/internal/safeconv"
	"github.com/petabytecl/scrap/internal/storageapp"
)

const localCapacityProfileID = "local-non-production"

func (a *InspectApplication) GetAdminClusterSummary(ctx context.Context) (*adminv1.ClusterSummary, error) {
	stats, err := a.localDiskStats(ctx)
	if err != nil {
		return nil, err
	}
	member, err := a.localAdminMember(ctx, stats)
	if err != nil {
		return nil, err
	}
	return &adminv1.ClusterSummary{
		ShardCount:         1,
		StorageMemberCount: 1,
		LocalBytesUsed:     stats.bytesUsed,
		LocalBytesCapacity: stats.bytesCapacity,
		StorageMembers:     []*adminv1.StorageMember{member},
	}, nil
}

func (a *InspectApplication) GetAdminShard(_ context.Context, shardID string) (*adminv1.Shard, error) {
	if shardID != "local" {
		return nil, mapError(metastore.ErrNotFound)
	}
	appliedIndex := a.app.authority.AppliedIndex()
	committedIndex := a.app.authority.DurableIndex()
	voterMemberIDs := authorityVoterMemberIDs(a.app.authority.Members())
	leaderMemberID := a.app.metadataAuthorityMember
	if leaderMemberID == "" {
		leaderMemberID = a.app.MemberID()
	}
	return &adminv1.Shard{
		ShardId:              "local",
		LeaderMemberId:       leaderMemberID,
		VoterMemberIds:       voterMemberIDs,
		CommittedIndex:       committedIndex,
		AppliedIndex:         appliedIndex,
		OpenTransactionCount: 0,
	}, nil
}

func authorityVoterMemberIDs(members []raftmeta.Member) []string {
	out := make([]string, 0, len(members))
	for _, member := range members {
		if member.IsVoter() {
			out = append(out, member.MemberID)
		}
	}
	if len(out) == 0 {
		return []string{defaultLocalRuntimeID}
	}
	return out
}

func (a *InspectApplication) GetAdminDocument(_ context.Context, doc identity.Document) (*adminv1.AdminDocument, error) {
	document, err := a.app.metadata.HeadDocument(doc)
	if err != nil {
		return nil, mapError(err)
	}
	return &adminv1.AdminDocument{
		Document: &adminv1.DocumentTarget{
			TenantId:      document.Identity.TenantID,
			TransactionId: document.Identity.TransactionID,
			DocumentName:  document.Identity.DocumentName,
		},
		ShardId:        "local",
		BlockIds:       []string{document.Location.BlockID},
		Length:         document.Length,
		LogicalSha256:  append([]byte(nil), document.LogicalSHA256[:]...),
		RepairRequired: document.Availability == metastore.AvailabilityDegradedRepair,
	}, nil
}

func (a *InspectApplication) GetAdminBlock(ctx context.Context, target storageapp.BlockTarget) (*adminv1.Block, error) {
	if target.ShardID != "local" {
		return nil, mapError(metastore.ErrNotFound)
	}
	documents, err := a.app.metadata.ListBlockDocuments(target.BlockID)
	if err != nil {
		return nil, mapError(err)
	}
	if len(documents) == 0 {
		return nil, mapError(metastore.ErrNotFound)
	}
	length, checksum, err := a.blockDigest(ctx, target.BlockID)
	if err != nil {
		return nil, mapError(err)
	}
	backendObjectKey := ""
	intent, err := a.app.metadata.GetUploadIntent(target.BlockID)
	if err != nil && !errors.Is(err, metastore.ErrNotFound) {
		return nil, mapError(err)
	}
	if err == nil {
		backendObjectKey = intent.BackendObjectKey
	}
	return &adminv1.Block{
		ShardId:          "local",
		BlockId:          target.BlockID,
		Length:           length,
		Checksum:         checksum,
		ReplicaMemberIds: []string{a.app.MemberID()},
		BackendObjectKey: backendObjectKey,
	}, nil
}

func (a *InspectApplication) GetAdminMember(ctx context.Context, memberID string) (*adminv1.StorageMember, error) {
	if memberID != a.app.MemberID() {
		return nil, mapError(metastore.ErrNotFound)
	}
	stats, err := a.localDiskStats(ctx)
	if err != nil {
		return nil, err
	}
	return a.localAdminMember(ctx, stats)
}

func (a *InspectApplication) localAdminMember(ctx context.Context, stats localDiskStats) (*adminv1.StorageMember, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	memberState := a.app.currentLocalMemberState()
	state := adminv1.MemberState_MEMBER_STATE_ONLINE
	if memberState.Draining {
		state = adminv1.MemberState_MEMBER_STATE_DRAINING
	}
	return &adminv1.StorageMember{
		StorageMemberId: a.app.MemberID(),
		CellId:          a.app.CellID(),
		State:           state,
		Cordoned:        memberState.Cordoned,
		BytesUsed:       stats.bytesUsed,
		BytesCapacity:   stats.bytesCapacity,
		LastSeenAt:      timestamppb.New(a.app.now()),
	}, nil
}

func (a *InspectApplication) GetAdminCapacityRunway(ctx context.Context, capacityProfileID string) (*adminv1.CapacityRunway, error) {
	if capacityProfileID == "" {
		capacityProfileID = localCapacityProfileID
	}
	if capacityProfileID != localCapacityProfileID {
		return nil, mapError(metastore.ErrNotFound)
	}
	stats, err := a.localDiskStats(ctx)
	if err != nil {
		return nil, err
	}
	return &adminv1.CapacityRunway{
		CapacityProfileId:    localCapacityProfileID,
		UsableBytesRemaining: stats.usableBytesRemaining,
		Warnings: []*adminv1.OperationWarning{
			{
				Code:    "SCRAP_NON_PRODUCTION_CAPACITY_PROFILE",
				Message: "local mode reports filesystem capacity but does not satisfy production capacity-profile gates",
			},
			{
				Code:    "SCRAP_INGRESS_RATE_UNKNOWN",
				Message: "estimated_bytes_per_day and runway_days require a measured deployment ingress profile",
			},
		},
	}, nil
}

func (a *InspectApplication) blockDigest(ctx context.Context, blockID string) (uint64, []byte, error) {
	if err := ctx.Err(); err != nil {
		return 0, nil, err
	}
	file, err := os.Open(a.app.blocks.BlockPath(blockID))
	if err != nil {
		return 0, nil, err
	}
	defer closeutil.Ignore(file)
	hasher := sha256.New()
	written, err := io.Copy(hasher, file)
	if err != nil {
		return 0, nil, err
	}
	if err := ctx.Err(); err != nil {
		return 0, nil, err
	}
	length, err := safeconv.Int64ToUint64("block digest bytes written", written)
	if err != nil {
		return 0, nil, err
	}
	return length, hasher.Sum(nil), nil
}

type localDiskStats struct {
	bytesUsed            uint64
	bytesCapacity        uint64
	usableBytesRemaining uint64
}

func (a *InspectApplication) localDiskStats(ctx context.Context) (localDiskStats, error) {
	if err := ctx.Err(); err != nil {
		return localDiskStats{}, err
	}
	used, err := directorySize(a.app.dir)
	if err != nil {
		return localDiskStats{}, err
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(a.app.dir, &stat); err != nil {
		return localDiskStats{}, fmt.Errorf("stat local storage filesystem: %w", err)
	}
	return localDiskStats{
		bytesUsed:            used,
		bytesCapacity:        statfsBytes(stat.Blocks, stat.Bsize),
		usableBytesRemaining: statfsBytes(stat.Bavail, stat.Bsize),
	}, nil
}

func directorySize(root string) (uint64, error) {
	var total uint64
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		size, err := entrySize(entry, err)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if size <= 0 {
			return nil
		}
		if uint64(size) > math.MaxUint64-total {
			total = math.MaxUint64
			return nil
		}
		total += uint64(size)
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("walk local storage directory: %w", err)
	}
	return total, nil
}

func entrySize(entry fs.DirEntry, entryErr error) (int64, error) {
	if entryErr != nil {
		return 0, entryErr
	}
	if entry.IsDir() {
		return 0, nil
	}
	info, err := entry.Info()
	if err != nil {
		return 0, fmt.Errorf("inspect local storage entry: %w", err)
	}
	return info.Size(), nil
}

func statfsBytes(blocks uint64, blockSize int64) uint64 {
	if blockSize <= 0 {
		return 0
	}
	size := uint64(blockSize)
	if blocks > math.MaxUint64/size {
		return math.MaxUint64
	}
	return blocks * size
}
