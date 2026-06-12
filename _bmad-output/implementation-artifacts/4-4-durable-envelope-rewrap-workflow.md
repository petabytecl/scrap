---
baseline_commit: ceb447dd663e40a0f22c83f31ed2d7aa0c6c2153
created: 2026-06-12T02:18:53-04:00
---

# Story 4.4: Durable Envelope Rewrap Workflow

Status: ready-for-dev

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a security engineer,
I want envelope metadata rewrapped durably through Raft,
so that key rotation converges without rewriting Block payload bytes.

## Traceability

- Epic: Epic 4 - Operators Can Run Fail-Closed Security and OpenBao Workflows.
- Requirement: FR-10 - OpenBao envelope encryption and durable rewrap.
- Security slice: #407 - durable rewrap workflow and evidence.
- Governing ADRs: ADR 0020 - OpenBao envelope encryption contract, and ADR 0021 - durable rewrap Raft command.
- Current baseline: Story 4.3 is done at `ceb447dd663e40a0f22c83f31ed2d7aa0c6c2153`; encrypted write/read evidence, disabled-encryption read fail-closed coverage, OpenBao adapter integration, `make check`, and post-review leak scans were committed and pushed before this story was created.
- Related future stories: Story 4.5 and 4.6 own `scrapctl openbao bootstrap`; Story 4.7 owns production security rehearsal closure with real mTLS/OpenBao gates and full evidence-bundle aggregation.

## Acceptance Criteria

1. **AC-4.4.1 - Rewrap state is Raft authority.** Given a rewrap request is authorized, when rewrap runs, then envelope metadata changes converge through committed Raft state. Evidence proves rewrap state is replicated authority, not local operator state.
2. **AC-4.4.2 - Retry and interruption are idempotent and redacted.** Given rewrap is interrupted or retried, when the workflow resumes, then it is idempotent and does not leak key material. Evidence records retry behavior and redaction checks.
3. **AC-4.4.3 - Reads remain valid and Block bytes unchanged.** Given rewrap completes, when encrypted reads run across Members, then plaintext verification still succeeds without rewriting Block payload bytes. Evidence proves Block bytes remain unchanged while envelope metadata converges.
4. **AC-4.4.4 - In-flight old/new metadata does not orphan envelopes.** Given rewrap is interrupted after old and new envelope metadata both exist in flight, when the workflow resumes or replay runs, then it does not orphan old envelopes or expose ambiguous decrypt behavior. Evidence records the interrupted-rewrap fixture and resumed state.

## Tasks / Subtasks

- [ ] Create the Story 4.4 evidence artifact before behavior changes. (AC: 1-4)
  - [ ] Create `_bmad-output/implementation-artifacts/epic-4-durable-envelope-rewrap-evidence.md`.
  - [ ] Record baseline commit, timestamp, owner, exact files reviewed, current coverage, gaps, commands, expected results, actual results, redaction proof, and remaining Story 4.7 production-rehearsal scope.
  - [ ] Use strict result language per row: `PASS`, `CONCERNS`, or `FAIL`; do not use hybrid phrases.
  - [ ] If existing code already satisfies a row, prove it with current tests or source evidence. Do not mark any row pass from intent, ADRs, architecture, or old story notes alone.

- [ ] Audit and reuse the existing durable rewrap implementation. (AC: 1-4)
  - [ ] Read and preserve `internal/shard/rewrap.go`, `internal/rewrap/types.go`, `proto/scrap/v1/raft.proto`, `internal/encryption/transit.go`, `internal/encryption/fake.go`, `internal/encryption/openbao.go`, and relevant tests before editing.
  - [ ] Reuse `Shard.RewrapDocument`, `RewrapDocumentEnvelope`, `rewrap.Result`, `rewrap.HealthSnapshot`, `encryption.Transit.RewrapDataKey`, `block.ReplaceDocumentEnvelope`, and generation-aware Upload Outbox logic before adding any new abstraction.
  - [ ] Confirm admin exposure goes through `internal/admin` with authorization, audit, rate-limit behavior, and redacted responses already established by Story 4.2.
  - [ ] Do not add a new crypto library, Transit wrapper, background scan, local marker state, Projection-only state, Backend index-only update, direct Backend rewrite, alternate envelope format, or duplicate admin route unless the evidence proves the current boundary cannot satisfy an AC.

