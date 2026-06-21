---
baseline_commit: 3968fc0
---

# Story 3.8: Make Scrub Coordination Concurrency Deterministic

Status: done

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a storage operator,
I want scrub coordination to behave deterministically under duplicate and overlapping requests,
so that integrity verification and repair workflows cannot hang or lose results.

## Acceptance Criteria

1. **AC-3.8.1 - Duplicate scrubID cannot hang the first waiter.** Given a duplicate `scrubID`, when a second consistency check is proposed, then behavior is deterministic and the first waiter cannot hang indefinitely. Evidence records the duplicate-ID regression test.
2. **AC-3.8.2 - Overlapping results remain retrievable by ID.** Given overlapping scrubs with different IDs, when results apply, then each result remains retrievable by ID for the defined retention window or is explicitly rejected with a documented policy. Evidence records cache behavior by scrub ID.
3. **AC-3.8.3 - No mutex held across a blocking send.** Given `applyConsistencyCheck` notifies a waiter, when the send occurs, then the coordinator does not hold the mutex across a potentially blocking send. Evidence records a deterministic lock/send regression test.
4. **AC-3.8.4 - Deterministic synchronization in tests.** Given concurrency tests run, when cancellation, timeout, cleanup, and race-sensitive paths are exercised, then tests use channels, contexts, or bounded polling, not sleeps. Evidence records the synchronization strategy.
5. **AC-3.8.5 - Race-clean shard package.** Given the scrub coordinator fix is complete, when verification runs, then `go test ./internal/shard/...` and `go test -race ./internal/shard/...` pass. Evidence links both commands.

## Traceability

- Epic: Epic 3 - Backend Upload, Eviction, Restore, and Cold Reads (scrub/repair coordination).
- Requirements: FR-3 (all-or-error reads and corruption handling), FR-6 (backend upload/pressure), FR-8 (restore-first cold reads), NFR-1 (fail-closed storage behavior), NFR-7 (test coverage by risk), NFR-8 (data-integrity blocker discipline).
- Release policy: any confirmed or plausible data-integrity defect is release-blocking until fixed or explicitly disproven with current evidence (NFR-8). A scrub coordinator that can hang or silently drop integrity results is a data-integrity blocker.
- Governing ADRs: `docs/adr/0002-dual-checksum-architecture.md`, `docs/adr/0003-mirror-block-layout.md`, ADR 0014 (Raft metadata authority) as referenced by FR-3.
- Prerequisites: Stories 3.1 through 3.7 are done (upload confirmation, admission pressure, eviction, restore-first cold reads, restore failure semantics, encryption-compatible restore, durability/cold-read closure). The scrub coordinator already exists and is wired; this story hardens its concurrency.
- Non-goals: no Raft command/wire change, no proto/gen edits, no change to scrub scheduling cadence (light/deep intervals), no change to projection hashing under `Shard.mu`, no change to the `scrub.ResultCache` / `scrub.Proposer` / `scrub.LeaderChecker` interface shapes, no Backend or repair-path behavior change.

## Tasks / Subtasks

- [x] Make duplicate in-flight `scrubID` deterministic. (AC: 1)
  - [x] In `internal/shard/scrub_coordinator.go` `ProposeConsistencyCheck`, before inserting into `c.proposals`, check under `c.mu` whether `scrubID` already has an in-flight proposal channel.
  - [x] If a proposal for `scrubID` is already in flight, return a typed sentinel error (`ErrScrubInProgress`) without overwriting the existing channel, so the first waiter keeps its channel and still receives its result on apply.
  - [x] Defined `var ErrScrubInProgress = errors.New("shard: consistency check already in progress")`. Raw `scrubID` is NOT embedded (redaction).
  - [x] Preserved sequential reuse: a `scrubID` reused after the prior proposal completed (entry deleted on delivery/cancel) still works.

