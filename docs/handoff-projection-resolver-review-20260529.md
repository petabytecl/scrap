# Handoff — "The Review": projection-resolution refactor (`internal/index.Resolver`)

**Date:** 2026-05-29 · **Repo:** `/home/coto/dev/petabyte/scrap` · **Branch:** `main` (HEAD `4e0d3e3`, up to date with `origin/main`)
**Prior session:** ran `/code-review xhigh` over the **uncommitted working-tree diff**. This doc captures the verified review so a fresh agent can act on it (apply fixes / open issues).

> ⚠️ The review findings live ONLY in the prior chat transcript and in this file. No issue/PR/commit captures them yet. The diff itself is uncommitted (see below) — do not assume it's pushed.

---

## 1. What is under review

A refactor that extracts projection resolution (Pebble **Projection** transaction `Entry` → walk its **Block** IDs → open each block `.idx` → **Document** metadata) out of `internal/shard` into a new `internal/index.Resolver`, switching the shard's lookups to it with **fail-closed** error semantics (corruption surfaces as `storeapi.ErrDataLoss` instead of being swallowed).

Working-tree diff (run `git diff HEAD` + read the two untracked files):
- `internal/index/resolution.go` (NEW, 121 lines) — `Resolver` with `ResolveDocument` / `ListDocuments` / `ContainsDocument`; sentinels `ErrDocumentNotFound`, `ErrCorrupt`.
- `internal/index/resolution_test.go` (NEW, 190 lines)
- `internal/shard/projection.go` — `documentVisibleInProjection`, `findDocEntry`, `projectionResolver()`, `mapProjectionResolutionError`
- `internal/shard/shard.go` — `WriteDocument`, `FindDocuments` rewired
- `internal/shard/openlog.go` — `recoverPrepFile` extracted, uses the resolver
- `internal/shard/shard_test.go` — `TestProjectionResolutionCorruptionFailsClosed`
- `CONTEXT.md` — new "Projection Resolution" glossary entry (scopes fail-closed to the **read** path / `FindDocuments`)

Tests pass: `go test ./internal/index/ -run Resolver` and `go test ./internal/shard/ -run ProjectionResolutionCorruption` → both `ok`.

---

## 2. Verified facts (primary-source, don't re-derive)

- **Durability asymmetry (the crux):** `index.put` (internal/index/index.go:148) commits with `pebble.Sync` → projection is durable immediately. `block.IndexWriter.Append` (internal/block/index.go) does three plain `Write`s and **only fsyncs in `Close()`** (i.e. at block *seal*). ⇒ Between a doc's commit and its block sealing, the projection is durably **ahead** of the `.idx`. After a crash, "projection references a block whose `.idx` lost its tail" is a **legitimate, recoverable state**, not corruption.
- **Apply re-runs on replay:** `applyEntries` (internal/shard/apply.go:35-38) applies every entry; the `replayUntil` watermark only suppresses *spans*, not the apply itself.
- **Apply errors are swallowed on replay/followers:** `applyCommitDocumentCommand` (apply.go:120-131) delivers `applyErr` to a proposer channel only if one is registered; on replay/followers there is none → error dropped, FSM advances.
- **Recovery runs before replay:** `New()` calls `recoverOpenlog()` (shard.go:166) **before** `scrapraft.Open` replay (shard.go:180). So recovery reads on-disk `.idx` as the crash left them.
- **First block ID is 1:** `scanMaxBlockID` returns 1 on an empty/missing blocks dir → the test's hardcoded block `1` is correct.
- **`docExistsInPebble` (old) was fail-OPEN:** returned `false` on `idx.Get` error AND `continue`d past blocks whose `.idx` failed to open. The new resolver is fail-CLOSED.
- Invariant "block in `Entry.BlockIDs` ⇒ its `.idx` has ≥1 entry for the tx" holds in **healthy** operation (`addProjectionDocument` adds the BlockID exactly when an entry is appended). It is violated only by the torn-write window above or genuine corruption — so the resolver is **not** a false-positive generator in normal multi-block operation.

