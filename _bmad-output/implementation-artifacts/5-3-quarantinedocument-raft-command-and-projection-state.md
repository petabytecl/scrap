---
baseline_commit: ba8a309f702339a5e6d922878a37ce99324b0eb7
created: 2026-06-12T13:54:44-04:00
---

# Story 5.3: QuarantineDocument Raft Command and Projection State

Status: ready-for-dev

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a security operator,
I want scanner detections to converge through Raft-owned quarantine metadata,
so that unsafe content state is replicated authority, not local scanner state.

## Traceability

- Epic: Epic 5 - Security Operators Can Contain Unsafe Content Without Mutating Documents.
- Requirements: FR-11 and FR-12.
- Governing decision: DG-1 in `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md`.
- Governing ADRs: ADR 0008 async content scanning, ADR 0025 admin surface amendment, ADR 0026 multi-Shard release boundary.
- Current baseline: Story 5.2 is done and pushed at `ba8a309f702339a5e6d922878a37ce99324b0eb7`.
- Related future scope: Story 5.4 owns `ReadDocument` denial and public metadata `scan_status`; Story 5.5 owns admin HTTP list/inspect/confirm/release; Story 5.6 owns `scrapctl` quarantine UX; Story 5.7 owns content-safety closure evidence.

## Acceptance Criteria

1. **AC-5.3.1 - Detection proposes metadata-only Raft command.** Given scanner detection identifies a suspicious Document, when the detection is accepted, then a `QuarantineDocument` Raft command is proposed without Document bytes or raw scanner payloads. Evidence identifies the proto/Raft boundary and command.
2. **AC-5.3.2 - Committed command materializes sparse Projection state.** Given the command commits, when Projection state is updated, then a dedicated Content Quarantine prefix materializes the quarantined Document identity. Evidence proves Projection state is rebuildable from Raft.
3. **AC-5.3.3 - Shard/Raft owns quarantine authority.** Given `internal/avscan` reports a hit, when quarantine state changes, then Shard/Raft owns the state transition, not the scanner package. Evidence records the changed-boundary list and authority path.
4. **AC-5.3.4 - Replay rebuilds quarantine from Raft.** Given committed quarantine metadata is replayed after restart, when Projection state rebuilds, then Content Quarantine state reconciles from Raft without consulting transient scanner memory. Evidence records replay/rebuild behavior.

## Tasks / Subtasks

- [ ] Create the Story 5.3 evidence artifact before production code changes. (AC: 1-4)
  - [ ] Create `_bmad-output/implementation-artifacts/epic-5-quarantinedocument-raft-projection-evidence.md`.
  - [ ] Record baseline commit, changed-boundary list, Raft command field summary, Projection key summary, replay/rebuild commands, redaction scans, and final `PASS`/`CONCERNS`/`FAIL` rows.
  - [ ] Keep closure scoped to Story 5.3. Do not claim read denial, public metadata scan status, admin confirm/release, `scrapctl` quarantine UX, or Epic 5 closure.

- [ ] Add the metadata-only Raft wire contract. (AC: 1, 3)
  - [ ] Update `proto/scrap/v1/raft.proto` with an additive `QuarantineDocument` oneof variant. Do not renumber existing variants; `5` and `6` are already trace context fields and `7` is `rewrap_doc`.
  - [ ] Add a `QuarantineDocument` message that carries bounded metadata only: `transaction_id`, `document_name`, optional `block_id` evidence, `detected_at_us`, and a bounded scan type/reason. Do not carry Document bytes, rule text, signature names, raw scanner payloads, dependency error strings, or file paths.
  - [ ] Include the ADR 0008 scan-type distinction (`INITIAL`/`RESCAN`) for operational triage if a scan-type field is added. Unknown/empty values must be validated before apply changes Projection state.
  - [ ] Regenerate `gen/go/scrap/v1/raft.pb.go` through the repo protobuf toolchain. Do not edit generated files by hand.
  - [ ] Add proto/apply tests that prove the command can be marshaled, decoded, and dispatched without affecting existing command compatibility.

- [ ] Add Content Quarantine Projection storage in `internal/index`. (AC: 2, 4)
  - [ ] Add a focused quarantine Projection file, for example `internal/index/content_quarantine.go`, instead of mixing quarantine state into Transaction entries, scanner watermarks, Upload Outbox, or Confirmed Upload Catalog.
  - [ ] Use the ADR 0008 sparse key shape `q\x01<transaction_id>\x00<document_name>` unless implementation discovers a concrete collision; changing the storage shape requires documenting the rationale because ADR 0008 names this prefix.
  - [ ] Store only bounded quarantine metadata needed for later read/admin stories. Keep Block bytes untouched and do not mutate `.blk` or `.idx` files.
  - [ ] Provide narrow methods such as put/get/list/delete or put/get only if later stories can add list/delete without speculative work. Include sentinel not-found errors and strict decode validation.
  - [ ] Ensure quarantine keys participate in `Index.StreamingHash()` because Content Quarantine is replicated read-side authority, unlike Story 5.2 scanner watermarks.
  - [ ] Add index tests for missing state, put/get, duplicate idempotency, corrupt/truncated/unknown-version values, key ordering or point-get behavior, and StreamingHash changes when quarantine state differs.