- [ ] Close Raft authority and convergence evidence. (AC: 1)
  - [ ] Prove authorized `RewrapDocument` creates a committed `RewrapDocumentEnvelope` Raft command and waits for apply before reporting success.
  - [ ] Prove Raft apply replaces `.idx` envelope metadata, keeps Pebble Projection derived, and does not use local operator state as completion authority.
  - [ ] Prove `proposal_id` correlates only the leader waiter and does not change replay identity.
  - [ ] Prove followers/replay apply the command deterministically. Use the narrowest reliable existing fixture; if a true multi-Member fixture exists, include it, otherwise record package-level apply/replay evidence and leave production multi-Member rehearsal to Story 4.7.

- [ ] Close retry, resume, and interruption evidence. (AC: 2, 4)
  - [ ] Prove retrying a request for an already-current key version is idempotent and does not rewrite metadata or Block payload bytes.
  - [ ] Prove stale rewrap commands cannot downgrade a newer envelope and do not close or break the current index writer.
  - [ ] Prove overlapping proposals are isolated by proposal ID and cannot notify the wrong waiter.
  - [ ] Prove old and new envelope metadata in flight cannot orphan the old envelope or create ambiguous decrypt behavior; stale old-version commands must be ignored safely and resumed state must remain readable.
  - [ ] Prove Transit unavailable, auth denied, missing key, minimum-version rejection, invalid request, and not-encrypted cases return bounded reasons and preserve the existing readable Document.

- [ ] Close no-Block-rewrite and read verification evidence. (AC: 3, 4)
  - [ ] Prove rewrap changes only envelope metadata and never rewrites `.blk` Frame payload bytes.
  - [ ] Prove encrypted reads after rewrap still decrypt through the normal Shard path and verify plaintext SHA-256 before returning data.
  - [ ] Prove rewrapped encrypted restore uses the new envelope metadata while restored Block bytes still omit plaintext and remain byte-equivalent to the pre-rewrap Block.
  - [ ] Prove sealed historical Blocks with upload enabled requeue replacement upload obligations by non-zero upload generation and that stale pre-rewrap confirmations cannot clear the replacement obligation.
  - [ ] Prove non-zero upload generation appears in replacement Backend object keys and stale in-flight upload writers cannot overwrite replacement `.idx` objects.

- [ ] Close admin health, audit, and redaction evidence. (AC: 1-4)
  - [ ] Prove `/admin/rewrap/document` requires `admin_operator` authorization and denies before side effects for weaker roles.
  - [ ] Prove the route is audited as a Document operation and bounded failure reasons are recorded.
  - [ ] Prove `/healthz` exposes bounded rewrap status and failures by reason without `transaction_id`, `document_name`, wrapped-key ciphertext, plaintext, data keys, raw paths, provider bodies, or tokens.
  - [ ] Prove returned errors and evidence artifacts classify broad secret/identifier matches and have zero strict shaped-value leaks.

- [ ] Preserve package, authority, and storage boundaries. (AC: 1-4)
  - [ ] Keep Transit and envelope operations in `internal/encryption`; rewrap contracts in `internal/rewrap`; Shard orchestration and Raft apply in `internal/shard`; admin HTTP mapping in `internal/admin`; Block index mutation in `internal/block`; Backend bytes opaque in `internal/backend`.
  - [ ] Do not move rewrap authority into `internal/backend`, `internal/server`, `internal/peer`, `internal/index`, `internal/scrapctl`, evidence tooling, or local filesystem markers.
  - [ ] Do not change storage identity, Shard membership authority, public/peer/admin wire contracts, Backend object identity, or Pebble Projection authority for this story.
  - [ ] Do not edit generated `gen/` files directly. If proto/storage/envelope contract changes become unavoidable, update `proto/`, run generation/check gates, and justify ADR impact before proceeding.