- [x] Retain overlapping results by ID with a bounded retention window. (AC: 2)
  - [x] Replaced the single `scrubResult *scrub.Result` field with `results map[string]scrub.Result` plus `resultOrder []string` for bounded FIFO retention.
  - [x] Added `const maxRetainedScrubResults = 16` (bounded memory per NFR-1; oldest evicted first), documented in a code comment.
  - [x] Added `storeResultLocked(result scrub.Result)` that inserts/updates by `ScrubID` and evicts the oldest ID when the cap is exceeded; re-applying the same ID updates in place without reordering.
  - [x] `GetScrubResult` reads from the `results` map under `RLock`.
  - [x] `results` initialized in `newScrubCoordinator`.

- [x] Remove the mutex-across-send hazard. (AC: 3)
  - [x] `applyConsistencyCheck` now stores the result, captures the waiter channel, and deletes it under `c.mu.Lock()`, then `Unlock`s BEFORE sending.
  - [x] The channel send (`ch <- result`) happens after the lock is released, only when `ch != nil`.
  - [x] Proposal channel kept buffered (cap 1); lock discipline does not rely on the buffer.

- [x] Add deterministic concurrency regression tests in `internal/shard`. (AC: 1, 2, 3, 4)
  - [x] `TestScrubCoordinatorDuplicateScrubIDDoesNotHangFirstWaiter` (channels/contexts, no sleeps).
  - [x] `TestScrubCoordinatorRetainsOverlappingResultsByID`.
  - [x] `TestScrubCoordinatorRetentionEvictsOldestBeyondCap`.
  - [x] `TestScrubCoordinatorApplyDoesNotHoldLockDuringSend` (unbuffered channel + bounded `time.After` failure timeout, no sleeps).
  - [x] Existing tests still pass: `TestScrubCoordinatorProposeApplyAndCache`, `TestScrubCoordinatorContextCancelRemovesProposal`, `TestScrubCoordinatorApplyAfterStopIsSafe`.

- [x] Verification and story evidence. (AC: 5)
  - [x] Ran `go test ./internal/shard/... -run Scrub -count=1` (red before on retention, green after).
  - [x] Ran `go test ./internal/shard/... -count=1`.
  - [x] Ran `go test -race ./internal/shard/... -count=1`.
  - [x] Ran `make package-boundaries`, `go vet ./internal/shard/...`, and `git diff --check`.
  - [x] Recorded exact commands and results in the Debug Log.

## Dev Notes

### Current State (files being modified)

`internal/shard/scrub_coordinator.go` — `scrubCoordinator` owns proposal tracking, result caching, and scrubber lifecycle. Projection hashing stays under `Shard.mu`; the coordinator has its own `sync.RWMutex`.

Three concurrency defects, each mapped to an AC:

1. **AC-3.8.1 — duplicate scrubID orphans the first waiter.** `ProposeConsistencyCheck` does `c.proposals[scrubID] = doneCh` unconditionally:
   ```go
   doneCh := make(chan scrub.Result, 1)
   c.mu.Lock()
   c.proposals[scrubID] = doneCh   // overwrites any existing waiter for scrubID
   c.mu.Unlock()
   ```
   A second propose with the same `scrubID` overwrites the first channel. `applyConsistencyCheck` then delivers to the latest channel and deletes the entry, so the first waiter never receives — it hangs until its own `ctx` cancels/times out. Non-deterministic and can hang.

2. **AC-3.8.2 — overlapping results lost.** The cache is a single pointer:
   ```go
   scrubResult *scrub.Result
   ...
   func (c *scrubCoordinator) GetScrubResult(scrubID string) (scrub.Result, bool) {
       if c.scrubResult == nil || c.scrubResult.ScrubID != scrubID { return scrub.Result{}, false }
       return *c.scrubResult, true
   }
   ```
   Two overlapping scrubs with different IDs: the second apply overwrites `scrubResult`, so `GetScrubResult(firstID)` returns false — the first result is lost with no documented policy.

