package peer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/admission"
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

func (c *Client) ConsistencyCheck(ctx context.Context, addr string, shardID uint64, scrubID string) (*scrapv1.ConsistencyCheckResponse, error) {
	conn, err := c.getConn(addr)
	if err != nil {
		return nil, err
	}
	client := scrapv1.NewPeerServiceClient(conn)
	resp, err := client.ConsistencyCheck(ctx, &scrapv1.ConsistencyCheckRequest{
		ScrubId: scrubID,
		ShardId: shardID,
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

func (c *Client) RequestIndexRebuild(ctx context.Context, addr string, shardID uint64, scrubID string) (*scrapv1.RequestIndexRebuildResponse, error) {
	conn, err := c.getConn(addr)
	if err != nil {
		return nil, err
	}
	client := scrapv1.NewPeerServiceClient(conn)
	resp, err := client.RequestIndexRebuild(ctx, &scrapv1.RequestIndexRebuildRequest{
		ScrubId: scrubID,
		ShardId: shardID,
	})
	if err != nil {
		return nil, fmt.Errorf("peer: request rebuild %s: %w", addr, err)
	}
	return resp, nil
}

// TransferBlockToFiles streams a peer Block into durable staging files without
// buffering the full Block in memory (ADR 0036 / H-13).
func (c *Client) TransferBlockToFiles(ctx context.Context, addr string, shardID, blockID uint64, blkPath, idxPath string) error {
	conn, err := c.getConn(addr)
	if err != nil {
		return err
	}
	client := scrapv1.NewPeerServiceClient(conn)
	stream, err := client.TransferBlock(ctx, &scrapv1.TransferBlockRequest{
		ShardId: shardID,
		BlockId: blockID,
	})
	if err != nil {
		return fmt.Errorf("peer: transfer block %d from %s: %w", blockID, addr, mapTransferError(err))
	}

	msg, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("peer: transfer block meta: %w", mapTransferError(err))
	}
	meta := msg.GetMeta()
	if meta == nil {
		return errors.New("peer: expected meta, got chunk")
	}
	if err := validateTransferSizes(meta.BlockSize, meta.IdxSize); err != nil {
		return err
	}

	reserve := meta.BlockSize + meta.IdxSize
	if err := admission.Process.Acquire(ctx, reserve); err != nil {
		return err
	}
	defer admission.Process.Release(reserve)

	return recvBlockDataToFiles(stream, meta.BlockSize, meta.IdxSize, blkPath, idxPath)
}

// TransferBlock is a test/helper wrapper that materializes TransferBlockToFiles
// into memory. Production repair paths must use TransferBlockToFiles.
func (c *Client) TransferBlock(ctx context.Context, addr string, shardID, blockID uint64) ([]byte, []byte, error) {
	dir, err := os.MkdirTemp("", "scrap-transfer-*")
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	blkPath := dir + "/block.blk"
	idxPath := dir + "/block.idx"
	if err := c.TransferBlockToFiles(ctx, addr, shardID, blockID, blkPath, idxPath); err != nil {
		return nil, nil, err
	}
	blk, err := os.ReadFile(blkPath) //nolint:gosec // test helper path under temp dir
	if err != nil {
		return nil, nil, err
	}
	idx, err := os.ReadFile(idxPath) //nolint:gosec // test helper path under temp dir
	if err != nil {
		return nil, nil, err
	}
	return blk, idx, nil
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

func recvBlockDataToFiles(stream grpc.ServerStreamingClient[scrapv1.TransferBlockResponse], blkSize, idxSize int64, blkPath, idxPath string) error { //nolint:cyclop // stream-to-file transfer must validate sizes, write both components, and fsync
	blkFile, err := os.OpenFile(blkPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // staging path constructed by caller
	if err != nil {
		return fmt.Errorf("peer: create block staging file: %w", err)
	}
	idxFile, err := os.OpenFile(idxPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // staging path constructed by caller
	if err != nil {
		_ = blkFile.Close()
		return fmt.Errorf("peer: create index staging file: %w", err)
	}
	defer func() {
		_ = blkFile.Close()
		_ = idxFile.Close()
	}()

	writer := &blockDataFileWriter{
		blk:       blkFile,
		idx:       idxFile,
		remaining: blkSize,
		idxSize:   idxSize,
		idxWrote:  0,
	}
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("peer: recv block data: %w", err)
		}
		if err := writer.consume(msg.GetChunkData()); err != nil {
			return err
		}
	}
	if writer.remaining != 0 {
		return fmt.Errorf("peer: block size mismatch: missing %d bytes", writer.remaining)
	}
	if writer.idxWrote != idxSize {
		return fmt.Errorf("peer: index size mismatch: got %d, expected %d", writer.idxWrote, idxSize)
	}
	if err := blkFile.Sync(); err != nil {
		return fmt.Errorf("peer: sync block staging file: %w", err)
	}
	if err := idxFile.Sync(); err != nil {
		return fmt.Errorf("peer: sync index staging file: %w", err)
	}
	return nil
}

// blockDataFileWriter splits a transfer stream at the declared block size into
// blk and idx staging files.
type blockDataFileWriter struct {
	blk       *os.File
	idx       *os.File
	remaining int64
	idxSize   int64
	idxWrote  int64
}

func (w *blockDataFileWriter) consume(chunk []byte) error {
	if w.remaining <= 0 {
		return w.writeIdx(chunk)
	}
	take := min(int64(len(chunk)), w.remaining)
	if _, err := w.blk.Write(chunk[:take]); err != nil {
		return fmt.Errorf("peer: write block staging: %w", err)
	}
	if take < int64(len(chunk)) {
		if err := w.writeIdx(chunk[take:]); err != nil {
			return err
		}
	}
	w.remaining -= take
	return nil
}

func (w *blockDataFileWriter) writeIdx(chunk []byte) error {
	if w.idxWrote+int64(len(chunk)) > w.idxSize {
		return fmt.Errorf("peer: index data exceeds declared size %d", w.idxSize)
	}
	if _, err := w.idx.Write(chunk); err != nil {
		return fmt.Errorf("peer: write index staging: %w", err)
	}
	w.idxWrote += int64(len(chunk))
	return nil
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
