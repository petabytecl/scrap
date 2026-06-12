---
baseline_commit: c7092a01fce601dd2af7e03d4458352bbf58650d
created: 2026-06-12T13:07:03-04:00
---

# Story 5.2: Scanner Watermarks and Rescan Priority

Status: review

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a security operator,
I want scan progress and signature versions persisted,
so that rescans are deterministic and restart-safe.

## Traceability

- Epic: Epic 5 - Security Operators Can Contain Unsafe Content Without Mutating Documents.
- Requirements: FR-11.
- Governing decision: DG-1 in `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md`.
- Governing ADRs: ADR 0008 async content scanning, ADR 0025 admin surface amendment, ADR 0026 multi-Shard release boundary.
- Current baseline: Story 5.1 is done and pushed at `c7092a01fce601dd2af7e03d4458352bbf58650d`.
- Related future scope: Story 5.3 owns `QuarantineDocument` Raft authority; Story 5.4 owns read denial and `scan_status`; Story 5.5 and 5.6 own admin quarantine operations and `scrapctl` quarantine workflows.

## Acceptance Criteria

1. **AC-5.2.1 - Persist scanner dual watermarks.** Given scan work completes for a sealed Block, when progress is persisted, then scanner watermarks record `last_scanned_block_id` and `last_sig_version_scanned`. Evidence identifies the persisted Projection keys and changed boundary.
2. **AC-5.2.2 - Restart resumes from persisted progress.** Given scanner or Member restart occurs, when scanning resumes, then work resumes from persisted progress without treating watermarks as Document visibility authority. Evidence proves watermarks are progress evidence only.
3. **AC-5.2.3 - Signature updates trigger deterministic rescan priority.** Given signatures update, when rescan priority is computed, then previously clean Documents can be rescanned without changing Document identity. Evidence records the rescan trigger and redaction proof.
4. **AC-5.2.4 - Rollback and conflict are duplicate-safe.** Given persisted scanner watermark state appears to roll backward or conflict with signature-version state, when scanning resumes, then duplicate scan work is safe and Document visibility is unchanged. Evidence records watermark rollback and duplicate scheduling behavior.

## Tasks / Subtasks

- [x] Create the Story 5.2 evidence artifact before code changes. (AC: 1-4)
  - [x] Create `_bmad-output/implementation-artifacts/epic-5-scanner-watermarks-rescan-evidence.md`.
  - [x] Record baseline commit, changed-boundary list, persisted key names, restart/rescan/rollback commands, redaction scans, and final `PASS`/`CONCERNS`/`FAIL` rows.
  - [x] Keep closure scoped to Story 5.2. Do not claim `QuarantineDocument`, read denial, metadata scan status, admin quarantine operations, or Epic 5 closure.

- [x] Add scanner watermark storage to the Pebble Projection boundary. (AC: 1, 2, 4)
  - [x] Add a focused `internal/index` scanner watermark file instead of mixing this state into transaction entries, pending uploads, or confirmed uploads.
  - [x] Store one per-Shard Projection-local watermark record because each Shard owns its own Pebble Projection.
  - [x] Include `LastScannedBlockID uint64` and `LastSignatureVersionScanned string` in the stored value.
  - [x] Use a dedicated bounded key prefix such as `"\x00scanner-watermark\x00"` with explicit lower/upper bounds if iteration is needed.
  - [x] Use existing Pebble patterns: `idx.db.Set(..., pebble.Sync)`, `idx.db.Get`, decode validation, sentinel `Err...NotFound`, and package-local encoding helpers.
  - [x] Add index tests for missing watermark, put/get, corrupt/truncated/unknown-version value, invalid signature version, and streaming hash determinism with watermark keys included.

- [x] Add a scanner progress boundary to `internal/avscan` without importing `internal/index`. (AC: 1-4)
  - [x] Define small consumer-side interfaces in `internal/avscan`, for example `ProgressStore` and `SignatureVersionProvider`; keep them to the methods the scheduler consumes.
  - [x] Treat a missing progress record as zero progress with a bounded reason, not as data loss.
  - [x] Persist progress only after scan work for a Block completes successfully.
  - [x] Persist `last_scanned_block_id` as a contiguous frontier. Do not advance it past a lower Block that failed in the same run, even if later Blocks were scanned successfully.
  - [x] Keep the Story 5.1 in-memory `completed` map as process-local duplicate suppression only. It must not become persistent authority and must not replace the persisted watermark.

