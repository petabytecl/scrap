package api

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/petabytecl/scrap/internal/authz"
	"github.com/petabytecl/scrap/internal/config"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/replication"
)

type PeerReplicationClientPreparer struct {
	Address          string
	WorkloadIdentity string
}

func NewPeerReplicationClientPreparer(address, workloadIdentity string) PeerReplicationClientPreparer {
	return PeerReplicationClientPreparer{
		Address:          strings.TrimSpace(address),
		WorkloadIdentity: strings.TrimSpace(workloadIdentity),
	}
}

func (p PeerReplicationClientPreparer) PrepareDocument(ctx context.Context, request replication.PrepareRequest) (replication.Receipt, error) {
	if p.Address == "" {
		return replication.Receipt{}, replication.ErrReceiptMismatch
	}
	data, err := peerPrepareDataFromRequest(ctx, request)
	if err != nil {
		return replication.Receipt{}, err
	}
	if err := replication.ValidatePreparedBytes(request.Document, data); err != nil {
		return replication.Receipt{}, err
	}
	conn, err := grpc.NewClient(
		p.Address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(config.DefaultGRPCMaxRecvMsgSizeBytes),
			grpc.MaxCallSendMsgSize(config.DefaultGRPCMaxSendMsgSizeBytes),
		),
	)
	if err != nil {
		return replication.Receipt{}, fmt.Errorf("create peer replication client %q: %w", p.Address, err)
	}
	defer func() { _ = conn.Close() }()

	callCtx := ctx
	if p.WorkloadIdentity != "" {
		callCtx = metadata.AppendToOutgoingContext(callCtx, authz.WorkloadIdentityMetadataKey, p.WorkloadIdentity)
	}
	resp, err := adminv1.NewPeerReplicationServiceClient(conn).PrepareDocument(callCtx, peerPrepareRequestToProto(request.Document, data))
	if err != nil {
		return replication.Receipt{}, err
	}
	return peerReplicaReceiptFromProto(resp.GetReceipt())
}
