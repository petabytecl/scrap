---
baseline_commit: 672d7d7738d74b1e4bd7c317e3497e2ca08fa05d
created: 2026-06-12T14:44:19-04:00
---

# Story 5.4: Quarantined Read Denial and Metadata Reconciliation

Status: review

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a billing service engineer,
I want unsafe Documents denied on read while metadata stays available,
so that reconciliation can continue without serving quarantined bytes.

## Traceability

- Epic: Epic 5 - Security Operators Can Contain Unsafe Content Without Mutating Documents.
- Requirements: FR-12.
- Governing decision: DG-1 in `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md`.
- Governing ADRs: ADR 0008 async content scanning, ADR 0025 admin surface amendment, ADR 0026 multi-Shard release boundary.
- Current baseline: Story 5.3 is done and pushed at `672d7d7738d74b1e4bd7c317e3497e2ca08fa05d`.
- Related future scope: Story 5.5 owns admin HTTP quarantine operations; Story 5.6 owns `scrapctl` quarantine UX; Story 5.7 owns content-safety closure evidence.

## Acceptance Criteria

1. **AC-5.4.1 - Quarantined reads deny bytes.** Given a Document is in Content Quarantine, when `ReadDocument` is called, then the operation returns `FAILED_PRECONDITION` with a bounded quarantine reason and no Document bytes. Evidence proves no bytes are returned.
2. **AC-5.4.2 - Metadata remains available with scan status.** Given the same Document is queried through `HeadDocument` or `FindDocuments`, when metadata is returned, then scan status is visible for reconciliation. Evidence proves metadata responses remain bounded and redacted.
3. **AC-5.4.3 - Read/quarantine races fail closed.** Given quarantine state races with read visibility, when the read path evaluates state, then it fails closed and never returns unsafe bytes. Evidence records race handling and fail-closed behavior.
4. **AC-5.4.4 - Replay/read reconciliation stays fail closed.** Given quarantine metadata is replayed while reads are active, when metadata and Projection state reconcile, then reads continue to fail closed until committed authority permits bytes. Evidence records replay/read-race behavior.

## Tasks / Subtasks

- [x] Create the Story 5.4 evidence artifact before production code changes. (AC: 1-4)
  - [x] Create `_bmad-output/implementation-artifacts/epic-5-quarantined-read-metadata-evidence.md`.
  - [x] Record baseline commit, changed-boundary list, public status/error contract, read-denial proof, metadata status proof, race/replay proof, redaction scans, and final `PASS`/`CONCERNS`/`FAIL` rows.
  - [x] Keep closure scoped to Story 5.4. Do not claim admin HTTP operations, `scrapctl` quarantine UX, confirm/release lifecycle, or Epic 5 closure.

- [x] Add the public metadata scan-status contract. (AC: 2)
  - [x] Update `proto/scrap/v1/document.proto` with an additive `ScanStatus` enum and `scan_status` fields on `HeadDocumentResponse` and `DocumentMeta`. Do not renumber existing fields.
  - [x] Include the ADR 0008 public states `CLEAN`, `QUARANTINED`, and `UNSCANNED`; reserve the zero value for an unspecified default.
  - [x] Do not add per-Document clean authority in this story. Until a committed clean state exists, non-quarantined existing Documents should surface as `UNSCANNED`.
  - [x] Regenerate `gen/go/scrap/v1/document.pb.go` and related gRPC output through the repo protobuf toolchain. Do not edit generated files by hand.
  - [x] Update proto/server contract tests so `HeadDocument` and `FindDocuments` expose the expected scan status.

- [x] Add store-domain status and precondition error types. (AC: 1-3)
  - [x] Extend `internal/store.DocumentMeta` with a bounded scan-status value.
  - [x] Add a typed store error for quarantined reads that unwraps to a precondition sentinel and carries public reason `QUARANTINED_AV`.
  - [x] Map the quarantine precondition to gRPC `codes.FailedPrecondition` in `internal/server`, using `google.rpc.ErrorInfo` if it fits the existing error-detail style.
  - [x] Keep core store and Shard packages free of gRPC imports.
  - [x] Ensure error messages and details are bounded and do not include raw scanner payloads, rule text, signatures, file paths, Backend keys, trace IDs, request IDs, or Document bytes.

