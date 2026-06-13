---
title: 'Fix flaky multi-member E2E: upload Shard-scoping + failover convergence gate (#437)'
type: 'bugfix'
created: '2026-06-13'
status: 'in-progress'
baseline_commit: 'b3fd781894914049df7e0a72c1ba9c514c41eaa8'
context:
  - '{project-root}/CONTEXT.md'
  - '{project-root}/_bmad-output/project-context.md'
  - '{project-root}/docs/go-style-guide.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** Two `test/e2e/` tests fail intermittently under CI load due to **test-side** wait/assertion bugs, not product defects. `TestE2EBackendUploadHappyPath` waits on a **pod-aggregated** `upload_pending_blocks` that sums all local Shards (the prod-like Cell hosts Shards 7 **and** 9 on every Member), so the *other* Shard's pending uploads keep the count `> 0` and the test times out. `TestE2ELeaderFailover` fires its canary write the moment the replacement pod is **K8s-Ready** — before Shard replication has converged — so it hits `ResourceExhausted: replica block N is not open`.

**Approach:** Replace both weak signals with **deterministic, Shard-scoped readiness checks** built on the existing diagnostics API and E2E helpers. No timeout/retry/sleep inflation. The third failure (`TestE2EMultiShardRestartDeterminism` offset mismatch) is a **suspected product convergence bug** and is split into a separate investigation issue — explicitly out of scope here.

## Boundaries & Constraints

**Always:** Use deterministic readiness checks derived from per-Shard diagnostics (leader_state resolved, readiness `ready`, Shard-scoped pending). Keep code changes inside `test/e2e/`. Resolve the test's target Shard via the existing `e2eTwoShardPlacement` / route lookup. Follow `docs/go-style-guide.md` and use glossary terms (Shard, Member, Block, quorum) exactly.

**Ask First:** If the `/healthz` `shard_diagnostics` JSON does **not** already expose per-Shard `upload_pending_blocks`, HALT before touching any `internal/` product code to add it. If a defensible fix appears to require retrying past a fail-closed replication guard (`block not open` / offset) on the write path, HALT.

**Never:** Do not "fix" flakiness by raising timeouts, adding sleeps, or adding blind retries. Do not skip/quarantine/weaken any test. Do not change `TestE2EMultiShardRestartDeterminism` behavior or mask replica divergence. Do not change the pod-level aggregate in `internal/cmd/app.go`.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Upload happy path, 2-Shard Cell | Test tx on Shard A; Shard B has unrelated pending uploads | Wait returns when **Shard A** pending == 0, ignoring Shard B | Fail-closed with Shard-scoped diagnostics dump on timeout |
| Failover canary, not yet converged | Replacement pod K8s-Ready but target Shard replication lagging | Gate blocks until target Shard reports resolved leader + `ready`, then canary write | Fail-closed (`t.Fatalf`) if convergence never reached in window |
| Failover canary, converged | Target Shard ready across Cell | Canary write proceeds and succeeds | Existing `tryWriteDocE2E` retry path unchanged |

</frozen-after-approval>

## Code Map

- `test/e2e/upload_e2e_test.go` -- `TestE2EBackendUploadHappyPath` (40-62), `waitUploadPendingBlocks` (535-546), `uploadHealth` struct (527-533). Primary target of the Shard-scoping fix.
- `test/e2e/multishard_evidence_e2e_test.go` -- reuse `fetchShardDiagnosticsWithResolvedLeaders` (152-167), `shardLeaderStatesResolved` (136-146), `assertShardDiagnosticBounded` readiness logic (244-262), `e2eShardDiagnostic` struct. May extend the struct with a per-Shard pending-uploads field.
- `test/e2e/e2e_test.go` -- `TestE2ELeaderFailover` (226-249), `waitForCellWriteQuorumForTransaction` / `waitForCellWriteQuorumWithTransaction` (251-268). Target of the convergence-gate fix.
- `internal/admin/shard_diagnostics.go:51` -- product per-Shard `upload_pending_blocks`. Read-only: confirm it is present in the `/healthz` `shard_diagnostics` JSON before wiring the test to it.
- `internal/cmd/app.go:279-294` -- pod-level aggregate that is the root cause of the upload flake. Do **not** change.

