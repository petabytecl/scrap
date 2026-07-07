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
eviction planner and validation paths read it too. Nor can the scan state be
recorded as a new field inside the version-1 marker: marker readers reject
unknown JSON fields, so a binary rollback would make `Classify` fail on every
scanned-restored Block (review finding on PR #485).

## Decision

- The durable post-restore scan record is a **sidecar file**
  (`<block>.blk.restore.scanned.json`, `RestoreScanRecord`), not a marker
  field. The restore marker stays exactly version-1 shaped, so binaries that
  predate the record keep classifying Blocks after a rollback; they simply
  rescan restored Blocks per restart as before.
- The record carries the marker's `restored_at_us` as a generation binder: it
  suppresses rescans only while it matches the current marker. A fresh
  restore rewrites the marker with a new `restored_at_us`, so a record from
  an earlier restore generation never suppresses the scan of a later one, and
  no removal ordering between record and marker can produce a wrongly
  suppressed scan.
- `avscan` defines a new optional port, `ScanRecorder`, notified after a
  restored Block completes a scan. The Shard's scanner coordinator implements
  it by writing the record (`localblock.RecordRestoreScan`); failures are
  logged and non-fatal because a lost record only causes one extra rescan.
- The Shard's Block lister reports `Restored=true` only while the marker is
  present without a matching record (`localblock.RestorePendingScan`), so a
  restored Block is rescanned exactly once per restore instead of once per
  restart. An unreadable marker or record keeps the Block eligible
  (rescan-safe direction).
- Eviction removes the restore marker and the scan record after a successful
  `.blk` unlink (`localblock.RemoveRestoreMarker`), ending the restore
  lifecycle and bounding accumulation. Removal is best-effort: files leaked
  by a crash are stale but harmless (the generation binder ignores them) and
  are rewritten by the next restore.

Scan state deliberately binds to Block **content**, not to a restore
generation: Blocks are immutable and every restore/repair publish is verified
against committed authority (SHA-256/size/validation token), so any two
restores of the same Block ID carry identical bytes. The generation binder
exists to keep bookkeeping self-consistent across crash windows, not because
a later restore could contain different content.