- [ ] Update story, evidence, and tracker artifacts. (AC: 1-4)
  - [ ] Update this story with debug logs, completion notes, review findings, and file list.
  - [ ] Update `_bmad-output/implementation-artifacts/epic-4-durable-envelope-rewrap-evidence.md` with final AC matrix rows and command evidence.
  - [ ] Move `_bmad-output/implementation-artifacts/sprint-status.yaml` to `review` only when implementation and local verification are complete.
  - [ ] Do not mark Story 4.7 production rehearsal or evidence-bundle closure complete from Story 4.4 package tests.

- [ ] Run verification and leak scans. (AC: 1-4)
  - [ ] Run focused unit/package tests listed below.
  - [ ] Run affected package regression listed below.
  - [ ] Run OpenBao adapter integration when Docker/Testcontainers are available.
  - [ ] Run `git diff --check`.
  - [ ] Run `env GOCACHE=/tmp/scrap-v2-go-build make check` before code review because this story closes security/encryption lifecycle behavior.
  - [ ] Run credential and identifier leak scans over the new evidence artifact, this story, and touched code. Classify matches as forbidden, allowed fixture/test vocabulary, allowed policy vocabulary, or artifact prose.
  - [ ] If a command is skipped, record the skip reason and closure impact in the evidence artifact. Do not mark an AC as pass from intent alone.

## Dev Notes

### Current State

- `CONTEXT.md` defines Document, Transaction, Block, Frame, Shard, Cell, Member, Backend, Pebble Projection, Local Block Lifecycle, and OpenBao Transit vocabulary. Use those terms exactly.
- FR-10 requires encrypted writes before Block persistence, decrypted reads through the normal path, and durable envelope rewrap through Raft without rewriting Block payload bytes or leaking key material.
- ADR 0020 requires rewrap to be a durable metadata lifecycle operation. Rewrap asks Transit to update wrapped data-key/envelope ciphertext without exposing plaintext, records success through Raft metadata, is idempotent for already-current envelopes, and needs OpenBao rewrap capability without plaintext export.
- ADR 0021 is accepted and defines the current Raft/wire/storage contract for this story: additive `RewrapDocumentEnvelope`, proposal IDs, stale command no-ops, deterministic follower/replay apply, historical Block preflight for upload-enabled leaders, and generation-aware ConfirmUpload/Backend keys.
- Architecture says any field affecting rewrap progress or readiness is Raft-owned. Derived stores, evidence bundles, logs, metrics, traces, audit records, and Backend inventory may observe but must not decide rewrap completion or read availability.
- Story 4.3 proved encrypted write/read local crypto path and OpenBao adapter integration. Story 4.4 must build on that work and close durable rewrap evidence; do not re-open basic write/read encryption unless a rewrap-specific gap requires it.

### Existing Code To Reuse

- `internal/shard/rewrap.go` implements `Shard.RewrapDocument`, request validation, leader/encryption preconditions, envelope snapshot, Transit `RewrapDataKey`, Raft proposal, apply wait, `.idx` envelope replacement, stale envelope handling, upload requeue, result mapping, and health snapshots.
- `internal/rewrap/types.go` defines bounded JSON request/result/health contracts and reasons. These are the admin/evidence shape; do not add raw key material or raw Document identifiers to health.
- `proto/scrap/v1/raft.proto` already contains `RewrapDocumentEnvelope` and generation-aware `ConfirmUpload`. If behavior can be proven with the existing fields, avoid proto churn.
- `internal/encryption/fake.go` and `internal/encryption/openbao.go` already support `RewrapDataKey`. Fake Transit models rotation, outage, auth denied, missing key, minimum-version, bad wrapped keys, and future-version failures.
- `internal/block/index.go` owns envelope replacement through `ReplaceDocumentEnvelope`. Do not parse or rewrite Block indexes ad hoc outside `internal/block`.
- `internal/shard/upload.go`, `internal/shard/upload_controller.go`, and `internal/shard/block_upload_lifecycle.go` already carry `upload_generation` through pending uploads, ConfirmUpload apply, committed authority, and Backend key prefix construction.
- `internal/admin/rewrap.go` owns the admin HTTP route. `internal/admin/server_test.go`, `internal/admin/authorization_test.go`, and `internal/admin/audit_ratelimit_test.go` already cover safe response shape, authorization, and audit behavior for rewrap.

