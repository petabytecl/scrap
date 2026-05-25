package adminpeer

import (
	"context"
	"fmt"
	"sort"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"

	"github.com/petabytecl/scrap/internal/api"
	"github.com/petabytecl/scrap/internal/appstatus"
	"github.com/petabytecl/scrap/internal/authz"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/safeconv"
)

const (
	DefaultInspectTimeout = 2 * time.Second

	WarningPeerInspectUnavailable      = "SCRAP_PEER_ADMIN_INSPECT_UNAVAILABLE"
	WarningPeerInspectIdentityMismatch = "SCRAP_PEER_ADMIN_IDENTITY_MISMATCH"

	peerInspectLocalOnlyMetadataKey = "x-scrap-peer-inspect-local-only"
)

type Peer struct {
	MemberID string
	Address  string
}

type Options struct {
	Local              api.InspectApplication
	LocalMemberID      string
	Peers              []Peer
	WorkloadIdentity   string
	PeerRequestTimeout time.Duration
	Dial               Dialer
}

type Dialer func(context.Context, string) (InspectClient, error)

type InspectClient interface {
	GetMember(context.Context, *adminv1.GetMemberRequest, ...grpc.CallOption) (*adminv1.GetMemberResponse, error)
	GetCapacityRunway(context.Context, *adminv1.GetCapacityRunwayRequest, ...grpc.CallOption) (*adminv1.GetCapacityRunwayResponse, error)
	Close() error
}

type Aggregator struct {
	local              api.InspectApplication
	localMemberID      string
	peers              []Peer
	peerRequestTimeout time.Duration
	dial               Dialer
}

func NewAggregator(options Options) api.InspectApplication {
	peers := normalizePeers(options.Peers, options.LocalMemberID)
	if len(peers) == 0 {
		return options.Local
	}
	timeout := options.PeerRequestTimeout
	if timeout <= 0 {
		timeout = DefaultInspectTimeout
	}
	dial := options.Dial
	if dial == nil {
		dial = NewGRPCDialer(options.WorkloadIdentity)
	}
	return &Aggregator{
		local:              options.Local,
		localMemberID:      options.LocalMemberID,
		peers:              peers,
		peerRequestTimeout: timeout,
		dial:               dial,
	}
}

func NewGRPCDialer(workloadIdentity string) Dialer {
	return func(_ context.Context, address string) (InspectClient, error) {
		conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, err
		}
		return &grpcInspectClient{
			ClientConn: conn,
			client:     adminv1.NewInspectServiceClient(conn),
			workload:   workloadIdentity,
		}, nil
	}
}

type grpcInspectClient struct {
	*grpc.ClientConn
	client   adminv1.InspectServiceClient
	workload string
}

func (c *grpcInspectClient) GetMember(ctx context.Context, req *adminv1.GetMemberRequest, opts ...grpc.CallOption) (*adminv1.GetMemberResponse, error) {
	return c.client.GetMember(peerContext(ctx, c.workload), req, opts...)
}

func (c *grpcInspectClient) GetCapacityRunway(ctx context.Context, req *adminv1.GetCapacityRunwayRequest, opts ...grpc.CallOption) (*adminv1.GetCapacityRunwayResponse, error) {
	return c.client.GetCapacityRunway(peerLocalOnlyContext(ctx, c.workload), req, opts...)
}

func peerContext(ctx context.Context, workloadIdentity string) context.Context {
	if workloadIdentity == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, authz.WorkloadIdentityMetadataKey, workloadIdentity)
}

func peerLocalOnlyContext(ctx context.Context, workloadIdentity string) context.Context {
	ctx = peerContext(ctx, workloadIdentity)
	return metadata.AppendToOutgoingContext(ctx, peerInspectLocalOnlyMetadataKey, "true")
}

