package peer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/scrub"
)

const maxTransferBytes int64 = 4 << 30 // 4 GiB safety limit for block transfers

type Client struct {
	mu        sync.Mutex
	conns     map[string]*grpc.ClientConn
	transport credentials.TransportCredentials
}

type ClientOption func(*Client)

func WithClientTransportCredentials(creds credentials.TransportCredentials) ClientOption {
	return func(c *Client) {
		if creds != nil {
			c.transport = creds
		}
	}
}

func NewClient(opts ...ClientOption) *Client {
	c := &Client{
		conns:     make(map[string]*grpc.ClientConn),
		transport: insecure.NewCredentials(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) getConn(addr string) (*grpc.ClientConn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if conn, ok := c.conns[addr]; ok {
		return conn, nil
	}

	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(c.transport.Clone()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return nil, fmt.Errorf("peer: dial %s: %w", addr, err)
	}
	c.conns[addr] = conn
	return conn, nil
}

func (c *Client) ReplicateDocument(ctx context.Context, addr string, init *scrapv1.ReplicateDocumentInit, chunks [][]byte) ([]byte, error) {
	conn, err := c.getConn(addr)
	if err != nil {
		return nil, err
	}

	client := scrapv1.NewPeerServiceClient(conn)
	stream, err := client.ReplicateDocument(ctx)
	if err != nil {
		return nil, fmt.Errorf("peer: open stream to %s: %w", addr, err)
	}

	if err := stream.Send(&scrapv1.ReplicateDocumentRequest{
		Part: &scrapv1.ReplicateDocumentRequest_Init{Init: init},
	}); err != nil {
		return nil, fmt.Errorf("peer: send init to %s: %w", addr, err)
	}

	for _, chunk := range chunks {
		if err := stream.Send(&scrapv1.ReplicateDocumentRequest{
			Part: &scrapv1.ReplicateDocumentRequest_ChunkData{ChunkData: chunk},
		}); err != nil {
			return nil, fmt.Errorf("peer: send chunk to %s: %w", addr, err)
		}
	}

	resp, err := stream.CloseAndRecv()
	if err != nil {
		return nil, fmt.Errorf("peer: close stream to %s: %w", addr, err)
	}

	return resp.GetSha256(), nil
}

func (c *Client) ConsistencyCheck(ctx context.Context, addr, scrubID string) (*scrapv1.ConsistencyCheckResponse, error) {
	conn, err := c.getConn(addr)
	if err != nil {
		return nil, err
	}
	client := scrapv1.NewPeerServiceClient(conn)
	resp, err := client.ConsistencyCheck(ctx, &scrapv1.ConsistencyCheckRequest{
		ScrubId: scrubID,
	})
	if err != nil {
		return nil, fmt.Errorf("peer: consistency check %s: %w", addr, mapConsistencyCheckError(err))
	}
	return resp, nil
}

func mapConsistencyCheckError(err error) error {
	if status.Code(err) == codes.NotFound {
		return fmt.Errorf("%w: %w", scrub.ErrConsistencyResultNotReady, err)
	}
	return err
}

func (c *Client) RequestIndexRebuild(ctx context.Context, addr, scrubID string) (*scrapv1.RequestIndexRebuildResponse, error) {
	conn, err := c.getConn(addr)
	if err != nil {
		return nil, err
	}
	client := scrapv1.NewPeerServiceClient(conn)
	resp, err := client.RequestIndexRebuild(ctx, &scrapv1.RequestIndexRebuildRequest{
		ScrubId: scrubID,
	})
	if err != nil {
		return nil, fmt.Errorf("peer: request rebuild %s: %w", addr, err)
	}
	return resp, nil
}

func (c *Client) TransferBlock(ctx context.Context, addr string, blockID uint64) ([]byte, []byte, error) {
	conn, err := c.getConn(addr)
	if err != nil {
		return nil, nil, err
	}
	client := scrapv1.NewPeerServiceClient(conn)
	stream, err := client.TransferBlock(ctx, &scrapv1.TransferBlockRequest{
		BlockId: blockID,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("peer: transfer block %d from %s: %w", blockID, addr, mapTransferError(err))
	}

	msg, err := stream.Recv()
	if err != nil {
		return nil, nil, fmt.Errorf("peer: transfer block meta: %w", mapTransferError(err))
	}
	meta := msg.GetMeta()
	if meta == nil {
		return nil, nil, errors.New("peer: expected meta, got chunk")
	}

	return recvBlockData(stream, meta.BlockSize, meta.IdxSize)
}

func mapTransferError(err error) error {
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	switch st.Code() {
	case codes.FailedPrecondition:
		if transferStatusHasReason(err, transferReasonEvicted) {
			return fmt.Errorf("%w: %w", scrub.ErrPeerBlockEvicted, err)
		}
	case codes.DataLoss:
		switch {
		case transferStatusHasReason(err, transferReasonMetadataLoss):
			return fmt.Errorf("%w: %w", scrub.ErrPeerBlockMetadataLoss, err)
		case transferStatusHasReason(err, transferReasonUnexpectedLoss):
			return fmt.Errorf("%w: %w", scrub.ErrPeerBlockUnexpectedLoss, err)
		case transferStatusHasReason(err, transferReasonQuarantined):
			return fmt.Errorf("%w: %w", scrub.ErrPeerBlockQuarantined, err)
		}
	default:
		return err
	}
	return err
}

func validateTransferSizes(blkSize, idxSize int64) error {
	if blkSize < 0 || blkSize > maxTransferBytes {
		return fmt.Errorf("peer: invalid block size %d", blkSize)
	}
	if idxSize < 0 || idxSize > maxTransferBytes {
		return fmt.Errorf("peer: invalid index size %d", idxSize)
	}
	return nil
}

func recvBlockData(stream grpc.ServerStreamingClient[scrapv1.TransferBlockResponse], blkSize, idxSize int64) ([]byte, []byte, error) {
	if err := validateTransferSizes(blkSize, idxSize); err != nil {
		return nil, nil, err
	}

	blkData := make([]byte, 0, blkSize)
	idxData := make([]byte, 0, idxSize)
	remaining := blkSize

	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("peer: recv block data: %w", err)
		}
		chunk := msg.GetChunkData()
		if remaining <= 0 {
			idxData = append(idxData, chunk...)
			continue
		}
		take := min(int64(len(chunk)), remaining)
		blkData = append(blkData, chunk[:take]...)
		if take < int64(len(chunk)) {
			idxData = append(idxData, chunk[take:]...)
		}
		remaining -= take
	}

	if int64(len(blkData)) != blkSize {
		return nil, nil, fmt.Errorf("peer: block size mismatch: got %d, expected %d", len(blkData), blkSize)
	}
	if int64(len(idxData)) != idxSize {
		return nil, nil, fmt.Errorf("peer: index size mismatch: got %d, expected %d", len(idxData), idxSize)
	}
	return blkData, idxData, nil
}

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, conn := range c.conns {
		_ = conn.Close()
	}
}

type ReplicateResult struct {
	PeerAddr string
	SHA256   []byte
	Err      error
}

func (c *Client) FanOut(ctx context.Context, peerAddrs []string, init *scrapv1.ReplicateDocumentInit, chunks [][]byte) []ReplicateResult {
	results := make([]ReplicateResult, len(peerAddrs))
	var wg sync.WaitGroup

	for i, addr := range peerAddrs {
		wg.Add(1)
		go func(idx int, a string) {
			defer wg.Done()
			sha, err := c.ReplicateDocument(ctx, a, init, chunks)
			results[idx] = ReplicateResult{PeerAddr: a, SHA256: sha, Err: err}
		}(i, addr)
	}

	wg.Wait()
	return results
}

func QuorumMet(totalVoters, successfulPeers int) bool {
	quorum := totalVoters/2 + 1 //nolint:mnd // majority quorum formula: ⌊n/2⌋+1
	leaderPlusSuccessful := 1 + successfulPeers
	return leaderPlusSuccessful >= quorum
}
