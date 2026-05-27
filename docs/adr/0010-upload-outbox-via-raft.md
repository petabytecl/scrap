# Upload outbox via Raft commands

Status: Accepted

Date: 2026-05-27

## Context

When a Block seals, the shard leader must durably record the upload obligation and
track it through completion. This state must survive leader changes — a new leader
inheriting the shard must know which Blocks still need uploading. The upload outbox
is the mechanism that tracks this lifecycle.

S.C.R.A.P.'s metadata authority is Raft (CONTEXT.md safety invariant #5). The
projection (Pebble) is a derived, rebuildable view. Any state that must survive a
projection wipe-and-rebuild must originate from Raft.

## Decision

Two new `RaftCommand` variants track the upload lifecycle:

- **`SealBlock`**: proposed by the leader when a Block seals. Records the block ID,
  sealed size, and seal timestamp. This is the upload obligation.
- **`ConfirmUpload`**: proposed by the leader after uploading both `.blk` and `.idx`
  to the Backend and verifying via HEAD (size + ETag match). Records the block ID,
  backend key prefix, and confirmation timestamp.

The upload outbox is derived state: any committed `SealBlock` without a matching
`ConfirmUpload` is a pending upload. The projection materializes this as a
scannable key prefix for fast outbox enumeration by the upload processor.

On leader change, the new leader scans the materialized outbox and resumes uploads
from where the previous leader left off. Uploads are idempotent (same key, same
bytes), so partial uploads by the old leader are harmlessly re-uploaded.

Upload verification uses HEAD + size/ETag check after each upload. The leader only
proposes `ConfirmUpload` after verification passes. This prevents confirming an
upload that the Backend does not actually hold intact.

## Considered Options

- **Projection-only tracking** (upload state in Pebble keys, no Raft commands):
  simpler, but a projection wipe-and-rebuild (triggered by light scrub divergence
  detection, ADR 0004) would lose all upload state. The leader would not know which
  Blocks are uploaded vs. pending. Re-uploading everything is wasteful; assuming
  everything is uploaded is unsafe. Rejected — violates "Raft is the authority."
- **Separate upload WAL** (dedicated append-only log for upload state): survives
  projection wipe but adds a third durability layer with its own crash-recovery
  path. Unnecessary when Raft already provides durable, replicated, ordered state.
  Rejected — added complexity without benefit.
- **Raft commands** (chosen): upload state lives in the same authority as all other
  shard state. Replay-safe, leader-change-safe, projection-rebuild-safe. Two
  additional Raft entries per sealed Block (~100 bytes each) are negligible
  overhead.