func (a *Aggregator) GetAdminClusterSummary(ctx context.Context) (*adminv1.ClusterSummary, error) {
	summary, err := a.local.GetAdminClusterSummary(ctx)
	if err != nil {
		return nil, err
	}
	out := cloneClusterSummary(summary)
	localCellID := summaryCellID(out)
	for _, peer := range a.peers {
		member, warning := a.fetchPeerMember(ctx, peer, localCellID)
		if warning != nil {
			out.Warnings = append(out.Warnings, warning)
			continue
		}
		out.StorageMembers = append(out.StorageMembers, member)
		out.LocalBytesUsed = saturatingAdd(out.LocalBytesUsed, member.GetBytesUsed())
		out.LocalBytesCapacity = saturatingAdd(out.LocalBytesCapacity, member.GetBytesCapacity())
	}
	sortStorageMembers(out.StorageMembers)
	count, err := safeconv.IntToUint32("storage member count", len(out.StorageMembers))
	if err != nil {
		return nil, appstatus.Wrap(appstatus.CodeInternal, "storage member count overflow", err)
	}
	out.StorageMemberCount = count
	return out, nil
}

func (a *Aggregator) GetAdminShard(ctx context.Context, shardID string) (*adminv1.Shard, error) {
	return a.local.GetAdminShard(ctx, shardID)
}

func (a *Aggregator) GetAdminDocument(ctx context.Context, doc identity.Document) (*adminv1.AdminDocument, error) {
	return a.local.GetAdminDocument(ctx, doc)
}

func (a *Aggregator) GetAdminBlock(ctx context.Context, block api.BlockTarget) (*adminv1.Block, error) {
	return a.local.GetAdminBlock(ctx, block)
}

func (a *Aggregator) GetAdminMember(ctx context.Context, memberID string) (*adminv1.StorageMember, error) {
	if memberID == a.localMemberID {
		return a.local.GetAdminMember(ctx, memberID)
	}
	for _, peer := range a.peers {
		if peer.MemberID != memberID {
			continue
		}
		member, err := a.getPeerMember(ctx, peer)
		if err != nil {
			return nil, appstatus.Wrap(appstatus.CodeUnavailable, "peer admin inspect unavailable", err)
		}
		if member.GetStorageMemberId() != memberID {
			return nil, appstatus.Errorf(appstatus.CodeUnavailable, "peer member identity mismatch: requested %q, got %q", memberID, member.GetStorageMemberId())
		}
		return member, nil
	}
	return nil, appstatus.Errorf(appstatus.CodeNotFound, "storage member %q not found", memberID)
}

func (a *Aggregator) GetAdminCapacityRunway(ctx context.Context, capacityProfileID string) (*adminv1.CapacityRunway, error) {
	if peerInspectLocalOnly(ctx) {
		return a.local.GetAdminCapacityRunway(ctx, capacityProfileID)
	}
	runway, err := a.local.GetAdminCapacityRunway(ctx, capacityProfileID)
	if err != nil {
		return nil, err
	}
	out := cloneCapacityRunway(runway)
	for _, peer := range a.peers {
		peerRunway, err := a.getPeerCapacityRunway(ctx, peer, capacityProfileID)
		if err != nil {
			out.Warnings = append(out.Warnings, peerWarning(WarningPeerInspectUnavailable, peer, err))
			continue
		}
		out.UsableBytesRemaining = saturatingAdd(out.UsableBytesRemaining, peerRunway.GetUsableBytesRemaining())
		out.EstimatedBytesPerDay = combineEstimatedBytesPerDay(out.GetEstimatedBytesPerDay(), peerRunway.GetEstimatedBytesPerDay())
		out.RunwayDays = combineRunwayDays(out.GetRunwayDays(), peerRunway.GetRunwayDays())
		out.Warnings = append(out.Warnings, peerRunway.GetWarnings()...)
	}
	return out, nil
}

func peerInspectLocalOnly(ctx context.Context) bool {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return false
	}
	for _, value := range md.Get(peerInspectLocalOnlyMetadataKey) {
		if value == "true" {
			return true
		}
	}
	return false
}

func (a *Aggregator) fetchPeerMember(ctx context.Context, peer Peer, localCellID string) (*adminv1.StorageMember, *adminv1.OperationWarning) {
	member, err := a.getPeerMember(ctx, peer)
	if err != nil {
		return nil, peerWarning(WarningPeerInspectUnavailable, peer, err)
	}
	if member.GetStorageMemberId() != peer.MemberID {
		return nil, peerWarningf(WarningPeerInspectIdentityMismatch, peer, "peer reported member %q", member.GetStorageMemberId())
	}
	if localCellID != "" && member.GetCellId() != "" && member.GetCellId() != localCellID {
		return nil, peerWarningf(WarningPeerInspectIdentityMismatch, peer, "peer reported cell %q, expected %q", member.GetCellId(), localCellID)
	}
	return member, nil
}