3. **AC-3.8.3 — mutex held across send.** `applyConsistencyCheck` sends while holding the write lock:
   ```go
   c.mu.Lock()
   c.scrubResult = &result
   if ch, ok := c.proposals[result.ScrubID]; ok {
       ch <- result            // blocking send while holding c.mu
       delete(c.proposals, result.ScrubID)
   }
   c.mu.Unlock()
   ```
   The channel is buffered (cap 1) so it does not block today, but holding the mutex across a send is the exact hazard the AC forbids; an unbuffered or full channel would deadlock the coordinator.

### Call sites and contracts (must preserve)

- `applyConsistencyCheck` is invoked from the single-threaded Raft apply loop (`internal/shard/apply.go:149`, `case *scrapv1.RaftCommand_ConsistencyCheck`). `ProposeConsistencyCheck`, `GetScrubResult`, and `removeProposal` run on RPC goroutines — the `RWMutex` mediates them.
- `GetScrubResult` is the `scrub.ResultCache` interface method (`internal/scrub/result.go`): `GetScrubResult(scrubID string) (Result, bool)`. The interface is unchanged by this story; only the backing store changes from a single pointer to a bounded map.
- `scrub.Result` is `{ ScrubID string; AppliedIndex uint64; SHA256 [32]byte }`. Plain value type — safe to store by value in a map.
- `internal/shard/shard.go:953` exposes `Shard.ProposeConsistencyCheck` / `GetScrubResult` as thin delegators. The peer `ConsistencyCheck` RPC (`internal/peer/server.go`) and admin/`scrapctl` diagnostics read results via `GetScrubResult`, so retaining results by ID matters for cross-peer result retrieval.
- `Stop()` intentionally leaves proposal channels open so an in-flight apply after `Stop` can still deliver to a buffered channel (see its doc comment). Preserve this — the buffered cap-1 channel and post-stop apply behavior (`TestScrubCoordinatorApplyAfterStopIsSafe`) must keep working.

### What Must Be Preserved

- `internal/shard` ownership of follower apply, projection authority, and scrub lifecycle. No transport/proto/Raft-command changes.
- The `scrub.ResultCache`, `scrub.Proposer`, `scrub.LeaderChecker` interface assertions at the bottom of `scrub_coordinator.go` must still hold.
- Projection hashing remains under `Shard.mu` (`internal/shard/projection.go:scrubProjectionResult`); do not move it under the coordinator mutex.
- Redaction: error messages and logs must not embed raw `scrubID` values or other raw identifiers (NFR-4 redaction discipline, consistent with the rest of the codebase).
- Bounded memory (NFR-1): the new result cache must be bounded (the `maxRetainedScrubResults` cap), never an unbounded map.

### Implementation Guidance

- Prefer the minimal struct change: drop `scrubResult *scrub.Result`, add `results map[string]scrub.Result` and `resultOrder []string`.
- `storeResultLocked` pattern (called only while holding `c.mu`):
  ```go
  func (c *scrubCoordinator) storeResultLocked(result scrub.Result) {
      if _, exists := c.results[result.ScrubID]; !exists {
          c.resultOrder = append(c.resultOrder, result.ScrubID)
          for len(c.resultOrder) > maxRetainedScrubResults {
              oldest := c.resultOrder[0]
              c.resultOrder = c.resultOrder[1:]
              delete(c.results, oldest)
          }
      }
      c.results[result.ScrubID] = result
  }
  ```
- `applyConsistencyCheck` after fix:
  ```go
  result := c.core.scrubProjectionResult(cc.GetScrubId(), entryIndex)
  c.mu.Lock()
  c.storeResultLocked(result)
  ch := c.proposals[result.ScrubID]
  delete(c.proposals, result.ScrubID)
  c.mu.Unlock()
  if ch != nil {
      ch <- result
  }
  ```
- Keep the proposal channel buffered cap-1 in `ProposeConsistencyCheck`.
- Use `go test -race` to catch any residual data race; the apply loop and RPC goroutines share `c.mu`.

### Testing Requirements

