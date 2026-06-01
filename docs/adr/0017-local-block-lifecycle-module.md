# Local Block Lifecycle module

Status: Accepted

Date: 2026-06-01

## Context

ADR 0016 defines Phase 4 local eviction as per-Member lifecycle state. The
marker files, startup classification, restore marker, and stale marker cleanup
describe whether one local Block copy is hot, evicted, cleanup-needed, or
unexpectedly lost. Those facts are filesystem state, not Raft metadata authority.

The first implementation put this behavior in `internal/shard`. That made the
Shard package own both Raft/Projection authority and local file-state
classification. It also forced `internal/peer` to import `internal/shard` just to
read an eviction marker during `TransferBlock`, even though peer transfer should
not depend on Shard internals.

## Decision

Local Block Lifecycle marker files and classification live in
`internal/localblock`.

`internal/localblock` owns:

- marker version constants and marker JSON read/write validation;
- eviction and restore marker path conventions;
- startup classification from `.blk`, `.idx`, and lifecycle markers;
- crash-safe marker publication using temp file, file sync, rename, and directory
  sync;
- stale hot-copy eviction marker cleanup; and
- eviction marker filename parsing for local scans.

`internal/shard` remains the owner of policy and authority: candidate planning,
eviction apply, restore orchestration, health snapshots, and Raft/Projection
state. `internal/shard/block_lifecycle.go` keeps a thin compatibility surface for
existing Shard tests while production callers move to `internal/localblock`.

`internal/peer` may import `internal/localblock` to classify or read local
lifecycle markers for `TransferBlock`. It must not import `internal/shard` for
marker details.

## Consequences

Positive:

- Local Block Lifecycle has a small interface with direct tests.
- Marker JSON and classification rules have one implementation instead of being
  implied by Shard callers.
- Peer transfer no longer depends on Shard internals for local marker state.

Negative:

- `internal/localblock` depends on the Block filename package for `.blk` and
  `.idx` paths.
- The temporary compatibility surface in `internal/shard` should be retired when
  the remaining Shard tests are updated to import `internal/localblock`
  directly.