func (a *Aggregator) getPeerMember(ctx context.Context, peer Peer) (*adminv1.StorageMember, error) {
	peerCtx, client, cancel, err := a.peerClient(ctx, peer)
	if err != nil {
		return nil, err
	}
	defer cancel()
	resp, err := client.GetMember(peerCtx, &adminv1.GetMemberRequest{
		StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: peer.MemberID},
	})
	if err != nil {
		return nil, err
	}
	return resp.GetStorageMember(), nil
}

func (a *Aggregator) getPeerCapacityRunway(ctx context.Context, peer Peer, capacityProfileID string) (*adminv1.CapacityRunway, error) {
	peerCtx, client, cancel, err := a.peerClient(ctx, peer)
	if err != nil {
		return nil, err
	}
	defer cancel()
	resp, err := client.GetCapacityRunway(peerCtx, &adminv1.GetCapacityRunwayRequest{
		CapacityProfileId: optionalString(capacityProfileID),
	})
	if err != nil {
		return nil, err
	}
	return resp.GetRunway(), nil
}

func (a *Aggregator) peerClient(ctx context.Context, peer Peer) (context.Context, InspectClient, func(), error) {
	peerCtx, cancel := context.WithTimeout(ctx, a.peerRequestTimeout)
	client, err := a.dial(peerCtx, peer.Address)
	if err != nil {
		cancel()
		return nil, nil, func() {}, err
	}
	return peerCtx, client, func() {
		_ = client.Close()
		cancel()
	}, nil
}

func normalizePeers(peers []Peer, localMemberID string) []Peer {
	seen := make(map[string]struct{}, len(peers))
	out := make([]Peer, 0, len(peers))
	for _, peer := range peers {
		if peer.MemberID == "" || peer.Address == "" || peer.MemberID == localMemberID {
			continue
		}
		if _, exists := seen[peer.MemberID]; exists {
			continue
		}
		seen[peer.MemberID] = struct{}{}
		out = append(out, peer)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].MemberID < out[j].MemberID
	})
	return out
}

func cloneClusterSummary(summary *adminv1.ClusterSummary) *adminv1.ClusterSummary {
	if summary == nil {
		return &adminv1.ClusterSummary{}
	}
	return proto.Clone(summary).(*adminv1.ClusterSummary)
}

func cloneCapacityRunway(runway *adminv1.CapacityRunway) *adminv1.CapacityRunway {
	if runway == nil {
		return &adminv1.CapacityRunway{}
	}
	return proto.Clone(runway).(*adminv1.CapacityRunway)
}

func sortStorageMembers(members []*adminv1.StorageMember) {
	sort.Slice(members, func(i, j int) bool {
		return members[i].GetStorageMemberId() < members[j].GetStorageMemberId()
	})
}

func summaryCellID(summary *adminv1.ClusterSummary) string {
	for _, member := range summary.GetStorageMembers() {
		if member.GetCellId() != "" {
			return member.GetCellId()
		}
	}
	return ""
}

func peerWarning(code string, peer Peer, err error) *adminv1.OperationWarning {
	return peerWarningf(code, peer, "%v", err)
}

func peerWarningf(code string, peer Peer, format string, args ...any) *adminv1.OperationWarning {
	return &adminv1.OperationWarning{
		Code:    code,
		Message: fmt.Sprintf("peer %s at %s: "+format, append([]any{peer.MemberID, peer.Address}, args...)...),
		Target:  &adminv1.Target{Target: &adminv1.Target_StorageMember{StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: peer.MemberID}}},
	}
}

func saturatingAdd(left, right uint64) uint64 {
	if right > ^uint64(0)-left {
		return ^uint64(0)
	}
	return left + right
}

func combineEstimatedBytesPerDay(left, right uint64) uint64 {
	if left == 0 || right == 0 {
		return 0
	}
	return saturatingAdd(left, right)
}

func combineRunwayDays(left, right uint32) uint32 {
	if left == 0 || right == 0 {
		return 0
	}
	if left < right {
		return left
	}
	return right
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