### Likely Gaps To Close

- Current code already has substantial Story 4.4 coverage. The highest-risk gap is evidence completeness, not missing infrastructure. Start by building the AC matrix and proving each row with current tests.
- Confirm whether existing tests cover "across Members" literally. If not, add the smallest appropriate multi-Member or Raft replay fixture, or record package-level deterministic replay evidence and keep production multi-Member rehearsal in Story 4.7.
- Confirm interrupted/retried behavior covers both client interruption before/after apply and replay after process restart. Existing stale apply, already-applied replay, proposal ID, and restore tests may be enough only if the evidence ties them to the AC precisely.
- Confirm redaction scans include this story and the new rewrap evidence artifact, not just code.
- Confirm admin health/evidence includes failure visibility for rewrap without raw `transaction_id`, `document_name`, wrapped-key ciphertext, plaintext, data keys, provider response bodies, tokens, or raw paths.

### Previous Story Intelligence

- Story 4.3 review found that evidence must use exact file lists and reproducible leak-scan commands. Avoid globs in final evidence unless the command output records the concrete expanded scope.
- Story 4.3 added `TestEncryptedShardReadFailsClosedWhenShardEncryptionDisabled` after review. Keep the same standard here: if an AC is only implied by source, add a focused test or record why an existing named test is sufficient.
- Story 4.3 separated local crypto-path evidence from Story 4.7 production rehearsal. Apply the same split: Story 4.4 can close durable rewrap package/local evidence, but real mTLS/OpenBao production security rehearsal stays with Story 4.7 unless a production gate actually runs.
- Story 4.2 established admin authorization, audit, and rate-limit patterns. Reuse `internal/admin` route tests rather than creating duplicate authz machinery in Shard or scrapctl.
- Recent commits keep story creation, implementation/evidence, and review-fix commits separated. Commit and push before continuing to the next work.

### Implementation Guidance

- Start with evidence and focused tests. Do not change production code unless a named AC row lacks current proof.
- Prefer adding focused tests in existing files before changing production code: `internal/shard/rewrap_apply_test.go`, `internal/shard/encryption_test.go`, `internal/shard/restore_test.go`, `internal/shard/upload_apply_test.go`, `internal/shard/upload_outbox_boundary_test.go`, `internal/admin/*_test.go`, and `internal/encryption/*_test.go`.
- For AC-4.4.1, prove the path from authorized admin request to `Shard.RewrapDocument`, Raft proposal, apply, and `.idx` envelope replacement. Do not claim Raft authority from a direct helper call alone unless paired with command/apply proof.
- For AC-4.4.2 and AC-4.4.4, model crash/interruption through apply/replay fixtures where possible: stale old-version command, already-applied replacement, proposal waiter mismatch, context cancellation around proposal wait, and repeated operator request.
- For AC-4.4.3, compare `.blk` bytes before/after rewrap, then read the Document through normal Shard read and verify plaintext. Include sealed/restored Block coverage if using upload-enabled Shards.
- For upload-generation evidence, prove stale pre-rewrap confirmations are ignored and replacement pending uploads survive until a matching generation confirms.
- Use fake Transit for deterministic Tier 1 failures. Use OpenBao testcontainer only for adapter parity, and classify skips if Docker/Testcontainers are not available.
- Do not add transparent migration for old unencrypted Blocks, metadata encryption, tenant-specific key policy, direct Backend ciphertext streaming, cold-only read behavior, or OpenBao bootstrap UX in this story.

### Project Structure Notes