- [ ] Wire Shard-owned proposal and apply behavior. (AC: 1-4)
  - [ ] Add a Shard-level method or coordinator path that accepts bounded scanner detections and proposes `QuarantineDocument` through the existing Raft proposer.
  - [ ] Add `applyQuarantineDocumentCommand` dispatch in `internal/shard/apply.go` and keep the actual Projection mutation close to `internal/shard/projection.go` or a small sibling file.
  - [ ] Apply must validate Document identity with existing store validation, reject empty/invalid scan metadata, and return bounded errors without raw scanner payloads.
  - [ ] Apply must be idempotent for duplicate commands for the same `(transaction_id, document_name)` and must not corrupt Transaction entries or increment Document counts.
  - [ ] If the command references a Block ID, verify it is metadata evidence only. Do not infer visibility or Shard ownership from local files, Backend objects, scanner memory, or Block lifecycle state.
  - [ ] Add apply-span telemetry mapping for `quarantine_document` using hashed Document identity by default and no raw scanner payload attributes.

- [ ] Extend `internal/avscan` hit reporting without granting scanner authority. (AC: 1, 3)
  - [ ] Extend `avscan.Result` with a bounded list of detections or a callback interface that reports affected Documents by identity. Keep the interface consumer-defined and minimal.
  - [ ] `internal/avscan` must not import `internal/index`, `internal/shard`, generated Raft command types, gRPC status packages, or admin packages.
  - [ ] Scheduler behavior remains Story 5.1/5.2 compliant: leader-only, post-ACK, non-blocking for writes, stream-based over `Block.OpenBytes`, serialized `RunOnce`, retry-safe after scan failures, and progress persisted only after successful Block scan completion.
  - [ ] A detection proposal failure must make scanner/quarantine status observable with bounded reason, but it must not block writes or treat scanner state as Projection authority.

- [ ] Prove replay/rebuild and restart behavior. (AC: 2, 4)
  - [ ] Add tests where a committed `QuarantineDocument` command applies, the Projection is reopened or rebuilt, and the quarantine point-get still resolves from Raft-applied state.
  - [ ] Add tests where scanner memory is empty after restart but committed quarantine state remains in the Projection after replay.
  - [ ] Add duplicate command tests and corrupt Projection value tests. Corrupt quarantine Projection state should fail closed for the quarantine lookup path introduced in this story, while public read behavior remains unchanged until Story 5.4.
  - [ ] Do not add sleeps. Use direct apply, fake proposers, manual ticks, contexts, or bounded polling with clear failure messages.

- [ ] Update story, evidence, and sprint artifacts. (AC: 1-4)
  - [ ] Move this story to `in-progress` when implementation starts and to `review` only after local verification is complete.
  - [ ] Update the evidence artifact and this story with debug log references, completion notes, review findings, and file list.
  - [ ] Run `bmad-code-review`; address critical/high findings before marking `done`.

## Dev Notes

### Current State

- Story 5.1 added `internal/avscan` scheduler lifecycle, stream-based Block scanning, bounded scanner status, OpenTelemetry metrics, and Shard/admin/`scrapctl` status visibility.
- Story 5.2 added Projection-backed scanner watermarks and deterministic rescan priority. The scanner watermark key is intentionally excluded from `Index.StreamingHash()` because leader-only progress is not replica consistency authority.
- `internal/avscan.Result` currently reports only `ResultClean` or `ResultDetected` plus a scanned Document count. It does not identify affected Documents yet.
- `proto/scrap/v1/raft.proto` currently has `commit_doc`, `consistency_check`, `seal_block`, `confirm_upload`, and `rewrap_doc`. Trace context occupies fields `5` and `6`; do not reuse those numbers.
- `internal/shard/apply.go` decodes `RaftCommand`, maps apply-span attributes, and dispatches Projection mutations. Existing document identity spans are hashed by default through `telemetry.DocumentIdentityAttributes`.
- `internal/index` owns Pebble Projection prefixes. Transaction entries, Upload Outbox, Confirmed Upload Catalog, and scanner watermarks are separate files and separate key prefixes.
- `HeadDocument`, `ReadDocument`, and `FindDocuments` do not currently consult quarantine state. That is intentional for Story 5.3; Story 5.4 owns read denial and metadata status.

### Existing Code To Reuse

