package shard_test

import (
	"context"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
)

// noopTestReplicator satisfies the multi-member replicator requirement for
// tests that exercise leadership, readiness, or scrub behavior without
// modeling byte replication. It acknowledges every replication with the
// declared SHA-256 so quorum always passes.
type noopTestReplicator struct{}

func (noopTestReplicator) ReplicateDocument(_ context.Context, _ string, init *scrapv1.ReplicateDocumentInit, _ [][]byte) ([]byte, error) {
	return init.GetSha256(), nil
}
