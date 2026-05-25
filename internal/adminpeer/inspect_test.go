package adminpeer

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"

	"github.com/petabytecl/scrap/internal/api"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/safeconv"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestAggregatorCombinesPeerMembersAndCapacity(t *testing.T) {
	local := &fakeInspectApplication{
		summary: summaryWithMembers(member("scrapd-0", 10, 100)),
		runway:  runway(40, warning("SCRAP_NON_PRODUCTION_CAPACITY_PROFILE")),
	}
	peers := map[string]*fakeInspectClient{
		"scrapd-1:18081": {
			member: member("scrapd-1", 20, 200),
			runway: runway(50, &adminv1.OperationWarning{
				Code: "SCRAP_PEER_CAPACITY_ADVISORY",
			}),
		},
		"scrapd-2:18081": {
			member: member("scrapd-2", 30, 300),
			runway: runway(60),
		},
	}
	inspect := NewAggregator(Options{
		Local:         local,
		LocalMemberID: "scrapd-0",
		Peers: []Peer{
			{MemberID: "scrapd-1", Address: "scrapd-1:18081"},
			{MemberID: "scrapd-2", Address: "scrapd-2:18081"},
		},
		Dial: fakeDialer(peers, nil),
	})

	summary, err := inspect.GetAdminClusterSummary(context.Background())
	testutil.RequireNoErrorf(t, err, "aggregate summary")
	testutil.RequireEqualf(t, summary.GetStorageMemberCount(), uint32(3), "member count")
	testutil.RequireEqualf(t, summary.GetLocalBytesUsed(), uint64(60), "aggregate bytes used")
	testutil.RequireEqualf(t, summary.GetLocalBytesCapacity(), uint64(600), "aggregate bytes capacity")
	testutil.RequireEqualf(t, summary.GetStorageMembers()[0].GetStorageMemberId(), "scrapd-0", "first member")
	testutil.RequireEqualf(t, summary.GetStorageMembers()[2].GetStorageMemberId(), "scrapd-2", "last member")
	testutil.RequireEqualf(t, len(summary.GetWarnings()), 0, "summary warnings")

	runway, err := inspect.GetAdminCapacityRunway(context.Background(), "")
	testutil.RequireNoErrorf(t, err, "aggregate runway")
	testutil.RequireEqualf(t, runway.GetUsableBytesRemaining(), uint64(150), "aggregate usable bytes")
	testutil.RequireEqualf(t, len(runway.GetWarnings()), 2, "aggregate runway warnings")

	peerMember, err := inspect.GetAdminMember(context.Background(), "scrapd-2")
	testutil.RequireNoErrorf(t, err, "get peer member")
	testutil.RequireEqualf(t, peerMember.GetStorageMemberId(), "scrapd-2", "peer member id")
}

func TestAggregatorSurfacesPartialPeerFailureWarnings(t *testing.T) {
	local := &fakeInspectApplication{
		summary: summaryWithMembers(member("scrapd-0", 10, 100)),
		runway:  runway(40),
	}
	dialErrors := map[string]error{"scrapd-1:18081": errors.New("connection refused")}
	inspect := NewAggregator(Options{
		Local:         local,
		LocalMemberID: "scrapd-0",
		Peers: []Peer{
			{MemberID: "scrapd-1", Address: "scrapd-1:18081"},
			{MemberID: "scrapd-2", Address: "scrapd-2:18081"},
		},
		Dial: fakeDialer(map[string]*fakeInspectClient{
			"scrapd-2:18081": {member: member("scrapd-2", 30, 300), runway: runway(60)},
		}, dialErrors),
	})

	summary, err := inspect.GetAdminClusterSummary(context.Background())
	testutil.RequireNoErrorf(t, err, "aggregate partial summary")
	testutil.RequireEqualf(t, summary.GetStorageMemberCount(), uint32(2), "partial member count")
	testutil.RequireEqualf(t, len(summary.GetWarnings()), 1, "partial summary warning count")
	testutil.RequireEqualf(t, summary.GetWarnings()[0].GetCode(), WarningPeerInspectUnavailable, "partial summary warning code")

	runway, err := inspect.GetAdminCapacityRunway(context.Background(), "")
	testutil.RequireNoErrorf(t, err, "aggregate partial runway")
	testutil.RequireEqualf(t, runway.GetUsableBytesRemaining(), uint64(100), "partial usable bytes")
	testutil.RequireEqualf(t, runway.GetWarnings()[0].GetCode(), WarningPeerInspectUnavailable, "partial runway warning code")
}