## Tasks & Acceptance

**Execution:**
- [x] `internal/admin/shard_diagnostics.go` -- (read-only) confirmed per-Shard `upload_pending_blocks` is serialized into the `/healthz` `shard_diagnostics` payload (`shard_diagnostics.go:51`). No product change needed; Ask-First gate cleared.
- [x] `test/e2e/multishard_evidence_e2e_test.go` -- added `UploadPendingBlocks int json:"upload_pending_blocks"` to `e2eShardDiagnostic`.
- [x] `test/e2e/upload_e2e_test.go` -- added `e2eShardIDForTransaction`, `findShardDiagnostic`, `waitShardUploadPendingBlocks`, `waitForShardConvergence`, `shardReadyAcrossCell`; replaced the pod-aggregate `waitUploadPendingBlocks(leader, 0, ...)` in `TestE2EBackendUploadHappyPath` with the Shard-scoped wait. Reused `uploadE2ETimeout` (no increase).
- [x] `test/e2e/e2e_test.go` -- in `TestE2ELeaderFailover`, added `waitForShardConvergence(t, e2eShardIDForTransaction(t, txID), failoverConvergeTimeout)` before the canary write; added `failoverConvergeTimeout` const. No retry past fail-closed guards.
- [x] GitHub -- filed investigation issue #439 for the `TestE2EMultiShardRestartDeterminism` `replica offset mismatch` (suspected product replication-convergence bug); labels `bug,e2e,production-readiness,v2,needs-triage`; references #437 and #438.
- [ ] Verify both tests on the lima Linux VM (see Verification).

**Acceptance Criteria:**
- Given the prod-like 2-Shard Cell with unrelated pending uploads on the non-target Shard, when `TestE2EBackendUploadHappyPath` runs, then it waits only on the test tx's Shard and passes.
- Given a leader failover where the replacement pod is K8s-Ready but replication has not converged, when the test proceeds, then it deterministically waits for target-Shard convergence before the canary write instead of failing on `replica block not open`.
- Given the full change, when the two tests run on Linux, then both pass and **no** timeout/retry/sleep constant was increased and no test was skipped.

## Design Notes

The repo already has the correct primitive: `fetchShardDiagnosticsWithResolvedLeaders` polls `/healthz` diagnostics with a deadline and fails closed. The fixes extend that same pattern Shard-scoped rather than inventing new machinery. Example shape for the upload wait (guidance, ~8 lines):

```go
shardID := routeShardID(t, txID) // via e2eTwoShardPlacement + Lookup
deadline := time.Now().Add(uploadE2ETimeout)
for time.Now().Before(deadline) {
    if shardPendingBlocks(t, shardID) == 0 { return }
    time.Sleep(time.Second)
}
t.Fatalf("Shard %d upload_pending_blocks did not reach 0: %+v", shardID, lastDiag)
```

## Verification

**Commands:**
- `go vet ./test/e2e/...` -- expected: clean.
- `make fmt-check` and `golangci-lint run ./test/e2e/...` -- expected: clean.
- On lima Linux (cluster up via `make tier2-e2e-up` or `make e2e-up`, `SCRAP_E2E=1`): `go test ./test/e2e/ -run 'TestE2EBackendUploadHappyPath|TestE2ELeaderFailover' -count=3` -- expected: PASS on all 3 iterations (deterministic across repeats).

**Manual checks (if no CLI):**
- Confirm via `/healthz` JSON on a pod that `shard_diagnostics` carries per-Shard `upload_pending_blocks` before relying on it.
