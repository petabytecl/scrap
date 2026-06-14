---
title: 'Stabilize release-gate blockers (#438, #439, #441)'
type: 'bugfix'
created: '2026-06-14'
status: 'verified'
baseline_commit: '13d23dc6797dc3390679fba286ef54d839fe1574'
context:
  - '{project-root}/CONTEXT.md'
  - '{project-root}/_bmad-output/project-context.md'
  - '{project-root}/docs/go-style-guide.md'
  - '{project-root}/docs/adr/0026-multi-shard-v2-release-boundary.md'
---

<frozen-after-approval reason="human-owned intent - do not modify unless human renegotiates">

## Intent

**Problem:** The live GitHub issue queue has three release-gate blockers: unit-test flakes in `#441`, a product replica-convergence failure in `#439`, and Tier 3 evidence validation in `#438` blocked by the same unstable gates.

**Approach:** Remove deterministic race assumptions from the unit tests, add a product regression for followers whose current open Block is ahead of the leader replication init, fix the Shard replication convergence path without weakening fail-closed offset validation, then rerun the narrowest local gates before any higher Tier 2/Tier 3 evidence path.

## Boundaries & Constraints

**Always:** Preserve the glossary terms Document, Transaction, Block, Shard, Cell, and Member. Keep replication authority in `internal/shard`. Add or update failing tests before product changes where feasible. Use deterministic synchronization instead of sleeps, blind retries, or timeout inflation. Keep `#438` as validation evidence unless code or manifest defects appear while running the gate.

**Ask First:** If the fix requires changing Block/Frame layout, public/peer/admin protobuf contracts, Backend object identity, or relaxing authentication/authorization/encryption/rate-limit behavior, stop before editing those contracts.

**Never:** Do not skip, quarantine, or weaken release tests. Do not hide `replica block N is not open` or `replica offset` errors with retries that accept byte divergence. Do not claim `#438` complete without a current green Tier 3/evidence-bundle run or a clear blocked status.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|----------------------------|----------------|
| Invalid client-stream write metadata | Server returns `InvalidArgument` before all client sends complete | Test helper discovers final RPC status through `CloseAndRecv` when `Send` returns `io.EOF` | Non-status local send errors remain surfaced |
| Concurrent restore readers | One Backend restore is blocked while multiple readers enter `ReadDocument` | All readers complete from one Backend `GetObject` after release | Failure names the blocked read path without relying on a 25ms scheduling sleep |
| Restarted follower ahead of leader open Block | Follower has opened Block N+1; leader replicates valid data for Block N | Follower safely reopens the target Block only when its current open Block has no Document frames | Non-empty future Block or mismatched offset fails closed |
| Tier 3 validation | `#439` and `#441` fixes are present | Tier 2/Tier 3 commands can run to green or produce current blocker evidence | Do not mark release PASS on stale/local-only output |

</frozen-after-approval>

## Code Map

- `internal/server/server_test.go` -- client-streaming helper and metadata rejection tests for `#441`.
- `internal/shard/restore_test.go` -- concurrent restore synchronization and result waits for `#441`.
- `internal/shard/replication.go` -- follower-side AppendReplicatedDocument Block convergence for `#439`.
- `internal/shard/replication_test.go` -- regression tests around open Block rewind/advance and offset guard preservation.
- `internal/shard/blockfiles.go` -- Block writer lifecycle helpers if reopening the current empty Block requires shared code.
- `test/e2e/multishard_evidence_e2e_test.go` and `test/e2e/e2e_test.go` -- higher-level evidence tests affected by `#439`; read for context, avoid masking product failure.
- `.github/workflows/evidence-gate.yml`, `scripts/evidence-bundle.sh`, and `_bmad-output/implementation-artifacts/v2-release-tier-gates-evidence.md` -- `#438` validation path.

## Tasks & Acceptance

