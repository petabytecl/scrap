# Raft snapshot, log compaction, and WAL retention policy

Status: Accepted

Date: 2026-07-05

## Context

The Raft node loaded and received snapshots but never created one: it never
called `CreateSnapshot`, `Compact`, or `ReleaseLockTo`. `MemoryStorage` and
the on-disk WAL therefore grew without bound (eventual OOM and disk
exhaustion), every restart replayed the entire log, and a follower that fell
behind could never be caught up because there was no snapshot for the leader
to install (GitHub #443, DW-1).

Unlike etcd, the Shard's state machine is durably applied *outside* the Raft
log: every index write uses `pebble.Sync` and Block bytes are fsynced before
the commit command is proposed. A Raft snapshot therefore does not need to
carry state — the Member's own pebble index and Block files already hold
everything through the applied index.

## Decision

Every voter snapshots its own log and compacts locally, mirroring the etcd
raftexample lifecycle with these defaults (all overridable via
`raft.Config`):

- **Trigger** — after `MaxSnapCount` (default 10 000) entries applied since
  the last snapshot, the node calls the application `Snapshot` hook and
  persists a snapshot at the applied index. A failing hook only defers the
  snapshot; a persistence failure is fatal like any WAL durability failure.
- **Persist order** — snapshot file, then WAL snapshot record, then
  `ReleaseLockTo(index)`, so a WAL record never references a missing
  snapshot file.
- **Compaction window** — `MemoryStorage` retains `SnapshotCatchUpEntries`
  (default 5 000) entries behind the snapshot so a slightly lagging follower
  catches up via normal replication instead of an install-snapshot.
- **File retention** — after each snapshot the node best-effort purges old
  `.snap` and unlocked `.wal` files, keeping the newest five of each.
- **Snapshot payload** — the Shard supplies a small JSON manifest
  (`shard_id`, `member_hostname`, `member_id`, `created_at_us`), not state.
- **Restore semantics (fail closed)** — at restart, a Member restores its
  own manifest as a no-op: its durable local state already covers the
  snapshot index, and WAL replay continues from there. An install-snapshot
  carrying a *foreign* manifest means this Member fell behind the retention
  window (or started blank against a compacted leader log); the Shard
  refuses it with an explicit re-seed error instead of adopting an applied
  index its local store does not actually hold. Automated catch-up (manifest
  plus out-of-band Block transfer via the existing replica-repair path) is a
  follow-up; silent divergence is never acceptable.

The live install-snapshot path now also releases WAL locks, invokes the
`Restore` hook, and advances the applied/commit indexes, which the previous
(unreachable) code path did not.