- [x] Implement deterministic rescan priority from signature-version changes. (AC: 3, 4)
  - [x] Compare current bounded signature version from an injected provider with `LastSignatureVersionScanned`.
  - [x] When the signature version changes, reset scan priority to the sealed Block frontier from the beginning of the Shard and persist the active signature version with a reset Block frontier.
  - [x] Do not add per-Document scan state in this story. ADR 0008 explicitly chooses dual watermarks.
  - [x] Do not change Document identity, transaction entries, or read visibility while rescanning.
  - [x] Ensure rollback/conflict cases rescan safely rather than skipping work or corrupting visibility state.

- [x] Wire persisted scanner progress through `internal/shard`. (AC: 1-4)
  - [x] Implement a Shard-local adapter that satisfies the `avscan` progress interfaces using `*index.Index`.
  - [x] Pass the adapter into `avscan.Config` from `newScannerCoordinator`; do not make `internal/avscan` import `internal/index`.
  - [x] Preserve Story 5.1 behavior: scanner work is leader-only, post-ACK, context-driven, non-blocking for writes, stream-based over `Block.OpenBytes`, and shares Deep Scrub pause/I/O budget.
  - [x] Rebuild paths must preserve scanner progress only if it is intentionally part of Projection state. If rebuild cannot preserve it safely, document and test the restart-safe fallback to rescan from zero progress.

- [x] Add focused restart, rescan, rollback, and redaction tests before broad gates. (AC: 1-4)
  - [x] `internal/index` tests prove watermark encoding, validation, missing/corrupt values, and hash determinism.
  - [x] `internal/avscan` tests prove persisted progress skips already scanned Blocks after scheduler reconstruction, only advances a contiguous frontier, and survives nil/missing progress store behavior.
  - [x] `internal/avscan` tests prove signature-version change causes a rescan from the Shard beginning without changing Block or Document identity inputs.
  - [x] `internal/shard` tests prove close/reopen or scheduler reconstruction resumes from persisted progress while writes and reads remain unaffected.
  - [x] Add rollback/conflict tests where persisted progress is lower, higher than known sealed Blocks, or paired with an old signature version; duplicate scanning is allowed, skipped unsafe work is not.
  - [x] Do not use sleeps as synchronization. Use fake stores, manual ticks, channels, contexts, and bounded polling with clear failure messages.

- [ ] Update story, evidence, and sprint artifacts. (AC: 1-4)
  - [x] Move this story to `in-progress` when implementation starts and to `review` only after local verification is complete.
  - [x] Update the evidence artifact and this story with debug log references, completion notes, review findings, and file list.
  - [ ] Run `bmad-code-review`; address critical/high findings before marking `done`.

## Dev Notes

### Current State

- Story 5.1 created `internal/avscan` with leader-only scheduler lifecycle, stream-based `Block.OpenBytes`, bounded status snapshots, OpenTelemetry metrics, and Shard/admin/`scrapctl` diagnostics.
- Current scheduler progress is process-local only: `completed map[uint64]struct{}` suppresses duplicate scans during one process lifetime, and `Snapshot.LastScannedBlockID` is an operator status field. Neither is persisted.
- Current scheduler sorts eligible Blocks, continues after recoverable scan failures, serializes `RunOnce`, and keeps failed Blocks in `LagBlocks`. Do not regress these properties.
- Current Shard scanner wiring passes sealed Block streams from `internal/shard/scanner.go`; it intentionally does not expose `.blk` or `.idx` filesystem paths to `internal/avscan` engines.
- Current `internal/index` stores Projection entries, Upload Outbox, and Confirmed Upload Catalog in one Pebble database. Scanner watermarks should follow the existing prefix/encoding/test style but stay in their own file.

### Existing Code To Reuse

- `internal/avscan/scheduler.go` - scheduler lifecycle, sorting, duplicate suppression, scan error handling, metrics wrappers, and manual tick support.
- `internal/avscan/types.go` - add progress and signature-version interfaces/value types here if they are scheduler contracts.
- `internal/shard/scanner.go` - Shard-owned scanner coordinator and sealed Block lister; add the index-backed progress adapter here or a small sibling file.
- `internal/index/index.go` - Pebble open/get/set/hash patterns and transaction Projection key style.
- `internal/index/upload_outbox.go` - compact binary value versioning, dedicated key prefixes, iterators, validation, and sentinel-not-found pattern.
- `internal/index/confirmed_upload_catalog.go` - JSON value versioning and strict validation for richer persisted records.
- `internal/shard/shard.go` - Shard construction order. The index exists before scanner construction; `s.scanner.Start()` happens after Raft open and runtime refresh.
- `internal/shard/projection_rebuilder.go` - Projection rebuild behavior. Decide explicitly whether scanner watermarks are preserved through rebuild or intentionally reset to safe rescan.

### Implementation Guardrails

