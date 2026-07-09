package peer

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/petabytecl/scrap/internal/scrub"
)

type ClientRebuilder struct {
	client  *Client
	shardID uint64
}

func NewClientRebuilder(client *Client, shardID uint64) *ClientRebuilder {
	return &ClientRebuilder{client: client, shardID: shardID}
}

func (r *ClientRebuilder) RequestRebuild(ctx context.Context, addr, scrubID string) error {
	_, err := r.client.RequestIndexRebuild(ctx, addr, r.shardID, scrubID)
	return err
}

type ClientConsistencyChecker struct {
	client  *Client
	shardID uint64
}

func NewClientConsistencyChecker(client *Client, shardID uint64) *ClientConsistencyChecker {
	return &ClientConsistencyChecker{client: client, shardID: shardID}
}

func (c *ClientConsistencyChecker) CheckConsistency(ctx context.Context, addr, scrubID string) (scrub.Result, error) {
	resp, err := c.client.ConsistencyCheck(ctx, addr, c.shardID, scrubID)
	if err != nil {
		return scrub.Result{}, err
	}
	// A short digest zero-padded into [32]byte would look like a valid hash
	// and could drive a spurious divergence verdict; reject it instead.
	if len(resp.GetSha256()) != sha256.Size {
		return scrub.Result{}, fmt.Errorf("peer: consistency check %s returned %d-byte digest, want %d", addr, len(resp.GetSha256()), sha256.Size)
	}
	var sha [32]byte
	copy(sha[:], resp.Sha256)
	return scrub.Result{
		ScrubID:      resp.ScrubId,
		AppliedIndex: resp.AppliedIndex,
		SHA256:       sha,
	}, nil
}