- `proto/scrap/v1/raft.proto` - source of truth for Raft command wire contract.
- `gen/go/scrap/v1/raft.pb.go` - regenerated output from `make proto`/`buf generate`; never edit by hand.
- `internal/shard/apply.go` - command dispatch and apply-span operation mapping.
- `internal/shard/projection.go` - Projection mutation patterns under `s.mu`, duplicate/idempotent apply behavior, and Projection error mapping.
- `internal/shard/openlog_write_attempt.go` - example of constructing a metadata-only Raft command from Shard-owned state.
- `internal/shard/upload_outbox_events.go` and `internal/shard/upload.go` - examples of small proposal helpers and Raft command marshaling.
- `internal/index/upload_outbox.go` - compact binary value versioning, key prefixes, iterators, and not-found sentinel patterns.
- `internal/index/confirmed_upload_catalog.go` - strict JSON record validation for richer persisted metadata.
- `internal/index/scanner_watermark.go` - bounded scanner signature-version validation and Projection-local state, with the important caveat that quarantine state must not be excluded from `StreamingHash()`.
- `internal/avscan/types.go` and `internal/avscan/scheduler.go` - scanner Result, Engine, Scheduler, progress, cancellation, metrics, and duplicate-safe flow.
- `internal/shard/scanner.go` and `internal/shard/scanner_progress.go` - Shard-owned scanner coordinator and Projection-backed scanner progress adapter.

### Implementation Guardrails

- Raft, not scanner memory, owns Content Quarantine state. `internal/avscan` may report detections; `internal/shard` proposes and applies authority.
- `QuarantineDocument` must carry metadata only. Never include Document bytes, Frame bytes, scanner rule text, raw signature names, clamd/YARA dependency logs, filesystem paths, Backend keys, trace IDs, request IDs, or gRPC metadata.
- Content Quarantine is distinct from Block Quarantine. Do not rename `.blk`/`.idx`, write `.quarantine` filesystem markers, call Deep Scrub repair, or involve `internal/localblock`.
- Do not change write ACK behavior. Scanner detections are post-ACK and proposal failures must not block writes.
- Do not add public `scan_status` fields, `ReadDocument` denial, admin HTTP endpoints, admin authz changes, audit events, or `scrapctl` commands in this story.
- Do not add a new admin gRPC service. ADR 0025 amends ADR 0008: future quarantine management uses existing admin HTTP plus `scrapctl`.
- Do not add per-Document scan-clean state. This story stores only sparse quarantine hits.
- Keep quarantine state Shard-local. Do not introduce cross-Shard/global registries or infer Shard ownership from local files, Backend objects, hostnames, peer addresses, or scanner state.
- Validation belongs at system boundaries: proto/apply input, Projection encode/decode, scanner detection ingestion, and proposal path.
- Logs, metrics, spans, and evidence must use bounded labels and hashed identifiers by default. Public/local-debug raw identifier behavior must follow existing telemetry identifier mode rules.

### Latest Tech Information

- No new external runtime dependency is needed for Story 5.3. Use repo-pinned Go/protobuf/Buf/Pebble versions from `go.mod`, `tools.go.mod`, `buf.gen.yaml`, and `Makefile`.
- Protobuf changes are additive source changes under `proto/`; regenerate generated code with `make proto` or `buf generate`, and verify with `make proto-check`.
- `scrapd` remains `CGO_ENABLED=0` and `FROM scratch`; quarantine metadata work must not add native scanner packages, shells, sidecars, or runtime tools.

### Project Structure Notes

Likely update during implementation:

- `proto/scrap/v1/raft.proto`
- `gen/go/scrap/v1/raft.pb.go`
- `internal/index/content_quarantine.go`
- `internal/index/content_quarantine_test.go`
- `internal/index/index.go`
- `internal/avscan/types.go`
- `internal/avscan/scheduler.go`
- `internal/avscan/scheduler_test.go`
- `internal/shard/apply.go`
- `internal/shard/apply_span_test.go`
- `internal/shard/projection.go` or `internal/shard/content_quarantine.go`
- `internal/shard/content_quarantine_test.go`
- `internal/shard/scanner.go`
- `internal/shard/scanner_wiring_test.go`
- `_bmad-output/implementation-artifacts/5-3-quarantinedocument-raft-command-and-projection-state.md`
- `_bmad-output/implementation-artifacts/epic-5-quarantinedocument-raft-projection-evidence.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

Avoid unless a failing test proves it is required:

- `proto/scrap/v1/document.proto`, public gRPC response shapes, server read handlers, admin HTTP handlers, `scrapctl`, Block format, Backend object identity, Local Block Lifecycle, deployment overlays, scanner engine runtime dependencies, or a new `internal/quarantine` package.

### Testing Requirements

Run proto and targeted package tests after implementation:

```bash
make proto-check
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/avscan ./internal/shard -count=1
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