- [x] Gate Shard reads on Content Quarantine state. (AC: 1, 3, 4)
  - [x] In `internal/shard`, consult the committed Content Quarantine Projection via point-get before serving `ReadDocument` bytes.
  - [x] Perform the gate while holding the Shard Projection lock and before `ensureReadableBlockLockedForReason`, restore, Block open, or decrypt work can return bytes.
  - [x] Return no reader, zero metadata, and the typed quarantine precondition when a matching quarantine record exists.
  - [x] If the quarantine lookup itself is corrupt or unavailable, fail closed: return an error and do not fall through to byte serving.
  - [x] Preserve existing leader-read, validation, restore-first, encryption, and data-loss behavior for non-quarantined Documents.

- [x] Reconcile metadata status for `HeadDocument` and `FindDocuments`. (AC: 2, 4)
  - [x] Set `ScanStatusQuarantined` on metadata when a matching Content Quarantine record exists.
  - [x] Set `ScanStatusUnscanned` for existing non-quarantined Documents until a later story adds clean-state authority.
  - [x] Keep `FindDocuments` Transaction-scoped and routed to exactly one owning Shard. Do not add cross-Shard quarantine registries.
  - [x] Preserve Transaction document-count integrity and include quarantined Documents in metadata results.
  - [x] Keep metadata bounded: scan status is an enum only, not a scanner rule, signature, dependency error, or operator note.

- [x] Update public server behavior. (AC: 1-3)
  - [x] Map store scan statuses to `scrap.v1.ScanStatus` in `HeadDocument` and `FindDocuments`.
  - [x] Ensure `ReadDocument` returns `FAILED_PRECONDITION` before sending read metadata or chunks for quarantined Documents.
  - [x] Add tests proving server-streaming reads emit no metadata and no chunks on quarantine denial.
  - [x] Preserve authentication, authorization, audit, rate-limit, cancellation, and route-unavailable behavior.

- [x] Prove fail-closed race and replay behavior. (AC: 3, 4)
  - [x] Add deterministic tests for quarantine-before-read, read-after-quarantine-apply, and replayed quarantine Projection state denying reads.
  - [x] Add coverage where metadata remains available while read bytes are denied.
  - [x] Add corrupt quarantine Projection coverage showing reads do not serve bytes when quarantine state cannot be trusted.
  - [x] Do not use sleeps. Use direct apply, fake proposers, manual channels, contexts, or bounded polling with clear failure messages.

- [ ] Update story, evidence, and sprint artifacts. (AC: 1-4)
  - [x] Move this story to `in-progress` when implementation starts and to `review` only after local verification is complete.
  - [x] Update the evidence artifact and this story with debug log references, completion notes, review findings, and file list.
  - [ ] Run `bmad-code-review`; address critical/high findings before marking `done`.

## Dev Notes

### Current State

- Story 5.1 added `internal/avscan` scheduler lifecycle, bounded scanner status, and Shard/admin/`scrapctl` scanner diagnostics.
- Story 5.2 added Projection-backed scanner watermarks and deterministic rescan priority. Scanner watermarks remain progress evidence, not Document visibility authority.
- Story 5.3 added metadata-only `QuarantineDocument` Raft commands and sparse Content Quarantine Projection state under `q\x01<transaction_id>\x00<document_name>`.
- Story 5.3 review fixes made scanner progress wait for committed local quarantine apply before advancing, so a detection acknowledged by `ReportDetections` has reached local Projection authority.
- `HeadDocument`, `ReadDocument`, and `FindDocuments` currently do not consult Content Quarantine state. This story owns that public read/metadata behavior.
- `proto/scrap/v1/document.proto` currently has no scan status fields. `HeadDocumentResponse` and `DocumentMeta` currently use fields 1-5.
- `internal/store.DocumentMeta` currently contains name, content type, size, SHA-256, and creation time only.
- `internal/server.mapStoreError` currently maps store errors to `AlreadyExists`, `NotFound`, `InvalidArgument`, `ResourceExhausted`, `Unavailable`, and `DataLoss`; it has no `FailedPrecondition` mapping yet.

