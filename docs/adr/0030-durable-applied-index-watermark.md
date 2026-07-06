# Durable applied-index watermark for raft snapshot restore

Status: Accepted

Date: 2026-07-05

## Context

Shard state is durably applied outside the Raft log: `pebble.Sync` writes to
the Pebble Projection and fsynced Block files. A Raft snapshot therefore
carries no state — only a manifest recording which Member's durable local
state it stands for (ADR 0029). `restoreRaftSnapshot` accepted a snapshot
whenever the manifest's `member_hostname`/`member_id` matched the local
Member, on the assumption that a self-created snapshot's covered entries were
already durable locally.

That assumption breaks under a partial DataDir restore. If a Member is
restored from backups where the Raft snapshot/WAL are newer than the Pebble
Projection (or Block files) but the PVC identity is preserved, Raft startup
loads the snapshot at index `S`, treats every entry `<= S` as already applied,
and calls `restoreRaftSnapshot`. The old identity-only check returned nil, so
the Member rejoined as healthy while the Document metadata for entries between
the projection's true durable point and `S` was never replayed — silent
divergence (Codex review P2 on PR #453; deferred item FR-5).

The gap existed because nothing durable recorded *how far the projection's
content had advanced*: the applied index lived only in an in-memory
`atomic.Uint64` used by the projection consistency check, reset to zero on
restart until replay.

## Decision

Persist a durable **applied-index watermark** in the Pebble Projection and use
it to gate self-snapshot restore.

- `internal/index` stores the watermark under the reserved key
  `\x00applied-index\x00` (8-byte big-endian), written with `pebble.Sync` by
  `PersistAppliedIndex`, loaded on `Open`, and read via `AppliedIndex`. The key
  is per-Member restore bookkeeping, so it is excluded from `StreamingHash`
  (the cross-Member projection hash) — otherwise every Member would hash
  differently and consistency checks would always diverge.

- The watermark is persisted at **raft snapshot creation**, not on every apply.
  `SnapshotFunc` now receives the snapshot's applied index; `raftSnapshotData`
  fsyncs the watermark before the Raft snapshot itself is persisted, so the
  watermark can only ever lead the Raft snapshot, never trail the content it
  certifies. The snapshot's covered entries are already durable in Pebble
  before the snapshot is taken, so recording the applied index at that point is
  safe.

- The manifest gains an `applied_index` field and its version bumps to 2.
  `restoreRaftSnapshot` accepts a self-created snapshot only when the
  projection's durable `AppliedIndex()` is `>=` the manifest's `applied_index`;
  a lower durable value means the projection was rolled back below the snapshot
  (partial restore) and the restore fails closed with re-seed (replica-repair)
  guidance. Version 1 manifests carry no applied index and keep the legacy
  accept-on-identity behavior so snapshots written by older builds still
  restore.

A freshly rebuilt projection starts with watermark 0 until the next snapshot
persists it; in that window a partial restore fails closed conservatively
(0 `<` any snapshot index), which is safe (over-eager re-seed, never silent
divergence).

## Consequences

- New reserved Pebble key `\x00applied-index\x00` in the projection (storage
  format change); excluded from the consistency hash.
- `raft.SnapshotFunc` signature changes to take the applied index.
- Snapshot manifest is now version 2 (`applied_index`); version 1 remains
  accepted on restore for backward compatibility.
- One extra fsync per snapshot creation (not per apply), so the steady-state
  write path is unaffected.