**Execution:**
- [x] `internal/server/server_test.go` -- make `writeDocumentWithMessages` continue to `CloseAndRecv` after server-side `io.EOF` from `Send` -- proves the expected gRPC status instead of the transport race.
- [x] `internal/shard/restore_test.go` -- replace the concurrent-restore scheduling sleep with deterministic synchronization or a bounded helper justified by state observation -- removes load-sensitive timing.
- [x] `internal/shard/replication_test.go` -- add regressions for a restarted follower that has opened an empty future Block and for a follower that is behind within the current Block -- captures `#439`.
- [x] `internal/shard/replication.go` / `internal/shard/replica_repair.go` / `internal/block/writer.go` -- implement safe follower Block convergence while preserving offset mismatch rejection -- fixes the product defect without accepting corrupt offsets.
- [x] `#438` validation -- run the available local/Tier evidence command or document the exact external blocker if hosted Tier 3 cannot run from this environment -- keeps release status honest.

**Acceptance Criteria:**
- Given invalid WriteDocument metadata, when the server closes the client stream early, then tests assert the final `InvalidArgument` status rather than an intermediate `EOF`.
- Given concurrent readers of one evicted Block, when one Backend restore is in flight, then all readers complete and only one Backend `GetObject` occurs.
- Given a follower with an empty future open Block, when the leader replicates the previous open Block at the expected offset, then the follower appends the Document to the target Block.
- Given a follower with non-empty future Block state or mismatched StartOffset, when replication arrives, then it fails closed with the existing divergence errors.
- Given local focused gates pass, when Tier 2/Tier 3 evidence is attempted, then `#438` is either closed with current artifact links or left open with current blocker evidence.

## Design Notes

The `#439` fix should be narrower than a general rollback feature. A restarted follower may legitimately have opened a new empty Block because startup scans local Block files and sets `nextBlockID` past sealed or open files. Reopening the leader's target Block is acceptable only if the follower has not written Document frames into the newer open Block. The existing StartOffset guard remains the byte-level authority after the target Block is open.

Followers can also restart behind the leader inside the same open Block. In that case the repair path fetches the leader's Block and index through the existing peer Block transfer contract, rejects indexed bytes at or beyond the requested replication `StartOffset`, trims any trailing unindexed leader-local bytes, verifies the replacement, reopens the Block writer, and only then rechecks the original StartOffset guard before appending.

## Verification

**Commands:**
- `GOCACHE=/tmp/scrap-go-build go test ./internal/server -run 'TestGRPCWriteRejectsInvalidMetadata' -count=100` -- pass.
- `GOCACHE=/tmp/scrap-go-build go test ./internal/shard -run 'TestAppendReplicatedDocument|TestReadDocumentJoinsConcurrentBlockRestore|TestReadDocumentSharedRestoreSurvivesLeaderReaderCancellation' -count=50` -- pass.
- `GOCACHE=/tmp/scrap-go-build go test ./internal/block -run 'TestBlockWriter|TestOpenWriter' -count=1` -- pass.
- `GOCACHE=/tmp/scrap-go-build go test ./internal/shard -run 'TestAppendReplicatedDocument' -count=1` -- pass.
- `GOCACHE=/tmp/scrap-go-build go test ./internal/block ./internal/server ./internal/shard ./internal/scrapctl/evidencebundle ./internal/cmd` -- pass.
- `GOCACHE=/tmp/scrap-go-build go test ./test/e2e -run '^$'` -- pass.
- `GOCACHE=/tmp/scrap-go-build go test ./internal/shard -run 'TestAppendReplicatedDocument|TestReadDocumentJoinsConcurrentBlockRestore|TestReadDocumentSharedRestoreSurvivesLeaderReaderCancellation|TestReadDocumentRestoreWaiterDeadlineDoesNotCancelSharedRestore|TestReadDocumentRestoreLeaderDeadlineFailsClosed' -count=20` -- pass.
- `GOCACHE=/tmp/scrap-go-build make tier1-check` -- pass; includes fmt, package boundaries, proto, lint, `go test ./...`, race, integration, build, and govulncheck.
- `GOCACHE=/tmp/scrap-go-build make tier2-e2e-up` -- pass; `TestE2EMultiShardRestartDeterminism` passed in 113.61s, `TestE2ELightScrubDetectsProjectionDivergence` remains skipped by documented scope.
- `GOCACHE=/tmp/scrap-go-build make tier3-evidence-up` -- pass; green bundle `evidence/throughput-20260614T013646Z-13d23dc`, gates `pass: true`, privacy scan `PASS` with 40 artifacts and 0 findings.
- `jq '{encrypted_write_read_ok, encrypted_backend_upload_ok, encrypted_restore_ok}' artifacts/prodlike-security/security-evidence.json` -- all true.
