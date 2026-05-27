package peer

import (
	"context"

	"github.com/petabytecl/scrap/internal/scrub"
)

type ClientRebuilder struct {
	client *Client
}

func NewClientRebuilder(client *Client) *ClientRebuilder {
	return &ClientRebuilder{client: client}
}

func (r *ClientRebuilder) RequestRebuild(ctx context.Context, addr, scrubID string) error {
	_, err := r.client.RequestIndexRebuild(ctx, addr, scrubID)
	return err
}

type ClientConsistencyChecker struct {
	client *Client
}

func NewClientConsistencyChecker(client *Client) *ClientConsistencyChecker {
	return &ClientConsistencyChecker{client: client}
}

func (c *ClientConsistencyChecker) CheckConsistency(ctx context.Context, addr, scrubID string) (scrub.Result, error) {
	resp, err := c.client.ConsistencyCheck(ctx, addr, scrubID)
	if err != nil {
		return scrub.Result{}, err
	}
	var sha [32]byte
	copy(sha[:], resp.Sha256)
	return scrub.Result{
		ScrubID:      resp.ScrubId,
		AppliedIndex: resp.AppliedIndex,
		SHA256:       sha,
	}, nil
}
