---
baseline_commit: cf396ecac99c2472ddbc23d4f61098e3bb8a4a7e
created: 2026-06-11T21:01:00-04:00
---

# Story 3.2: Upload Pressure and Safe Admission Evidence

Status: done

## Story

As a storage operator,
I want upload lag to create safe, observable pressure before local durability runway is unsafe,
so that the Cell degrades before durability is compromised.

## Acceptance Criteria

1. **AC-3.2.1 - Pressure visibility from committed obligations.** Given upload lag grows past configured thresholds, when admission decisions are evaluated, then pressure state becomes visible before unsafe local storage runway. Evidence links pressure state to committed upload obligations.
2. **AC-3.2.2 - Automatic recovery after drain.** Given pressure clears, when uploads catch up, then admission behavior recovers without manual metadata edits. Evidence proves recovery follows committed state, not local operator mutation.
3. **AC-3.2.3 - Bounded telemetry and diagnostics.** Given pressure telemetry is produced, when evidence is collected, then labels are bounded and redacted. Evidence records telemetry and redaction checks.
4. **AC-3.2.4 - Rejection leaves no accepted partial state.** Given admission is rejected under upload pressure, when a write attempt is stopped, then no partial Block, Frame, or visibility metadata is left as accepted state. Evidence records cleanup and recovery behavior.

## Tasks / Subtasks

- [x] Build the Story 3.2 evidence checklist before code changes. (AC: 1-4)
  - [x] Record current upload pressure authority path: committed `SealBlock` plus local upload obligations -> pending upload stats -> `uploadController.SetPressure` -> admission `RESOURCE_EXHAUSTED`.
  - [x] Identify which existing tests already prove each AC and which gaps need new focused tests or evidence rows.
  - [x] Keep failed behavior fixes local to the relevant Epic 1 or Epic 2 boundary; do not hide unrelated defects inside evidence text.
- [x] Prove pressure is computed from committed upload obligations and local retry obligations. (AC: 1)
  - [x] Add or update focused shard/index tests showing committed pending uploads contribute to `PendingBytes`, `PendingBlocks`, and pressure level.
  - [x] Preserve the de-duplication rule in `uploadObligations.pressureStats`: a Block present in committed pending uploads and local retry obligations counts once.
  - [x] Verify pressure levels use normalized thresholds from `UploadPressureConfig` and surface before writes continue into unsafe runway.
- [x] Prove pressure rejection and recovery are state-driven. (AC: 2)
  - [x] Extend `TestUploadPressureRejectsWritesAndResumesAfterDrain` or add a narrow companion fixture that rejects at pressure/critical and accepts again only after committed confirmation clears the pending upload.
  - [x] Assert recovery uses `ConfirmUploadForTest` or the real upload confirm path, not manual Pebble edits, Backend inventory/listing, or operator mutation.
  - [x] Keep Backend upload asynchronous and outside the write ACK path; Story 3.1 already owns ACK-independence evidence.
- [x] Prove rejected admission leaves no accepted partial Document state. (AC: 4)
  - [x] Add a deterministic test around the seal-triggered rejection path in `sealAndOpenNew`.
  - [x] After a rejected write, assert `HeadDocument`/`ReadDocument`/`FindDocuments` do not expose the rejected Document.
  - [x] Assert no Openlog prep file remains for the rejected Document and that the active new Block is still reusable for a later accepted write after pressure drains.
  - [x] Do not require zero-byte new-Block cleanup if the design intentionally leaves the newly opened empty Block as active writable state; document that distinction in evidence.
- [x] Prove telemetry, admin health, and client errors are bounded. (AC: 3)
  - [x] Keep `scrap.upload.*` metrics bounded to Shard ID, pressure level/status, and small enumerations. Do not add raw `transaction_id`, `document_name`, idempotency keys, Backend keys, file paths, trace IDs, request IDs, peer addresses, or auth claims as labels or log fields.
  - [x] Verify public write rejection maps to gRPC `RESOURCE_EXHAUSTED` with `ErrorInfo.reason == "upload_pressure"`.
  - [x] Verify admin health exposes `upload_pressure`, `upload_pressure_level`, `upload_pending_bytes`, and `upload_pending_blocks` without raw identifiers.
  - [x] Record leak-scan commands and expected bounded matches in the evidence artifact.