func TestNewAggregatorFallsBackToLocalInspectWithoutPeers(t *testing.T) {
	local := &fakeInspectApplication{summary: summaryWithMembers(member("local", 1, 2))}
	inspect := NewAggregator(Options{Local: local, LocalMemberID: "local"})
	if inspect != local {
		t.Fatalf("aggregator = %T, want local inspect fallback", inspect)
	}
}

func TestNewAggregatorFallsBackToLocalInspectWithoutRemotePeers(t *testing.T) {
	local := &fakeInspectApplication{summary: summaryWithMembers(member("scrapd-0", 1, 2))}
	inspect := NewAggregator(Options{
		Local:         local,
		LocalMemberID: "scrapd-0",
		Peers: []Peer{
			{MemberID: "scrapd-0", Address: "scrapd-0:18081"},
			{MemberID: "", Address: "scrapd-1:18081"},
		},
	})
	if inspect != local {
		t.Fatalf("aggregator = %T, want local inspect fallback", inspect)
	}
}

type fakeInspectApplication struct {
	summary *adminv1.ClusterSummary
	runway  *adminv1.CapacityRunway
	member  *adminv1.StorageMember
}

func (f *fakeInspectApplication) GetAdminClusterSummary(context.Context) (*adminv1.ClusterSummary, error) {
	return f.summary, nil
}

func (f *fakeInspectApplication) GetAdminShard(context.Context, string) (*adminv1.Shard, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeInspectApplication) GetAdminDocument(context.Context, identity.Document) (*adminv1.AdminDocument, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeInspectApplication) GetAdminBlock(context.Context, api.BlockTarget) (*adminv1.Block, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeInspectApplication) GetAdminMember(context.Context, string) (*adminv1.StorageMember, error) {
	if f.member != nil {
		return f.member, nil
	}
	if len(f.summary.GetStorageMembers()) == 0 {
		return nil, errors.New("not found")
	}
	return f.summary.GetStorageMembers()[0], nil
}

func (f *fakeInspectApplication) GetAdminCapacityRunway(context.Context, string) (*adminv1.CapacityRunway, error) {
	return f.runway, nil
}

type fakeInspectClient struct {
	member *adminv1.StorageMember
	runway *adminv1.CapacityRunway
}

func (f *fakeInspectClient) GetMember(context.Context, *adminv1.GetMemberRequest, ...grpc.CallOption) (*adminv1.GetMemberResponse, error) {
	return &adminv1.GetMemberResponse{StorageMember: f.member}, nil
}

func (f *fakeInspectClient) GetCapacityRunway(context.Context, *adminv1.GetCapacityRunwayRequest, ...grpc.CallOption) (*adminv1.GetCapacityRunwayResponse, error) {
	return &adminv1.GetCapacityRunwayResponse{Runway: f.runway}, nil
}

func (f *fakeInspectClient) Close() error {
	return nil
}

func fakeDialer(clients map[string]*fakeInspectClient, errs map[string]error) Dialer {
	return func(_ context.Context, address string) (InspectClient, error) {
		if err := errs[address]; err != nil {
			return nil, err
		}
		client := clients[address]
		if client == nil {
			return nil, errors.New("missing fake peer")
		}
		return client, nil
	}
}

func summaryWithMembers(members ...*adminv1.StorageMember) *adminv1.ClusterSummary {
	var used uint64
	var capacity uint64
	for _, member := range members {
		used += member.GetBytesUsed()
		capacity += member.GetBytesCapacity()
	}
	count, err := safeconv.IntToUint32("storage member count", len(members))
	if err != nil {
		panic(err)
	}
	return &adminv1.ClusterSummary{
		ShardCount:         1,
		StorageMemberCount: count,
		LocalBytesUsed:     used,
		LocalBytesCapacity: capacity,
		StorageMembers:     members,
	}
}

func member(id string, used, capacity uint64) *adminv1.StorageMember {
	return &adminv1.StorageMember{
		StorageMemberId: id,
		CellId:          "scrap-cell-a",
		State:           adminv1.MemberState_MEMBER_STATE_ONLINE,
		BytesUsed:       used,
		BytesCapacity:   capacity,
	}
}

func runway(usable uint64, warnings ...*adminv1.OperationWarning) *adminv1.CapacityRunway {
	return &adminv1.CapacityRunway{
		CapacityProfileId:    "local-non-production",
		UsableBytesRemaining: usable,
		EstimatedBytesPerDay: 0,
		RunwayDays:           0,
		Warnings:             warnings,
	}
}

func warning(code string) *adminv1.OperationWarning {
	return &adminv1.OperationWarning{Code: code}
}