### Existing Code To Reuse

- `proto/scrap/v1/document.proto` - source of truth for public read and metadata wire contracts.
- `gen/go/scrap/v1/document.pb.go` and gRPC output - regenerated output from the proto toolchain; never edit by hand.
- `internal/store/store.go` and `internal/store/errors.go` - store-facing metadata and typed error patterns.
- `internal/server/server.go` - public gRPC response mapping, stream-read ordering, and store error mapping.
- `internal/shard/shard.go` - `HeadDocument`, `ReadDocument`, `FindDocuments`, and `readDocumentFromProjection`.
- `internal/index/content_quarantine.go` - Content Quarantine point-get authority and validation.
- `internal/shard/content_quarantine.go` and `internal/shard/content_quarantine_test.go` - Raft apply/proposal behavior and replay evidence from Story 5.3.
- `internal/shard/read_lifecycle_test.go`, `internal/shard/read_verification_test.go`, and `internal/shard/restore_test.go` - read-path failure and restore-first patterns.
- `internal/server/metadata_test.go`, `internal/server/read_verification_test.go`, and `internal/server/find_documents_test.go` - public metadata/read contract patterns.

### Implementation Guardrails

- `ReadDocument` must deny bytes using committed Content Quarantine Projection state, not scanner memory or scheduler status.
- Content Quarantine is metadata-level Document gating. Do not mutate Block bytes, `.blk` files, `.idx` files, Backend objects, Transaction entries, or upload catalogs.
- Keep Content Quarantine separate from Block Quarantine and Deep Scrub repair.
- Do not implement admin HTTP list/inspect/confirm/release, `scrapctl` quarantine commands, or operator release workflows in this story.
- Do not add a new admin gRPC service. ADR 0025 amends ADR 0008 to use existing admin HTTP plus `scrapctl`.
- Do not add per-Document clean-state authority. Public enum compatibility may include `CLEAN`, but absent quarantine state maps to `UNSCANNED` for now.
- Fail closed when quarantine state cannot be trusted. Returning an error is acceptable; serving bytes is not.
- Keep logs, metrics, spans, gRPC status details, and evidence bounded and redacted.
- Public APIs may expose bounded `scan_status` enum and bounded precondition reason. They must not expose raw scanner payloads, signature names, YARA/ClamAV rule text, dependency logs, local file paths, Backend keys, trace IDs, request IDs, or Document bytes.

### Latest Tech Information

- No new external runtime dependency is needed for Story 5.4. Use repo-pinned Go/protobuf/Buf/Pebble versions from `go.mod`, `tools.go.mod`, `buf.gen.yaml`, and `Makefile`.
- Protobuf changes are additive source changes under `proto/`; regenerate generated code with `make proto` or the repo's existing proto target, then verify with `make proto-check`.
- `scrapd` remains `CGO_ENABLED=0` and `FROM scratch`; read-gate work must not add native scanner packages, shells, sidecars, or runtime tools.

### Project Structure Notes

Likely update during implementation:

