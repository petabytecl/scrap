# Epic 1 Evidence Rollup

Date: 2026-06-11

## Release-Gate Summary

Epic 1 status: CONCERNS.

Runtime evidence status: PASS.

- PASS: Story 1.1 Durable Document Write ACK. Evidence: `_bmad-output/implementation-artifacts/1-1-durable-document-write-ack.md` records focused server/shard gates, shard race gates, and full `make check` passes after ACK and review-patch work.
- PASS: Story 1.2 Immutable Replay and Conflict Handling. Evidence: `_bmad-output/implementation-artifacts/1-2-immutable-replay-and-conflict-handling.md` records red/green exact replay and conflict tests, server/shard package gates, race gates, package-boundary checks, and full `make check` passes.
- PASS: Story 1.3 Verified Read and Metadata Inspection. Evidence: `_bmad-output/implementation-artifacts/1-3-verified-read-and-metadata-inspection.md` records red/green cancellation and corruption tests, block/index/server/shard gates, race gates, package-boundary checks, and full `make check` passes.
- PASS: Story 1.4 Transaction-Scoped Document Discovery. Evidence: `_bmad-output/implementation-artifacts/1-4-transaction-scoped-document-discovery.md` records red/green discovery cancellation tests, index/server/shard gates, race gates, package-boundary checks, leak-scan evidence, and full `make check` passes.
- PASS: Story 1.5 Core Gateway Restart and Rebuild Evidence. Evidence: `_bmad-output/implementation-artifacts/1-5-core-gateway-restart-and-rebuild-evidence.md` records current restart, rebuild, rebuilding-status, package, race, boundary, leak-scan, and full-check gates.
- CONCERNS: GitHub issue linkage is still not assigned in the BMAD story artifact. Owner: release owner before implementation PR. This is tracker hygiene, not missing P0 runtime evidence.
- FAIL: none.

## Current Story 1.5 Evidence

- RED: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server -run 'Rebuilding'` failed before implementation because all public DocumentService methods mapped rebuild-in-progress Store errors to `Internal`.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard ./internal/server -run 'Restart|Rebuild|Rebuilding'` passed after the central mapper fix and restart/rebuild characterization tests.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/server ./internal/shard` passed.
- FAIL then PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/server ./internal/shard` had one noisy combined `internal/shard` failure without extracted failure detail; isolated shard race and final combined rerun passed.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries` passed.
- FAIL then PASS: `env GOCACHE=/tmp/scrap-v2-go-build make check` first failed only on lint shape, then passed after helper cleanup and mapper complexity reduction.

## Boundary Summary

- `internal/store/errors.go`: added a stable unavailable reason for projection rebuild state. Store remains a domain package and still has no gRPC dependency.
- `internal/server/server.go`: central Store error mapping now maps rebuild-in-progress to public `Unavailable` with stable ErrorInfo details.
- `internal/server/restart_rebuild_test.go`: added public gRPC evidence for rebuild status mapping and a registered real Shard-backed restart test.
- `internal/shard/restart_rebuild_test.go`: added direct Shard restart, exact replay, conflict, stale Projection rebuild, and corrupt metadata fail-closed evidence.
- `_bmad-output/implementation-artifacts/1-5-core-gateway-restart-and-rebuild-evidence.md`: records Story 1.5 development evidence.
- `_bmad-output/implementation-artifacts/epic-1-evidence-rollup.md`: records this Epic 1 closure rollup.

## Routing And Privacy

- Storage identity remains `(transaction_id, document_name)`.
- `tenant_id` remains validation-only for this story and is not part of storage identity, Projection keys, block metadata, Backend keys, telemetry identity, or response metadata.
- Story 1.5 adds no multi-Shard router, Shard map, route cache, cross-Shard scan, or public routing response. Epic 2 still owns multi-Shard routing by Transaction.
- No new deployed log, metric, trace, or public response field was added for raw Document identity, Backend object keys, local storage paths, request IDs, trace IDs, or Shard internals.