Run leak scans over story, evidence, and touched quarantine/scanner/proto/shard/index code. Keep patterns in shell variables so commands do not self-match copied sensitive text:

```bash
secret_shape_pattern='(AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-[A-Za-z0-9-]{10,}|-----BEGIN (RSA |EC |OPENSSH |)PRIVATE KEY-----)'
quarantine_sensitive_pattern='([t]ransaction_id=|[d]ocument_name=|[i]dempotency[_-]?[k]ey=|Backend [k]ey:|trace[_-]?[i]d=|request[_-]?[i]d=|[s]ignature=|[r]ule=|clamd_[e]rror=|yara_[e]rror=|[f]ile[_-]?[p]ath=)'
scan_scope='_bmad-output/implementation-artifacts/5-3-quarantinedocument-raft-command-and-projection-state.md _bmad-output/implementation-artifacts/epic-5-quarantinedocument-raft-projection-evidence.md proto/scrap/v1/raft.proto gen/go/scrap/v1/raft.pb.go internal/index/content_quarantine.go internal/index/content_quarantine_test.go internal/index/index.go internal/avscan/types.go internal/avscan/scheduler.go internal/avscan/scheduler_test.go internal/shard/apply.go internal/shard/apply_span_test.go internal/shard/projection.go internal/shard/content_quarantine.go internal/shard/content_quarantine_test.go internal/shard/scanner.go internal/shard/scanner_wiring_test.go'
rg -n --pcre2 "$secret_shape_pattern" $scan_scope
rg -n --pcre2 "$quarantine_sensitive_pattern" $scan_scope
```

### Previous Story Intelligence

- Story 5.2 committed and pushed `8c10dc0 feat: persist scanner watermarks`; review fixes committed and pushed `ba8a309 fix: address scanner watermark review findings`.
- Review fixes that must not regress:
  - signature-version changes clear process-local duplicate suppression;
  - higher-than-known persisted frontiers reset to safe rescan;
  - Projection rebuild cannot leave scanner progress wired to a stale closed Pebble handle;
  - scanner watermarks do not affect replica consistency hash;
  - persistent progress requires both progress store and signature-version provider;
  - duplicate lister Block IDs are skipped;
  - progress-save cancellation is reported as cancellation;
  - numeric frontier gaps do not advance persisted scanner progress.
- Story 5.2 final gate passed after review fixes: `env GOCACHE=/tmp/scrap-v2-go-build make check`.
- Recent commit shape:
  - `ba8a309 fix: address scanner watermark review findings`
  - `8c10dc0 feat: persist scanner watermarks`
  - `ccc2844 docs: create story 5.2 scanner watermarks`
  - `c7092a0 fix: address content scanner review findings`
  - `ab02a69 feat: add content scanner scheduler`

### References

- `CONTEXT.md` - Content Quarantine glossary, Block Quarantine distinction, Content Scanner flow, write ACK contract, and V2 process rules.
- `_bmad-output/project-context.md` - Go package boundaries, testing rules, proto generation rules, no raw identifier telemetry, and static scratch image rules.
- `_bmad-output/planning-artifacts/epics.md` - Epic 5 split and Story 5.3 acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-11 async scanner and FR-12 replicated Content Quarantine requirements.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - DG-1 scanner/quarantine architecture, wire/storage changes, authority patterns, and test patterns.
- `docs/adr/0008-async-content-scanning-architecture.md` - `QuarantineDocument`, sparse quarantine prefix, async scanner, and read behavior decisions.
- `docs/adr/0025-content-quarantine-admin-surface.md` - admin-surface amendment and security/redaction rules.
- `docs/adr/0026-multi-shard-v2-release-boundary.md` - scanner/quarantine remain Shard-local authority flows.
- `docs/go-style-guide.md` - proto source-of-truth, package boundaries, errors, concurrency, tests, and metrics conventions.
- `_bmad-output/implementation-artifacts/5-2-scanner-watermarks-and-rescan-priority.md` - previous story implementation record and review findings.
- `_bmad-output/implementation-artifacts/epic-5-scanner-watermarks-rescan-evidence.md` - previous story final evidence.

## Dev Agent Record

### Agent Model Used

GPT-5 Codex for story creation.

### Debug Log References

- 2026-06-12T13:54:44-04:00 - Story 5.3 created from sprint status after Story 5.2 implementation, BMAD code review, review fixes, commit, and push completed.

### Completion Notes List

- Ultimate context engine analysis completed - comprehensive developer guide created.
- Story 5.3 is ready for dev-story implementation.

### File List

- `_bmad-output/implementation-artifacts/5-3-quarantinedocument-raft-command-and-projection-state.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

### Change Log

- 2026-06-12 - Created Story 5.3 developer context and moved sprint status to ready-for-dev.