Likely update during implementation:

- `_bmad-output/implementation-artifacts/4-4-durable-envelope-rewrap-workflow.md` - story status, debug log, completion notes, review findings, and file list.
- `_bmad-output/implementation-artifacts/epic-4-durable-envelope-rewrap-evidence.md` - AC matrix, source evidence, commands, redaction checks, and remaining Story 4.7 scope.
- `internal/shard/rewrap_apply_test.go` - Raft apply/replay, stale command, proposal ID, and upload-generation coverage if gaps remain.
- `internal/shard/encryption_test.go` - Shard-level rewrap, no Block rewrite, idempotency, failure, and read-after-rewrap coverage if gaps remain.
- `internal/shard/restore_test.go` - rewrapped restore and upload-enabled read coverage if gaps remain.
- `internal/shard/upload_apply_test.go` and `internal/shard/upload_outbox_boundary_test.go` - stale confirmation and generation coverage if gaps remain.
- `internal/admin/*_test.go` - admin authorization/audit/health redaction coverage if gaps remain.
- `internal/encryption/*_test.go` - fake/OpenBao rewrap parity and failure mapping if gaps remain.
- `_bmad-output/implementation-artifacts/sprint-status.yaml` - status transitions.

Likely avoid:

- `internal/backend`, `internal/server`, `internal/peer`, `internal/index`, `internal/scrapctl`, deployment manifests, OpenBao bootstrap CLI, production rehearsal closure docs, release closure docs, and generated `gen/` files.

No new ADR is required if the implementation follows ADR 0020 and ADR 0021. Create or update an ADR only if the implementation changes storage format, wire protocol, dependency choices, security/encryption/auth contracts, envelope metadata contract, upload generation semantics, or cross-package ownership boundary.

### Testing Requirements

Run focused Shard rewrap tests:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'Rewrap|rewrap|UploadGeneration|ConfirmUpload|BackendKeyPrefix|RestoreUsesRewrappedEnvelope|EncryptedShardRewrap' -count=1 -v
```

Run focused admin rewrap tests:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/admin -run 'Rewrap|AuthorizationDeniesRewrap|AuditsRewrap|HealthEndpointReportsBoundedRewrapStatus' -count=1 -v
```

Run focused Transit rewrap tests:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/encryption -run 'Rewrap|FakeTransit|OpenBao' -count=1 -v
```

Run proto/wire contract tests if proto or Raft command behavior is touched:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/store -run 'RewrapDocumentEnvelope|ConfirmUpload' -count=1 -v
```

Run the OpenBao adapter integration test when Testcontainers/Docker are available:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test -tags integration ./test/integration -run TestIntegrationOpenBaoTransitContainerRoundTrip -count=1 -v
```

Run affected package regression:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/encryption ./internal/block ./internal/shard ./internal/store ./internal/admin ./internal/cmd ./internal/server -count=1
```

Run leak scans with patterns kept in shell variables so the command does not self-match copied secrets:

```bash
cred_pattern='(?i)(api[_-]?[k]ey|[s]ecret|[p]assword|[t]oken|[b]earer|[a]uthorization|aws_access_key_[i]d|aws_[s]ecret_access_[k]ey|private [k]ey|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-)'
identifier_pattern='([t]ransaction_id|[d]ocument_name|[i]dempotency|Backend [k]ey|Backend object [k]ey|wrapped[- ][k]ey|data [k]ey|Transit [t]oken|plaintext data [k]ey|Frame payload|trace [I]D|request [I]D|gRPC [m]etadata|auth [c]laims|peer [a]ddress|[c]ertificate|/shards/|/tmp/|/home/)'
strict_value_pattern='(AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-|BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY|aws_access_[k]ey_id|aws_[s]ecret_access_[k]ey)'
scan_scope='_bmad-output/implementation-artifacts/4-4-durable-envelope-rewrap-workflow.md _bmad-output/implementation-artifacts/epic-4-durable-envelope-rewrap-evidence.md internal/encryption internal/block internal/shard internal/store internal/admin internal/cmd internal/server'
rg -n --pcre2 "$cred_pattern" $scan_scope
rg -n --pcre2 "$identifier_pattern" $scan_scope
rg -n --pcre2 "$strict_value_pattern" $scan_scope
```