- [x] Capture Epic 3 evidence and regression gates. (AC: 1-4)
  - [x] Create or update an Epic 3 evidence artifact for upload pressure with AC rows, reproducible commands, result, notes, and remaining Tier 2/Tier 3 gaps.
  - [x] Run focused tests first, then package/race gates needed for concurrency and pressure state.
  - [x] Run `make check` before code-review handoff unless a narrower failure clearly blocks and is documented.
  - [x] If deployed evidence is claimed, run the E2E target with `SCRAP_E2E=1`; a skipped E2E run must be recorded as CONCERNS, not PASS.

### Review Findings

- [x] [Review][Patch] Lock upload metric status values to bounded enums [internal/shard/upload_metrics_otel.go:106]
- [x] [Review][Patch] Assert all collected `scrap.upload.*` metrics have bounded attribute keys and values [internal/shard/upload_metrics_otel_test.go:153]
- [x] [Review][Patch] Cover pressure rejection inside an existing Transaction so `FindDocuments` must preserve accepted Documents while excluding the rejected Document [internal/shard/upload_pressure_test.go:96]
- [x] [Review][Patch] Scope the Openlog cleanup assertion to `.prep` files only [internal/shard/upload_pressure_test.go:304]
- [x] [Review][Patch] Prove pressure rejection leaves the active Block header-only before the next accepted write [internal/shard/upload_pressure_test.go:319]
- [x] [Review][Patch] Clarify evidence status, review baseline, implementation baseline, verification results, and leak-scan allowlist [_bmad-output/implementation-artifacts/epic-3-upload-pressure-evidence.md:3]

## Dev Notes

### Current State

- `CONTEXT.md` defines Upload Outbox as the per-Shard durable record of sealed Blocks pending Backend upload. It explicitly says the outbox drives admission pressure when pending upload bytes exceed the configured budget.
- PRD FR-6 requires asynchronous Backend upload through committed metadata and says upload lag can create admission pressure before local durability runway is unsafe.
- ADR 0010 makes committed `SealBlock` the upload obligation and committed `ConfirmUpload` the upload completion authority. Backend PUT, HEAD, list, or object existence is never authority by itself.
- ADR 0011 requires bounded Upload Outbox scans through the upload key prefix; `refreshUploadPressureLocked` should stay cheap for ordinary pending upload backlogs.
- ADR 0012 requires low-cardinality OTel metrics for upload lag and pressure and forbids raw `transaction_id` or `document_name` as metric attributes.
- ADR 0015 makes upload pressure part of the Tier 2 prod-like E2E gate and Tier 3 evidence scenario.
- ADR 0016 keeps early eviction operator-gated and explicitly defers automatic disk-pressure eviction policy; do not expand this story into eviction policy.
- Implementation readiness notes warn that Story 3.2 is broad and must remain evidence-only. If evidence exposes a functional defect, patch the narrow defect and keep the story focused on proof.

### Existing Implementation To Reuse

- `internal/shard/upload_pressure.go` owns `UploadPressureConfig`, threshold normalization, level calculation, public snapshots, and the scrub pause coordinator.
- `internal/shard/upload_controller.go` owns `uploadController.SetPressure`, adaptive concurrency, pressure metrics, and `rejectWrite`.
- `internal/shard/upload_obligations.go` aggregates committed pending uploads and local retry obligations into pending bytes/blocks while de-duplicating by Block ID.
- `internal/shard/block_upload_lifecycle.go` refreshes pressure from the pending Upload Outbox and committed/local upload obligations.
- `internal/shard/blockfiles.go` calls `refreshUploadPressureLocked` during `sealAndOpenNew`, checks `rejectWrite`, then proposes seals outside `s.mu`.
- `internal/server/server.go` maps `storeapi.ErrResourceExhausted` to gRPC `RESOURCE_EXHAUSTED` and attaches structured error details when a reason exists.
- `internal/admin/server.go`, `internal/cmd/app.go`, and `internal/cmd/shard_diagnostics.go` expose pressure snapshots for local and multi-Shard admin health.
- `test/e2e/upload_e2e_test.go` already has `TestE2EBackendUploadAdmissionPressure`; use it as deployed evidence only when it runs with `SCRAP_E2E=1`.
- `test/stress/main.go` already detects upload pressure via `ErrorInfo.reason == "upload_pressure"` in stress mode.

### Previous Story Intelligence

- Story 3.1 proved committed upload confirmation and ACK independence. Reuse its authority model and evidence style.
- Story 3.1 review fixes matter here:
  - Wait for real backend or state transitions before asserting evidence; avoid tests that can pass before pressure is actually reached.
  - Use actual pending upload Block IDs and sizes rather than hard-coded fixtures when proving authority.
  - Do not overclaim skipped E2E runs; record them as CONCERNS with next action.
  - Include leak-scan commands with concrete changed paths and expected bounded matches.
