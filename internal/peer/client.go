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

func (c *Client) TransferBlock(ctx context.Context, addr string, shardID, blockID uint64) ([]byte, []byte, error) {
	conn, err := c.getConn(addr)
	if err != nil {
		return nil, nil, err
	}
	client := scrapv1.NewPeerServiceClient(conn)
	stream, err := client.TransferBlock(ctx, &scrapv1.TransferBlockRequest{
		ShardId: shardID,
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

// transferPreallocBytes caps the initial allocation derived from peer-declared
// sizes; buffers still grow to the validated size as data actually arrives.
const transferPreallocBytes int64 = 64 << 20 // 64 MiB, the nominal Block size

func recvBlockData(stream grpc.ServerStreamingClient[scrapv1.TransferBlockResponse], blkSize, idxSize int64) ([]byte, []byte, error) {
	if err := validateTransferSizes(blkSize, idxSize); err != nil {
		return nil, nil, err
	}

	bufs := &blockDataBuffers{
		blk:       make([]byte, 0, min(blkSize, transferPreallocBytes)),
		idx:       make([]byte, 0, min(idxSize, transferPreallocBytes)),
		remaining: blkSize,
		idxSize:   idxSize,
	}

	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("peer: recv block data: %w", err)
		}
		if err := bufs.consume(msg.GetChunkData()); err != nil {
			return nil, nil, err
		}
	}

	if int64(len(bufs.blk)) != blkSize {
		return nil, nil, fmt.Errorf("peer: block size mismatch: got %d, expected %d", len(bufs.blk), blkSize)
	}
	if int64(len(bufs.idx)) != idxSize {
		return nil, nil, fmt.Errorf("peer: index size mismatch: got %d, expected %d", len(bufs.idx), idxSize)
	}
	return bufs.blk, bufs.idx, nil
}

// blockDataBuffers splits a transfer stream at the declared block size into
// blk and idx buffers.
type blockDataBuffers struct {
	blk       []byte
	idx       []byte
	remaining int64
	idxSize   int64
}

func (b *blockDataBuffers) consume(chunk []byte) error {
	if b.remaining <= 0 {
		var err error
		b.idx, err = appendBoundedIdxData(b.idx, chunk, b.idxSize)
		return err
	}
	take := min(int64(len(chunk)), b.remaining)
	b.blk = append(b.blk, chunk[:take]...)
	if take < int64(len(chunk)) {
		var err error
		if b.idx, err = appendBoundedIdxData(b.idx, chunk[take:], b.idxSize); err != nil {
			return err
		}
	}
	b.remaining -= take
	return nil
}

// appendBoundedIdxData rejects index bytes beyond the declared size as soon as
// they arrive, so a peer that never stops streaming cannot grow the buffer
// unboundedly waiting for an EOF-time check.
func appendBoundedIdxData(idxData, chunk []byte, idxSize int64) ([]byte, error) {
	if int64(len(idxData))+int64(len(chunk)) > idxSize {
		return nil, fmt.Errorf("peer: index data exceeds declared size %d", idxSize)
	}
	return append(idxData, chunk...), nil
}

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for addr, conn := range c.conns {
		_ = conn.Close()
		// Drop the closed conn so a later call redials instead of failing
		// every RPC on a conn stuck in the closed state.
		delete(c.conns, addr)
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
