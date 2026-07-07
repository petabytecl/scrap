# Restore-Marker Scan Record

Status: Accepted
Date: 2026-07-07

## Context

A restore marker is written whenever an evicted Block is brought back from the
Backend (or replaced from a peer after deep-scrub repair). The Content Scanner
uses the marker to keep restored Blocks scan-eligible below its durable
frontier, because eviction does not gate on scan state — a Block can be
evicted before its first scan and would otherwise slip under the watermark
forever.

Two defects followed from the marker being write-only (#454):

- The scanner's duplicate suppression (`completed` map) is process-local, so
  every restored Block below the frontier was rescanned once per process
  restart, forever, and inflated initial `LagBlocks` after every restart.
- No non-test code ever removed a restore marker, so marker files accumulated
  without bound across restore/evict cycles.

The marker cannot simply be deleted after a scan: eviction's hot-residency
guard reads `restored_at_us` to keep freshly restored Blocks resident, and the
eviction planner and validation paths read it too.

## Decision

- `RestoreMarker` gains an optional `scanned_at_us` field (marker version
  stays 1; older markers decode with the field zero). Zero means "restored,
  not yet scanned"; non-zero is a durable record that the Content Scanner
  completed a post-restore scan. A fresh restore rewrites the whole marker,
  resetting the field, so a non-zero value always refers to the most recent
  restore.
- `avscan` defines a new optional port, `ScanRecorder`, notified after a
  restored Block completes a scan. The Shard's scanner coordinator implements
  it by stamping the marker (`localblock.RecordRestoreScan`); failures are
  logged and non-fatal because a lost stamp only causes one extra rescan.
- The Shard's Block lister reports `Restored=true` only while the marker is
  present and unstamped, so a restored Block is rescanned exactly once per
  restore instead of once per restart. An unreadable marker keeps the Block
  eligible (rescan-safe direction).
- Eviction removes the restore marker after a successful `.blk` unlink
  (`localblock.RemoveRestoreMarker`), ending the restore lifecycle and
  bounding marker accumulation. Removal is best-effort: a marker leaked by a
  crash is stale but harmless and is rewritten by the next restore.