---

## 3. The headline: one root cause, three blast radii

The strict resolver is correct on the **read** path but was reused in two paths that must tolerate the torn-write window:

| Path | Old (`docExistsInPebble`, fail-open) | New (`Resolver`, fail-closed) | Symptom |
|------|--------------------------------------|-------------------------------|---------|
| `recoverOpenlog`→`recoverPrepFile` (openlog.go:84) | `false` → truncate + continue → shard starts | `ErrCorrupt` → `New()` fails | **Shard won't boot** |
| `applyCommitDocument` replay/follower (projection.go:38) | always proceeded with projection mutation | early-return on `ErrCorrupt`; error **swallowed** (apply.go:120-131) → mutation skipped | **Silent replica divergence** (scrub `StreamingHash` will flag) |
| `FindDocuments`/`Head`/`Read` (shard.go:416, 365, 387) | empty list / `NotFound` | `ErrDataLoss` (codes.DataLoss) | **Client 500 on recoverable state** |

**Intended fix altitude:** keep the strict resolver for client reads; give recovery and apply a *lenient* existence check (treat unreadable / missing-tail `.idx` as "not present" → proceed/self-heal). CONTEXT.md's own new glossary entry scopes fail-closed to the *read* side — recovery/replay inherited it by code reuse, not design.

---

## 4. Full findings list (12, ranked) — not persisted anywhere else

Correctness (1-6), then gaps/cleanup/efficiency/process (7-12).