- Older Story 4.3 established upload pressure ownership and scrub coordination. Do not rework that boundary unless Story 3.2 evidence exposes a concrete defect.

### Architecture Guardrails

- Do not change Block/Frame layout, Backend key format, public/peer/admin proto contracts, Raft command shape, Confirmed Upload Catalog schema, or Pebble key prefixes for this story.
- Do not add a dependency or assertion/mocking library. Existing Go stdlib tests, local fakes, gRPC `status`/`errdetails`, and OTel metric test helpers are sufficient.
- Do not use Backend inventory as an admission or recovery oracle. Pressure and recovery must follow pending Upload Outbox and committed `ConfirmUpload` state.
- Do not conflate Upload Outbox, Confirmed Upload Catalog, Local Block Lifecycle, Backend inventory, or eviction state.
- Do not add raw identifiers to deployed logs, metrics, traces, admin output, or evidence artifacts. Use bounded Shard IDs and enumerated statuses only.
- Keep code changes small and in existing files unless a focused helper file improves cohesion. Avoid generic `common`, `shared`, or `util` packages.
- If any production config parsing changes are needed, malformed explicit env/config input should fail clearly; do not silently widen unsafe limits.

### Testing Requirements

Run focused gates first:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestUploadPressure|TestSealTriggeredUploadPressure|TestUploadOutbox|TestWriteDocument_NoPrepFile' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server ./internal/admin -run 'UploadPressure|ResourceExhausted' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd -run 'ShardDiagnosticsPressure|UploadPressure|ResourceExhausted' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard -run 'TestUploadPressure|TestSealTriggeredUploadPressure' -count=10 -v
```

Run regression gates before code review:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/shard ./internal/server ./internal/admin ./internal/cmd -count=1
env GOCACHE=/tmp/scrap-v2-go-build make check
```

Use this E2E target only when the prod-like Cell is available and `SCRAP_E2E=1` is intentionally set:

```bash
env GOCACHE=/tmp/scrap-v2-go-build SCRAP_E2E=1 go test ./test/e2e -run TestE2EBackendUploadAdmissionPressure -count=1 -v
```

Suggested leak scans. Store the patterns in shell variables so the story file does not self-match credential-shaped words when the scan is copied into evidence:

```bash
cred_pattern='(?i)(api[_-]?[k]ey|[s]ecret|[p]assword|[t]oken|[b]earer|[a]uthorization|aws_access_key_[i]d|aws_[s]ecret_access_[k]ey|private [k]ey|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-)'
identifier_pattern='([t]ransaction_id|[d]ocument_name|[i]dempotency|Backend [k]ey|Backend object [k]ey|validation [t]oken|trace [I]D|request [I]D|gRPC [m]etadata|auth [c]laims|peer [a]ddress|[c]ertificate|/shards/|/tmp/|/home/)'
rg -n --pcre2 "$cred_pattern" _bmad-output/implementation-artifacts/3-2-upload-pressure-and-safe-admission-evidence.md _bmad-output/implementation-artifacts/epic-3-upload-pressure-evidence.md internal/shard internal/server internal/admin internal/cmd test/e2e test/stress
rg -n --pcre2 "$identifier_pattern" _bmad-output/implementation-artifacts/3-2-upload-pressure-and-safe-admission-evidence.md _bmad-output/implementation-artifacts/epic-3-upload-pressure-evidence.md internal/shard internal/server internal/admin internal/cmd test/e2e test/stress
```

### Latest Technical Information

- GitHub repository search for `upload pressure admission backpressure storage gateway Go` returned no reusable implementation to adopt.
- GitHub code search for `"RESOURCE_EXHAUSTED" "ErrorInfo" "upload_pressure" language:Go` returned no reusable implementation to adopt.
- Current module versions relevant to this story are `google.golang.org/grpc v1.81.1`, `go.opentelemetry.io/otel v1.44.0`, `go.opentelemetry.io/otel/sdk/metric v1.44.0`, `go.etcd.io/raft/v3 v3.6.0`, and `github.com/cockroachdb/pebble v1.1.5`.
- gRPC Go documentation supports returning `status.Status` errors with `WithDetails`; keep using the existing `ErrorInfo` mapping rather than introducing a new error payload.
- OpenTelemetry Go SDK documentation warns that high-cardinality attributes can increase memory and backend cost; keep pressure metrics on bounded attributes only.