- Scanner watermarks are progress evidence, not Document visibility authority. Do not modify `ReadDocument`, `HeadDocument`, `FindDocuments`, transaction entries, or public gRPC behavior in this story.
- Do not add `QuarantineDocument`, quarantine Pebble prefixes, scan status fields, or admin quarantine endpoints. Those begin in later stories.
- Do not add per-Document scan state. ADR 0008 calls for dual watermarks only: Block frontier plus signature-version frontier.
- Persist the Block watermark as the contiguous successful scan frontier, not as the maximum scanned Block. A later Block may scan successfully while an earlier Block failed; persisted progress must not skip the failed lower Block on restart.
- Signature version strings must be bounded and sanitized before persistence or diagnostics. Do not persist rule text, signature names, raw dependency errors, file paths, Document identifiers, trace IDs, or request IDs.
- Do not introduce ClamAV/YARA/native dependencies in this story. Use injected fake signature providers and fake engines.
- Do not introduce cross-Shard/global scanner state. Each Shard owns its own Projection and scanner progress.
- Do not make Backend inventory, local lifecycle files, or admin status decide progress authority. Persist and load progress through the Shard Projection boundary only.
- Do not weaken Story 5.1 gates: writes must continue when scanner progress persistence fails or the engine is unavailable, but failures must be operator-visible through bounded scanner status.

### Latest Tech Information

- No new external runtime dependency is needed for Story 5.2. Use the repo-pinned Pebble dependency from `go.mod` and existing `internal/index` patterns.
- `scrapd` remains `CGO_ENABLED=0` and `FROM scratch`; scanner watermark work must not add native scanner packages, shells, sidecars, or runtime tools.
- If a signature version provider is added, keep it an interface/fake in this story. Real ClamAV/YARA version discovery belongs with later engine adapter or deployment stories.

### Project Structure Notes

Likely update during implementation:

- `internal/index/scanner_watermark.go`
- `internal/index/scanner_watermark_test.go`
- `internal/avscan/types.go`
- `internal/avscan/scheduler.go`
- `internal/avscan/scheduler_test.go`
- `internal/shard/scanner.go` or `internal/shard/scanner_progress.go`
- `internal/shard/scanner_wiring_test.go`
- `_bmad-output/implementation-artifacts/5-2-scanner-watermarks-and-rescan-priority.md`
- `_bmad-output/implementation-artifacts/epic-5-scanner-watermarks-rescan-evidence.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

Avoid unless a failing test proves it is required:

- `proto/`, `gen/`, public/peer/admin wire contracts, Raft command shape, transaction Projection entries, Backend object identity, read-path metadata, Content Quarantine state, quarantine admin operations, deployment overlays, and scanner engine runtime dependency pinning.

### Testing Requirements

Run targeted package tests after implementation:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/avscan ./internal/shard ./internal/admin ./internal/cmd ./internal/scrapctl -count=1
```

Run static and structural gates:

```bash
git diff --check
scripts/check-e2e-gates.sh
```

Run broad local gate before code review:

```bash
env GOCACHE=/tmp/scrap-v2-go-build make check
```

Run leak scans over story, evidence, and touched scanner/index/shard/status code. Keep patterns in shell variables so commands do not self-match copied sensitive text:

```bash
secret_shape_pattern='(AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-[A-Za-z0-9-]{10,}|-----BEGIN (RSA |EC |OPENSSH |)PRIVATE KEY-----)'
scanner_sensitive_pattern='([t]ransaction_id=|[d]ocument_name=|[i]dempotency[_-]?[k]ey=|Backend [k]ey:|trace[_-]?[i]d=|request[_-]?[i]d=|[s]ignature=|[r]ule=|clamd_[e]rror=)'
scan_scope='_bmad-output/implementation-artifacts/5-2-scanner-watermarks-and-rescan-priority.md _bmad-output/implementation-artifacts/epic-5-scanner-watermarks-rescan-evidence.md internal/index/scanner_watermark.go internal/index/scanner_watermark_test.go internal/avscan/types.go internal/avscan/scheduler.go internal/avscan/scheduler_test.go internal/shard/scanner.go internal/shard/scanner_progress.go internal/shard/scanner_wiring_test.go internal/admin/shard_diagnostics.go internal/cmd/shard_diagnostics.go internal/scrapctl/status.go internal/scrapctl/output.go'
rg -n --pcre2 "$secret_shape_pattern" $scan_scope
rg -n --pcre2 "$scanner_sensitive_pattern" $scan_scope
```

### Previous Story Intelligence

- Story 5.1 committed and pushed `ab02a69 feat: add content scanner scheduler`; review fixes committed and pushed `c7092a0 fix: address content scanner review findings`.
- Review fixes that must not regress:
  - scanner engines receive stream openers, not local paths;
  - missing scanner engines are visible as bounded degraded scanner status;
  - scanner Notify happens after seal proposal work;
  - scheduler `RunOnce` is serialized;
  - recoverable failed Blocks do not starve later Blocks;
  - completed Block tracking is process-local and duplicate-safe;
  - lister/loop/metrics panics are bounded;
  - scanner diagnostics include last-updated Unix time.