Run broad gates before review:

```bash
git diff --check
env GOCACHE=/tmp/scrap-v2-go-build make check
```

If a command is skipped, record the skip reason and closure impact in the evidence artifact. Do not mark an AC as pass from intent alone.

### Latest Technical Information

- OpenBao's current Transit API documentation is Version 2.5.x and still documents configurable Transit mount paths; `POST /transit/rewrap/:name`; `ciphertext`, `context`, `key_version`, and `nonce` parameters; and rewrap semantics that do not return plaintext. This matches the existing `internal/encryption.OpenBaoTransit.RewrapDataKey` boundary.
- OpenBao 2.5.x release notes and GitHub releases show `v2.5.4` released on May 20, 2026. `test/integration/testinfra/openbao/openbao.go` currently pins `openbao/openbao:2.5.4`, so the integration fixture aligns with the latest checked 2.5.x release context.
- OpenBao docs say rotating a key affects future encryption, while upgrading existing ciphertext to the latest version uses the rewrap endpoint. Preserve the S.C.R.A.P. contract: Transit rewrap updates wrapped data-key/envelope metadata, while local Document payload bytes and Block Frames remain unchanged.
- OpenBao docs warn convergent encryption can make identical plaintext deterministic. ADR 0020 rejects Transit convergent encryption for Document payloads; preserve local random nonce-prefix AES-GCM payload encryption and use Transit only for data-key wrapping/rewrap.

### References

- `CONTEXT.md` - domain vocabulary, OpenBao Transit substrate, and storage gateway constraints.
- `_bmad-output/planning-artifacts/epics.md` - Epic 4 and Story 4.4 acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-10 and production encryption/rewrap consequences.
- `_bmad-output/planning-artifacts/architecture.md` - Raft-owned rewrap lifecycle, package boundaries, evidence gates, and anti-patterns.
- `docs/adr/0020-openbao-envelope-encryption-contract.md` - authoritative envelope encryption and rewrap contract.
- `docs/adr/0021-durable-rewrap-raft-command.md` - authoritative durable rewrap Raft command, proposal ID, stale command, and upload-generation contract.
- `docs/phase-4.5-security-implementation-slices.md` - #407 durable rewrap workflow and evidence.
- `_bmad-output/implementation-artifacts/4-3-openbao-backed-encrypted-write-and-read.md` - previous story implementation/review intelligence.
- `_bmad-output/implementation-artifacts/epic-4-openbao-encrypted-write-read-evidence.md` - evidence style, command recording, and scan classification pattern.
- `internal/shard/rewrap.go`
- `internal/rewrap/types.go`
- `proto/scrap/v1/raft.proto`
- `internal/shard/rewrap_apply_test.go`
- `internal/shard/encryption_test.go`
- `internal/shard/restore_test.go`
- `internal/shard/upload_apply_test.go`
- `internal/shard/upload_outbox_boundary_test.go`
- `internal/shard/upload.go`
- `internal/shard/upload_controller.go`
- `internal/shard/block_upload_lifecycle.go`
- `internal/admin/rewrap.go`
- `internal/admin/server_test.go`
- `internal/admin/authorization_test.go`
- `internal/admin/audit_ratelimit_test.go`
- `internal/encryption/transit.go`
- `internal/encryption/fake.go`
- `internal/encryption/openbao.go`
- `test/integration/openbao_transit_test.go`
- OpenBao Transit API docs: https://openbao.org/api-docs/secret/transit/
- OpenBao 2.5.x release notes: https://openbao.org/community/release-notes/2-5-0/
- OpenBao GitHub releases: https://github.com/openbao/openbao/releases

## Dev Agent Record

### Agent Model Used

{{agent_model_name_version}}

### Debug Log References

### Completion Notes List

### File List
