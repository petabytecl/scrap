package peer

import (
	"context"
	"fmt"
	"sync"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Client struct {
	mu    sync.Mutex
	conns map[string]*grpc.ClientConn
}

func NewClient() *Client {
	return &Client{conns: make(map[string]*grpc.ClientConn)}
}

func (c *Client) getConn(addr string) (*grpc.ClientConn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if conn, ok := c.conns[addr]; ok {
		return conn, nil
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
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

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, conn := range c.conns {
		conn.Close()
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

func QuorumMet(totalVoters int, successfulPeers int) bool {
	quorum := totalVoters/2 + 1
	leaderPlusSuccessful := 1 + successfulPeers
	return leaderPlusSuccessful >= quorum
}