### References

- `CONTEXT.md` - Upload Outbox and Confirmed Upload Catalog glossary.
- `_bmad-output/planning-artifacts/epics.md` - Epic 3 and Story 3.2 source.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-6 and release evidence matrix.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - admin/scrapctl upload-pressure evidence views and release evidence standard.
- `_bmad-output/planning-artifacts/implementation-readiness-report-2026-06-08.md` - Story 3.2 evidence-only sizing caution.
- `_bmad-output/implementation-artifacts/3-1-committed-backend-upload-confirmation.md` - previous Story 3.1 guardrails and review findings.
- `_bmad-output/implementation-artifacts/epic-3-backend-upload-confirmation-evidence.md` - Story 3.1 evidence format and caveats.
- `_bmad-output/implementation-artifacts/4-3-upload-pressure-pause-gate-and-scrub-coordination-ownership.md` - prior pressure ownership implementation notes.
- `docs/adr/0010-upload-outbox-via-raft.md` - committed upload obligation and confirmation authority.
- `docs/adr/0011-pebble-projection-key-prefixes.md` - scoped Upload Outbox scans.
- `docs/adr/0012-otel-evidence-plane.md` - telemetry and redaction requirements.
- `docs/adr/0015-prodlike-kind-cell-cilium-and-gates.md` - upload pressure Tier 2/Tier 3 gate.
- `docs/adr/0016-phase-4-partial-eviction-boundary.md` - eviction/admission scope separation.
- `internal/shard/upload_pressure.go`
- `internal/shard/upload_controller.go`
- `internal/shard/upload_obligations.go`
- `internal/shard/block_upload_lifecycle.go`
- `internal/shard/blockfiles.go`
- `internal/shard/upload_pressure_test.go`
- `internal/shard/upload_outbox_boundary_test.go`
- `internal/server/upload_pressure_test.go`
- `internal/admin/server.go`
- `internal/cmd/app.go`
- `test/e2e/upload_e2e_test.go`
- `test/stress/main.go`
- `https://github.com/grpc/grpc-go/blob/master/Documentation/rpc-errors.md`
- `https://github.com/open-telemetry/opentelemetry-go/blob/main/sdk/metric/doc.go`

## Dev Agent Record

### Agent Model Used

TBD by dev-story.

### Debug Log References

### Completion Notes List

- Created the Story 3.2 upload-pressure evidence artifact before code changes, including authority path, AC coverage map, gap list, and planned verification commands.
- Added `TestUploadOutboxRefreshPressureCombinesCommittedAndLocalObligations` to prove committed pending uploads and unique local obligations drive pending bytes, pending Blocks, and critical pressure level.
- Strengthened `TestUploadPressureRejectsWritesAndResumesAfterDrain` so recovery requires the pending upload to drain through confirmation before a later write is accepted.
- Strengthened `TestSealTriggeredUploadPressureRejectsCurrentWrite` so a pressure-rejected write is not visible through head/read/find, leaves no Openlog prep file, and the active Block remains reusable after pressure drains.
- Added `TestUploadOTelMetricsUsesBoundedAttributes` to lock upload metrics to bounded `scrap.shard_id` and `status` attributes.
- Updated the Epic 3 upload-pressure evidence artifact with PASS rows for focused, race, package, and broad local gates; recorded the Tier 2 E2E target as CONCERNS because it was skipped without `SCRAP_E2E=1`.
- Addressed BMAD code-review findings by bounding upload metric status values at the OTel metrics boundary, asserting all collected upload metrics have explicit bounded expectations, exercising pressure rejection in an existing Transaction, scoping Openlog checks to prep files, and proving the current active Block stays header-only after rejection.

### File List

- `_bmad-output/implementation-artifacts/3-2-upload-pressure-and-safe-admission-evidence.md`
- `_bmad-output/implementation-artifacts/epic-3-upload-pressure-evidence.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `internal/shard/upload_metrics_otel.go`
- `internal/shard/upload_metrics_otel_test.go`
- `internal/shard/upload_outbox_boundary_test.go`
- `internal/shard/upload_pressure_test.go`

### Change Log

- 2026-06-11: Added focused upload-pressure evidence tests, completed Story 3.2 evidence artifact, and verified focused/race/package/broad local gates.
- 2026-06-11: Fixed BMAD code-review findings, reran focused gates and `make check`, and moved Story 3.2 to done.