1. **openlog.go:84** — recovery aborts shard startup on torn/empty visible `.idx` (was truncate+continue). Trigger: leftover `.prep` for a tx that committed (projection durable) but whose block `.idx` is torn or lost-all-tx-entries. **High severity, high certainty.**
2. **projection.go:38 (+ apply.go:120-131)** — `applyCommitDocument` dup-check now fallible; on replay/follower the `ErrCorrupt` is silently swallowed and the projection mutation is skipped → cross-replica divergence. **High severity, medium certainty** (precise end-state nuanced; divergence detectable via scrub hash).
3. **shard.go:416 / projection.go:116** — read paths return gRPC DataLoss for the recoverable transient state (old: empty list / NotFound). Fail-closed on *genuine* corruption is intended; defect is firing on the *recoverable* case.
4. **resolution.go:93** — `transactionEntry` maps `len(BlockIDs)==0` → `ErrCorrupt` (was empty-list/NotFound). Defensive (no normal path creates zero-block entries) → lower severity; removed tolerance.
5. **internal/spike/store.go:257** — spike.Store still has the OLD lenient inline logic; not migrated. Integration/server suites wire spike → they validate lenient behavior and give false confidence the fail-closed contract holds e2e. (altitude)
6. **resolution_test.go:117** — `TestResolverFailsClosedWhenVisibleBlockHasNoTransactionEntries` codifies the over-aggressive "empty block == corrupt" as spec → will reject the correct fix for 1-3.
7. **resolution.go:64** — `ListDocuments` uses `entry.DocCount` only as a cap hint; never validates resolved count vs DocCount → DocCount/entry drift passes silently (a gap in the "owns fail-closed for visible metadata corruption" claim).
8. **projection.go:131** — `mapProjectionResolutionError` default arm is byte-identical to the `ErrCorrupt` arm (dead branch); also labels ANY unexpected error as permanent `ErrDataLoss` (misclassifies transient/retryable as irrecoverable). (simplification + altitude)
9. **projection.go:125** — `projectionResolver()` rebuilds `index.NewResolver(s.idx, s.idxPath)` per call; `s.idxPath` bound-method value heap-allocates a closure on every read/write/apply/recovery. (efficiency) Note: `s.idx` is swapped under `s.mu` on rebuild, so don't naively cache the whole resolver — the *path adapter* is stable.
10. **resolution.go:116** — existence check lost its short-circuit: `ContainsDocument`→`ResolveDocument`→`blockEntries` calls `FindByTransaction` (collects ALL tx entries per block, allocates) then scans for docName, vs old `ir.Find` first-match. Runs on every write + apply. (efficiency)
11. **projection.go:118** — `mapProjectionResolutionError` drops the `index.*` sentinel from the `errors.Is` chain for the NotFound/DocumentNotFound cases (latent; no current caller relies on it, `storeapi` sentinels ARE preserved).
12. **resolution.go:1 (package)** — new `internal/index` → `internal/block` dependency (index now decodes `.idx`); no cycle today (block doesn't import index) but inverts the package-map layering and shipped with **no ADR** despite AGENTS.md requiring one for cross-package boundary changes.

Candidates considered and **rejected** (don't re-raise): "ResolveDocument returns first-match across blocks → stale entry" (old code had identical ordering; dup-check prevents duplicate `(txID,docName)`); test block-ID-1 brittleness (verified correct); nil `s.idx` deref (resolver is nil-safe + all callers hold `s.mu`); double `%w` (Go 1.26, fine); slice aliasing after `Close` (FindByTransaction returns value copies).

---

## 5. Next-session options (pick with the user via AskUserQuestion)

The prior session ended by offering to apply fixes. Nothing has been changed/committed yet. Likely paths:

- **A. Apply the core fix (findings 1-3):** add a lenient existence path on `Resolver` (e.g. a variant that treats `OpenIndexReader` failure / empty `FindByTransaction` as "not present" rather than `ErrCorrupt`), wire it into `recoverPrepFile` and `applyCommitDocument`, keep the strict resolver for `Head`/`Read`/`Find`. Update/replace `TestResolverFailsClosedWhenVisibleBlockHasNoTransactionEntries` (finding 6) accordingly. Add a regression test: crash with projection-ahead-of-`.idx`, assert shard **starts** and replica **converges**.
- **B. Knock out the independent items (4-8):** migrate or delete `spike` (5), de-dup the error-map default arm (8), add DocCount validation (7).
- **C. File issues** for the lot via the issue tracker (GitHub Issues on `petabytecl/scrap`, see `docs/agents/issue-tracker.md`) and let them be picked up later.
- **D. Decide whether fail-closed-in-recovery is actually intended** — confirm with user/owner before "fixing", since CONTEXT.md scopes fail-closed to reads but someone may have wanted recovery strict too.

⚠️ Per the user's standing rules: **always use `AskUserQuestion`** (never plain-text questions), and **SCRAP only** (this is `main` branch — V1/main out of scope). User is in **explanatory** output style and wants deep explanations with concrete numbers.

---

## 6. Suggested skills

- **`/code-review`** — to re-run or extend the review after fixes land (or `/code-review --fix` to apply findings directly).
- **`scrap-diagnose`** — if pursuing finding 2 (replay divergence): reproduce → minimise → instrument the projection-ahead-of-`.idx` window with a fault-injection test.
- **`scrap-tdd`** — for the regression test in option A (red-green-refactor; the crash-recovery test is the RED).
- **`scrap-designing-data-intensive-applications`** — durability ordering, replay idempotency, derived-data (projection) maintenance are exactly its remit; consult before changing the fail-closed semantics.
- **`scrap-to-issues`** — if going with option C (break findings into tracer-bullet issues).
- **`scrap-improve-codebase-architecture`** / ADR authoring — for finding 12 (record the `index`→`block` direction) and finding 5 (spike divergence).
- **`scrap-grill-with-docs`** — to stress-test the fail-closed-vs-lenient decision against CONTEXT.md's glossary before committing.

---

## 7. Key references (don't duplicate — read these)
- Diff: `git diff HEAD` in `/home/coto/dev/petabyte/scrap`
- New code: `internal/index/resolution.go`
- Glossary: `CONTEXT.md` ("Projection Resolution", "Openlog" entries)
- Project rules / package map: `AGENTS.md`
- Apply path: `internal/shard/apply.go`; recovery: `internal/shard/openlog.go`; resolver wiring: `internal/shard/projection.go`
- Durability primitives: `internal/index/index.go:148` (`pebble.Sync`), `internal/block/index.go` (`IndexWriter.Append`/`Close`)