- Standard `testing`; no assertion libraries; no `time.Sleep` for synchronization (AC-3.8.4). Use channels, `context`, and `select`+`time.After` as bounded failure timeouts only.
- Same-package (`package shard`) tests so white-box access to `c.proposals`, `c.mu`, and `c.results` is available, matching the existing `scrub_coordinator_test.go` style.
- Reuse `scrubCoordinatorCoreStub`, `readProposedScrubCommand`, and `scrubCoordinatorResult` already defined in `scrub_coordinator_test.go`.
- Suggested verification:

```sh
go test ./internal/shard/... -run Scrub -count=1
go test ./internal/shard/... -count=1
go test -race ./internal/shard/... -count=1
make package-boundaries
git diff --check
```

Note: this workspace has a Go toolchain skew (`GOROOT` pinned to 1.26.1 while the active toolchain is 1.26.4). Run gates with `env -u GOROOT` if the default `go` reports `compile: version ... does not match go tool version`.

### Previous Story Intelligence

- Story 2.8 (just completed) reinforced: fix the root cause minimally, mirror existing patterns, keep client/peer-driven errors typed and redacted, and add deterministic regression tests that fail before the fix. CONTEXT.md lesson on not holding locks across potentially blocking channel sends directly motivates AC-3.8.3.
- Git history shows scrub/replication fixes land as focused red/green tests plus evidence updates (`8f4dce8`, `c9caa90` touched `internal/shard/scrub_coordinator.go`).

## References

- `_bmad-output/planning-artifacts/epics.md` - Story 3.8 acceptance criteria (lines 1142-1180).
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-3 (236), FR-6 (294), FR-8 (321), NFR-1 (507), NFR-7, NFR-8 (529).
- `internal/shard/scrub_coordinator.go` - `ProposeConsistencyCheck`, `applyConsistencyCheck`, `GetScrubResult`, `removeProposal`, struct fields (the fix site).
- `internal/shard/apply.go:149` - Raft apply dispatch into `applyConsistencyCheck`.
- `internal/shard/shard.go:953` - `Shard` delegators.
- `internal/shard/projection.go:19` - `scrubProjectionResult` under `Shard.mu`.
- `internal/scrub/result.go` - `Result` struct and `ResultCache` interface.
- `internal/shard/scrub_coordinator_test.go` - existing tests and stubs to reuse.
- `internal/peer/server.go` - peer `ConsistencyCheck` RPC reads `GetScrubResult`.
- `CONTEXT.md` - concurrency lesson: do not hold locks across blocking sends.

## Dev Agent Record

### Agent Model Used

Cascade (dev-story workflow, autonomous mode).

### Debug Log References

- RED: `env -u GOROOT GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard/... -run 'TestScrubCoordinatorRetainsOverlappingResultsByID' -count=1` → FAIL (`scrub-a result ... ok=false`), proving the single-pointer cache lost overlapping results (AC-3.8.2).
- GREEN: after replacing the cache with a bounded per-ID map, adding duplicate-ID rejection, and moving the send outside the lock: `go test ./internal/shard/... -run Scrub -count=1` → `ok`.
- PASS: `go test ./internal/shard/... -count=1` → `ok` (79.6s).
- PASS: `go test -race ./internal/shard/... -count=1` → `ok` (73.0s) on re-run. NOTE: one earlier `-race` run failed on `TestWriteDocumentAckAfterPeerReplicationRaftApplyAndVisibility` (an unrelated write/replication test that issues no consistency checks); it passed 3/3 in isolation under `-race` and the full suite passed on re-run, so it is a pre-existing load/timing flake, not a regression from this change.
- PASS: `make package-boundaries` → clean; `go vet ./internal/shard/...` → clean; `git diff --check` → clean.
- ENV: ran all gates with `env -u GOROOT` to work around the workspace Go toolchain skew (GOROOT pinned to 1.26.1, active toolchain 1.26.4).

### Completion Notes List