- Story 5.1 broad gate passed after review fixes: `env GOCACHE=/tmp/scrap-v2-go-build make check`.

### References

- `CONTEXT.md` - Content Scanner glossary, Content Quarantine distinction, write ACK contract, and V2 process rules.
- `_bmad-output/project-context.md` - Go package boundaries, testing rules, no raw identifier telemetry, and static scratch image rules.
- `_bmad-output/planning-artifacts/epics.md` - Epic 5 split and Story 5.2 acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-11 async Content Scanner consequences.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - DG-1 scanner/quarantine architecture, authority patterns, and test patterns.
- `docs/adr/0008-async-content-scanning-architecture.md` - dual watermark decision and rescan behavior.
- `docs/adr/0025-content-quarantine-admin-surface.md` - admin surface amendment and redaction rules.
- `docs/adr/0026-multi-shard-v2-release-boundary.md` - scanner/quarantine remain Shard-local authority flows.
- `docs/go-style-guide.md` - interfaces, concurrency, lifecycle, tests, and metrics conventions.
- `_bmad-output/implementation-artifacts/5-1-content-scanner-engine-boundary-and-scheduling.md` - previous story implementation record and review findings.

## Dev Agent Record

### Agent Model Used

GPT-5 Codex for story creation.

### Debug Log References

- 2026-06-12T13:07:03-04:00 - Story 5.2 created from sprint status after Story 5.1 implementation, code review, review fixes, commit, and push completed.
- 2026-06-12T13:13:21-04:00 - Dev-story workflow started. Story and sprint status moved to in-progress from baseline `ccc2844bdcf07da30530706efaab34ad75274c0f`.
- 2026-06-12T13:14:00-04:00 - Created Story 5.2 evidence artifact before production code changes.
- 2026-06-12T13:16:11-04:00 - Added Projection-local scanner watermark storage and focused RED/GREEN tests. Verified with `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index -run ScannerWatermark -count=1`.
- 2026-06-12T13:20:18-04:00 - Added avscan progress store and signature-version scheduler flow. Verified with `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/avscan -count=1`.
- 2026-06-12T13:22:44-04:00 - Wired Shard scanner progress adapter through the Projection-backed coordinator. Verified with `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run Scanner -count=1`.
- 2026-06-12T13:23:51-04:00 - Targeted Story 5.2 package gate passed with `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/avscan ./internal/shard ./internal/admin ./internal/cmd ./internal/scrapctl -count=1`.
- 2026-06-12T13:30:33-04:00 - Final implementation gates passed: `git diff --check`, `scripts/check-e2e-gates.sh`, redaction scans, and `env GOCACHE=/tmp/scrap-v2-go-build make check`.

### Completion Notes List

- Ultimate context engine analysis completed - comprehensive developer guide created.
- Story 5.2 is ready for dev-story implementation.
- Created scoped evidence artifact with baseline commit, changed boundaries, persisted key names, verification commands, redaction plan, and explicit out-of-scope quarantine/read-path closure.
- Added `internal/index` scanner watermark persistence using a dedicated Projection key, compact versioned encoding, bounded signature-version validation, not-found sentinel behavior, and streaming-hash participation tests.
- Added `internal/avscan` progress and signature-version interfaces, restart resume from persisted frontier, contiguous-frontier persistence, signature-version reset priority, and duplicate-safe rollback/conflict tests without importing `internal/index`.
- Added Shard-local scanner progress adapter from `*index.Index` to `avscan.ProgressStore`, plus coordinator reconstruction coverage proving persisted progress survives scheduler reconstruction without exposing Block paths to scanner engines.
- Local implementation verification passed and the story is ready for the BMAD code-review workflow.

### File List

- `_bmad-output/implementation-artifacts/5-2-scanner-watermarks-and-rescan-priority.md`
- `_bmad-output/implementation-artifacts/epic-5-scanner-watermarks-rescan-evidence.md`
- `internal/avscan/metrics_otel.go`
- `internal/avscan/scheduler.go`
- `internal/avscan/scheduler_test.go`
- `internal/avscan/types.go`
- `internal/index/scanner_watermark.go`
- `internal/index/scanner_watermark_test.go`
- `internal/shard/scanner.go`
- `internal/shard/scanner_progress.go`
- `internal/shard/scanner_progress_test.go`
- `internal/shard/shard.go`

### Change Log

- 2026-06-12 - Implemented Story 5.2 scanner watermarks and deterministic rescan priority; moved story to review after local gates passed.
