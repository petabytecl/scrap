# Install-snapshot crash safety and send-outcome reporting

Status: Accepted

Date: 2026-07-07

## Context

ADR 0029 introduced snapshot creation, compaction, and the live
install-snapshot path. A review of that path against the etcd contract
(GitHub #462) found four crash windows that turned routine follower lag into
a bricked or silently degraded voter:

1. The Ready loop saved WAL entries/HardState **before** persisting an
   incoming snapshot. A crash between the two left the WAL referencing a
   snapshot that never hit disk — `RestartNode` panics on every startup.
2. Recovery loaded the newest `.snap` file (`snap.Load`) and then opened the
   WAL at that index. The snap file is written before the WAL snapshot
   record, so a crash between the two orphaned the newest `.snap` and
   `wal.Open` failed with `ErrSnapshotNotFound` forever.
3. An install-snapshot was durably persisted (snap file, WAL record,
   `ReleaseLockTo`, `MemoryStorage`) **before** the application `Restore`
   hook could reject it. ADR 0029's fail-closed Restore (a foreign manifest
   means the Member must be re-seeded) then panicked *after* the snapshot was
   authoritative — a crash loop from disk at every startup, with no re-seed
   path short of manual surgery.
4. Nothing called `ReportSnapshot`/`ReportUnreachable`. The peer transport is
   fire-and-forget (bounded buffer, reconnecting stream), so a dropped
   `MsgSnap` left the leader's Progress for that follower parked in
   `StateSnapshot` indefinitely — the cell silently ran one durable replica
   short.

## Decision

- **Persist order for an incoming snapshot (etcd contract):** within one
  Ready, validate with the application `Restore` hook first, then persist
  the snap file, then the WAL snapshot record, then `ReleaseLockTo`, and only
  then `wal.Save(HardState, Entries)`. The in-memory switch
  (`MemoryStorage.ApplySnapshot`, applied/commit indexes) happens after the
  WAL save.
- **Restore validates before adoption.** A Restore rejection is still fatal
  (silent divergence is never acceptable), but nothing has been persisted
  yet, so the Member restarts at its previous durable state instead of
  crash-looping from disk. Consequence: `Restore` may be invoked more than
  once for the same snapshot (crash or leader retry) and must be idempotent.
  The Shard's manifest-validation Restore already is.
- **Recovery trusts the WAL, not the snap directory.** Restart uses
  `wal.ValidSnapshotEntries` + `snap.LoadNewestAvailable`, so orphaned
  `.snap` files and WAL snapshot records beyond the recorded commit (both
  legitimate crash artifacts of the persist order above) are skipped and the
  node boots from the last consistent state.
- **Transports report send outcomes.** `raft.StatusReporter`
  (`ReportUnreachable`, `ReportSnapshotFailure`, `ReportSnapshotSuccess`) is
  implemented by `raft.Node`; `raft.Open` registers it with any Transport
  implementing `raft.ReporterSink`. The peer transport reports every dropped
  raft message (buffer-full, drain-on-reconnect, failed stream send, dial or
  marshal failure) and reports snapshot delivery, so a dropped `MsgSnap`
  makes the leader retry instead of waiting forever.

The lagging-follower harness (`internal/raft/install_snapshot_test.go`)
drives a real 3-node install-snapshot lifecycle, and per-window regression
tests pin each crash artifact (`install_snapshot_crash_test.go`).