- `proto/scrap/v1/document.proto`
- `gen/go/scrap/v1/document.pb.go`
- `gen/go/scrap/v1/document_grpc.pb.go` if the generator changes service descriptors.
- `internal/store/store.go`
- `internal/store/errors.go`
- `internal/store/*test.go`
- `internal/server/server.go`
- `internal/server/*quarantine*_test.go` or focused metadata/read tests.
- `internal/shard/shard.go`
- `internal/shard/content_quarantine.go`
- `internal/shard/content_quarantine_test.go`
- `internal/shard/read_lifecycle_test.go` or focused quarantine read tests.
- `_bmad-output/implementation-artifacts/5-4-quarantined-read-denial-and-metadata-reconciliation.md`
- `_bmad-output/implementation-artifacts/epic-5-quarantined-read-metadata-evidence.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

Avoid unless a failing test proves it is required:

- `proto/scrap/v1/raft.proto`, Raft command shape, scanner scheduler behavior, admin HTTP handlers, `scrapctl`, Block format, Backend object identity, Local Block Lifecycle, deployment overlays, or scanner engine runtime dependencies.

### Testing Requirements

Run proto and targeted package tests after implementation:

```bash
make proto-check
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/store ./internal/server ./internal/shard -run 'Quarantine|ScanStatus|ReadDocument|HeadDocument|FindDocuments' -count=1
```

Run targeted package gates:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/store ./internal/server ./internal/shard ./internal/index ./internal/admin ./internal/cmd ./internal/scrapctl -count=1
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

Run leak scans over story, evidence, and touched quarantine/read/status code. Keep patterns in shell variables so commands do not self-match copied sensitive text:

```bash
secret_shape_pattern='(AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-[A-Za-z0-9-]{10,}|-----BEGIN (RSA |EC |OPENSSH |)PRIVATE KEY-----)'
quarantine_sensitive_pattern='([t]ransaction_id=|[d]ocument_name=|[i]dempotency[_-]?[k]ey=|Backend [k]ey:|trace[_-]?[i]d=|request[_-]?[i]d=|[s]ignature=|[r]ule=|clamd_[e]rror=|yara_[e]rror=|[f]ile[_-]?[p]ath=)'
scan_scope='_bmad-output/implementation-artifacts/5-4-quarantined-read-denial-and-metadata-reconciliation.md _bmad-output/implementation-artifacts/epic-5-quarantined-read-metadata-evidence.md proto/scrap/v1/document.proto gen/go/scrap/v1/document.pb.go internal/store/store.go internal/store/errors.go internal/server/server.go internal/shard/shard.go internal/shard/content_quarantine.go internal/shard/content_quarantine_test.go internal/index/content_quarantine.go'
rg -n --pcre2 "$secret_shape_pattern" $scan_scope
rg -n --pcre2 "$quarantine_sensitive_pattern" $scan_scope
```

### Previous Story Intelligence

- Story 5.3 committed and pushed `6b08381 feat: add quarantine raft projection state`; review fixes committed and pushed `672d7d7 fix: address quarantine raft review findings`.
- Review fixes that must not regress:
  - scheduler rejects detections unless scan result is `detected`;
  - detection batches are bounded by `avscan.MaxDetectionsPerBlock`;
  - cancellation and deadline errors are preserved;
  - positive `detected_at_us` is required before Projection state changes;
  - quarantine identity validation uses store byte limits;
  - full detection batches are validated before any proposal;
  - scanner progress waits for matching committed local apply;
  - replay evidence applies Raft entries into a fresh Projection;
  - corrupt quarantine decode wraps `index.ErrInvalidContentQuarantine`.
- Story 5.3 final gate passed after review fixes: `env GOCACHE=/tmp/scrap-v2-go-build make check`.
- Recent commit shape:
  - `672d7d7 fix: address quarantine raft review findings`
  - `6b08381 feat: add quarantine raft projection state`
  - `b4d43cd docs: create story 5.3 quarantine raft state`
  - `ba8a309 fix: address scanner watermark review findings`
  - `8c10dc0 feat: persist scanner watermarks`

### References

- `CONTEXT.md` - Content Quarantine glossary, Block Quarantine distinction, and read behavior vocabulary.
- `_bmad-output/project-context.md` - Go package boundaries, testing rules, proto generation rules, no raw identifier telemetry, and static scratch image rules.
- `_bmad-output/planning-artifacts/epics.md` - Epic 5 split and Story 5.4 acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-12 Content Quarantine read gate and admin operations.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - DG-1 scanner/quarantine architecture, read behavior, wire/storage changes, and test patterns.
- `docs/adr/0008-async-content-scanning-architecture.md` - read denial, `scan_status`, and sparse quarantine prefix decisions.
- `docs/adr/0025-content-quarantine-admin-surface.md` - admin-surface amendment and redaction rules.
- `docs/adr/0026-multi-shard-v2-release-boundary.md` - scanner/quarantine remain Shard-local authority flows.
- `docs/go-style-guide.md` - proto source-of-truth, package boundaries, errors, concurrency, tests, and metrics conventions.
- `_bmad-output/implementation-artifacts/5-3-quarantinedocument-raft-command-and-projection-state.md` - previous story implementation record and review findings.
- `_bmad-output/implementation-artifacts/epic-5-quarantinedocument-raft-projection-evidence.md` - previous story final evidence.

## Dev Agent Record

### Agent Model Used

GPT-5 Codex for story creation.

### Debug Log References

- 2026-06-12T14:44:19-04:00 - Story 5.4 created from sprint status after Story 5.3 implementation, BMAD code review, review fixes, commit, and push completed.
- 2026-06-12T14:47:32-04:00 - Dev-story workflow started from pushed baseline `3cc94e44bf32b9a069859f7bb262a6ba741b8016`; story and sprint status moved to in-progress.
- 2026-06-12T14:50:00-04:00 - Added RED tests for public scan status, store precondition error, server `FAILED_PRECONDITION` mapping, Shard read denial, metadata reconciliation, replay, and corrupt quarantine fail-closed behavior.
- 2026-06-12T14:54:00-04:00 - Implemented additive `ScanStatus` proto fields, store scan status/precondition types, server mapping, and Shard Content Quarantine read/metadata gates.
- 2026-06-12T15:02:49-04:00 - Final implementation gates passed: focused store/server/shard tests, targeted package gate, `make proto-check`, `git diff --check`, `git diff --cached --check`, `scripts/check-e2e-gates.sh`, redaction scans, and `env GOCACHE=/tmp/scrap-v2-go-build make check`.

### Completion Notes List

- Created Story 5.4 from the next backlog item in `sprint-status.yaml`.
- Scoped implementation to public read denial and metadata scan status only.
- Added public `ScanStatus` metadata fields for `HeadDocument` and `FindDocuments`, with non-quarantined Documents reporting `UNSCANNED` until clean-state authority exists.
- Added store-domain scan status and a bounded quarantine precondition error reason `QUARANTINED_AV`.
- Added Shard read gating against committed Content Quarantine Projection state before Block restore/open/decrypt can serve bytes.
- Preserved metadata availability for quarantined Documents through `HeadDocument` and `FindDocuments`.
- Added server mapping to gRPC `FAILED_PRECONDITION` with bounded `ErrorInfo` and no stream metadata/chunks on denial.
- Added replay and corrupt-quarantine fail-closed tests.

### File List

- `_bmad-output/implementation-artifacts/5-4-quarantined-read-denial-and-metadata-reconciliation.md`
- `_bmad-output/implementation-artifacts/epic-5-quarantined-read-metadata-evidence.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `proto/scrap/v1/document.proto`
- `gen/go/scrap/v1/document.pb.go`
- `internal/store/store.go`
- `internal/store/errors.go`
- `internal/store/errors_test.go`
- `internal/store/proto_document_contract_test.go`
- `internal/server/server.go`
- `internal/server/quarantine_read_test.go`
- `internal/server/read_verification_test.go`
- `internal/shard/content_quarantine.go`
- `internal/shard/content_quarantine_read_test.go`
- `internal/shard/shard.go`
- `internal/index/content_quarantine.go`

## Change Log

| Date | Version | Description | Author |
| --- | --- | --- | --- |
| 2026-06-12 | 0.1 | Initial ready-for-dev story created from Epic 5 Story 5.4. | GPT-5 Codex |
| 2026-06-12 | 0.2 | Started dev-story implementation. | GPT-5 Codex |
| 2026-06-12 | 1.0 | Implemented quarantined read denial and metadata scan status; moved story to review. | GPT-5 Codex |
