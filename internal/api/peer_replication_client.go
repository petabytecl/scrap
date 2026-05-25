package api

import (
	"context"
	"fmt"
	"strings"
	"sync"

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

	mu     sync.Mutex
	conn   *grpc.ClientConn
	client adminv1.PeerReplicationServiceClient
}

func NewPeerReplicationClientPreparer(address, workloadIdentity string) *PeerReplicationClientPreparer {
	return &PeerReplicationClientPreparer{
		Address:          strings.TrimSpace(address),
		WorkloadIdentity: strings.TrimSpace(workloadIdentity),
	}
}

func (p *PeerReplicationClientPreparer) PrepareDocument(ctx context.Context, request replication.PrepareRequest) (replication.Receipt, error) {
	if p == nil || p.Address == "" {
		return replication.Receipt{}, replication.ErrReceiptMismatch
	}
	data, err := peerPrepareDataFromRequest(ctx, request)
	if err != nil {
		return replication.Receipt{}, err
	}
	if err := replication.ValidatePreparedBytes(request.Document, data); err != nil {
		return replication.Receipt{}, err
	}
	client, err := p.peerClient()
	if err != nil {
		return replication.Receipt{}, err
	}
	callCtx := ctx
	if p.WorkloadIdentity != "" {
		callCtx = metadata.AppendToOutgoingContext(callCtx, authz.WorkloadIdentityMetadataKey, p.WorkloadIdentity)
	}
	resp, err := client.PrepareDocument(callCtx, peerPrepareRequestToProto(request.Document, data))
	if err != nil {
		return replication.Receipt{}, err
	}
	return peerReplicaReceiptFromProto(resp.GetReceipt())
}

func (p *PeerReplicationClientPreparer) peerClient() (adminv1.PeerReplicationServiceClient, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client != nil {
		return p.client, nil
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
		return nil, fmt.Errorf("create peer replication client %q: %w", p.Address, err)
	}
	p.conn = conn
	p.client = adminv1.NewPeerReplicationServiceClient(conn)
	return p.client, nil
}

func (p *PeerReplicationClientPreparer) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn == nil {
		return nil
	}
	err := p.conn.Close()
	p.conn = nil
	p.client = nil
	return err
}