- Fixed three concurrency defects in `internal/shard/scrub_coordinator.go`, each mapped to an AC:
  - AC-3.8.1: `ProposeConsistencyCheck` now returns `ErrScrubInProgress` for a duplicate in-flight `scrubID` instead of overwriting the first waiter's channel, so the first waiter can no longer be orphaned/hung.
  - AC-3.8.2: replaced the single `scrubResult *scrub.Result` with a bounded `results map[string]scrub.Result` (+ `resultOrder` FIFO, cap `maxRetainedScrubResults = 16`); overlapping results are now retrievable by ID until evicted by the documented retention policy.
  - AC-3.8.3: `applyConsistencyCheck` captures the waiter channel and deletes the proposal under the lock, then releases the lock before the channel send.
- Added four deterministic regression tests using channels/contexts/bounded timeouts (no sleeps), satisfying AC-3.8.4. The lock-discipline test uses an unbuffered channel so it would hang (and fail via bounded timeout) under the old lock-across-send code.
- Preserved: `scrub.ResultCache`/`Proposer`/`LeaderChecker` interface assertions, projection hashing under `Shard.mu`, post-Stop apply safety, buffered cap-1 proposal channels, and redaction (no raw scrub IDs in errors).
- Pre-existing observation (out of scope): `TestWriteDocumentAckAfterPeerReplicationRaftApplyAndVisibility` is a load/timing-sensitive flake under full-package `-race`. Worth a future stabilization pass; not introduced here.

### File List

- `internal/shard/scrub_coordinator.go`
- `internal/shard/scrub_coordinator_test.go`
- `_bmad-output/implementation-artifacts/3-8-make-scrub-coordination-concurrency-deterministic.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

## Senior Developer Review (AI)

- **Reviewer:** Cascade (adversarial review, autonomous mode)
- **Date:** 2026-06-21
- **Outcome:** Approve. 0 Critical, 0 High, 0 Medium. 2 Low (no action).
- **AC validation:** AC-3.8.1 (duplicate-ID returns `ErrScrubInProgress`, first waiter completes), AC-3.8.2 (bounded per-ID `results` map, retrievable by ID, FIFO eviction at cap 16), AC-3.8.3 (lock released before waiter send, proven by unbuffered-channel test), AC-3.8.4 (tests use channels/contexts/bounded `time.After`, no sleeps), AC-3.8.5 (`go test` and `-race` pass) — all IMPLEMENTED and test-backed.
- **File List vs git:** exact match; no undocumented or phantom changes.
- **Caller-impact check:** `ProposeConsistencyCheck` is invoked only by the light-scrub scheduler (`internal/scrub/light.go:119`), which mints a unique ULID scrub ID per run, so `ErrScrubInProgress` is defensive and never reaches an operator RPC with operator-supplied IDs; the scheduler already handles proposal errors via warn-log + error metric. No gRPC status-code mapping gap.
- **Concurrency check:** single `sync.RWMutex` mediates apply-loop vs RPC goroutines; `-race` clean. `storeResultLocked` is only called while holding the write lock.
- **Action Items:** none.
- **Low (no action):**
  - [Low] `c.resultOrder = c.resultOrder[1:]` on eviction is the standard slice-as-queue pattern; memory stays bounded (the backing array reallocates on append). Acceptable; a ring buffer would be marginally tidier.
  - [Low] Pre-existing `-race` flake in `TestWriteDocumentAckAfterPeerReplicationRaftApplyAndVisibility` under full-package load (unrelated to this story; passed in isolation and on full re-run). Flagged for future stabilization.

## Change Log

- 2026-06-21: Created Story 3.8 from sprint backlog; identified three concurrency defects in `scrubCoordinator` (duplicate-ID waiter orphaning, single-result cache loss, mutex-across-send) mapped to AC-3.8.1/2/3; set status to ready-for-dev.
- 2026-06-21: Implemented all three fixes with four deterministic regression tests; `go test ./internal/shard/...`, `-race`, `package-boundaries`, `vet`, and `git diff --check` all green. Status → review.
- 2026-06-21: Adversarial code review — Approve (0 Critical/High/Medium, 2 acknowledged Low). Verified ACs, caller impact, and race-cleanliness. Status → done.
