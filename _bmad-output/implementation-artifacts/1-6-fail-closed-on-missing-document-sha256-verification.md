---
baseline_commit: c9caa9090820e59247815e1e5f1fd8a5c1d3e740
created_at: 2026-06-14T17:26:00-04:00
---

# Story 1.6: Fail Closed on Missing Document SHA-256 Verification

Status: done

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a release owner,
I want visible Document reads to fail closed when committed metadata lacks a valid SHA-256 digest,
so that S.C.R.A.P. never serves unverified bytes.

## Traceability

- Epic: Epic 1 - Billing ETL Can Trust Immutable Document Writes and Reads.
- Requirements: FR-3, NFR-1, NFR-7, NFR-8.
- Acceptance IDs: AC-1.6.1, AC-1.6.2, AC-1.6.3, AC-1.6.4.
- Governing sources: `_bmad-output/planning-artifacts/epics.md`, `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md`, `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md`, `_bmad-output/planning-artifacts/sprint-change-proposal-2026-06-14.md`, `CONTEXT.md`, ADR 0002, ADR 0003, ADR 0014.
- GitHub issue: not assigned in the current epic artifact. Before implementation PR, link either a tracker issue or this BMAD story artifact.

## Acceptance Criteria

1. **AC-1.6.1 - Zero digest fails closed in Block read verification.** Given a Block reader entry with an all-zero SHA-256 digest, when read verification runs, then the read fails closed instead of skipping Document digest verification. Evidence includes the targeted `internal/block` regression test.
2. **AC-1.6.2 - Zero digest compatibility decision is explicit.** Given valid historical fixtures, when read verification runs, then the implementation either proves all production metadata has non-zero SHA-256 or maps zero digest entries to a typed corruption failure. Evidence records the compatibility decision and affected boundary.
3. **AC-1.6.3 - Shard read returns no unverified bytes.** Given shard-level read verification is exercised, when a zero-digest metadata fixture is visible, then S.C.R.A.P. returns a typed failure and no partial or unverified bytes. Evidence records the shard-level read verification command.
4. **AC-1.6.4 - Release evidence links the blocker closure.** Given release evidence is updated, when final closure is evaluated, then the affected FR-3 row records PASS, CONCERNS, or FAIL with the fix, test, command, and artifact linked. Evidence proves the data-integrity blocker is no longer open.

## Tasks / Subtasks

- [x] Add Block-layer red tests before production changes. (AC: 1, 2)
  - [x] Add `TestReadDocumentTwoPassRejectsZeroSHA256Digest` in `internal/block/twopass_test.go` or `internal/block/reader_test.go`.
  - [x] Use `writeSingleDocBlock` or `writeTestBlock`; set `entry.SHA256 = [32]byte{}` after a normal write and assert `ReadDocumentTwoPass` fails.
  - [x] Assert the error is typed with `errors.Is(err, block.ErrSHA256Mismatch)` or a new block sentinel if implementation creates one.
  - [x] Preserve existing `TestTwoPassCorruptSHA256`, `TestTwoPassMultiFrame`, and frame sequence/truncation behavior.
- [x] Close the parallel Block verification gap used by restore/scrub paths. (AC: 1, 2)
  - [x] Add a regression test in `internal/block/verify_test.go` proving `VerifyBlock` reports `CorruptionDocSHA256` when a plaintext `.idx` entry has an all-zero SHA-256 digest.
  - [x] Reuse `writeVerifyTestBlock` and rewrite or recreate the `.idx` entry with a zero digest in a temp directory.
  - [x] Keep encrypted-entry behavior unchanged: encrypted entries still validate stored ciphertext length through `EncryptionEnvelope` rather than plaintext `.idx` SHA in `VerifyBlock`.
- [x] Apply the minimal Block-layer fix. (AC: 1, 2)
  - [x] Update `internal/block/reader.go` so `verifyPass` treats an all-zero `entry.SHA256` as missing/corrupt Document integrity metadata.
  - [x] Update `internal/block/verify.go` so `checkDocSHA` records `CorruptionDocSHA256` when `expected == [32]byte{}`.
  - [x] Do not change Block layout, Frame encoding, `.idx` encoding, public protobufs, Raft commands, or Backend object key format.
  - [x] Do not add compatibility shims that serve zero-digest Documents as successful reads.
- [x] Add Shard-level fail-closed evidence. (AC: 3)
  - [x] Add `TestReadDocumentZeroIndexSHAFailsClosedWithoutReader` or equivalent in `internal/shard/read_verification_test.go`.
  - [x] Write a normal Document through `openTestShard`, rewrite the corresponding `.idx` entry's SHA-256 to all zeros in the temp Shard data directory, then call `ReadDocument`.
  - [x] Assert `errors.Is(err, storeapi.ErrDataLoss)`, `rc == nil`, and `meta == (storeapi.DocumentMeta{})`.
  - [x] Do not extend `internal/spike` for V2 closure evidence.
- [x] Update evidence artifacts and story record. (AC: 4)
  - [x] Update `_bmad-output/implementation-artifacts/epic-1-evidence-rollup.md` with Story 1.6 status, commands, and data-integrity blocker result.
  - [x] Update `_bmad-output/implementation-artifacts/release-evidence-matrix.md` FR-3 row to include Story 1.6 evidence and the PASS/CONCERNS/FAIL decision.
  - [x] Keep final release below PASS if other integrity blockers or contradictory release artifacts remain open.
  - [x] Record exact Debug Log References, changed-boundary list, compatibility decision, and redaction proof in this story file.
- [x] Run required verification and record exact results. (AC: 1-4)
  - [x] Run focused red tests first, for example `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/block -run 'ZeroSHA256|MissingSHA|TwoPassCorruptSHA|VerifyBlock' -count=1`.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'ZeroIndexSHA|ReadDocumentZero|ReadDocument.*FailsClosed' -count=1`.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/block/... ./internal/shard/...`.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/block ./internal/index ./internal/server ./internal/shard` to protect Epic 1 read behavior.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build make check` before moving the story to review unless a narrower gate is explicitly justified in Debug Log References.

### Review Findings

- [x] [Review][Defer] Decide whether encrypted zero-SHA256 metadata belongs in Story 1.6 — deferred, separate scope: encrypted Block verification semantics need their own story.
- [x] [Review][Patch] Assert nil reader on Block read verification error [internal/block/zero_sha256_test.go:15]
- [x] [Review][Patch] Qualify redaction proof that currently contradicts local `/tmp` debug command paths [_bmad-output/implementation-artifacts/1-6-fail-closed-on-missing-document-sha256-verification.md:230]
- [x] [Review][Patch] Remove unrelated create-story completion note from Story 1.6 completion notes [_bmad-output/implementation-artifacts/1-6-fail-closed-on-missing-document-sha256-verification.md:223]
- [x] [Review][Defer] `VerifyBlock` can report clean when an index entry has `FrameCount == 0` and all-zero SHA-256 [internal/block/verify.go:151] — deferred, pre-existing

## Dev Notes

### Current State

- This story exists because the post-audit release policy now treats any data-integrity bug as release-blocking. The V2 PRD now says any bug that can return unverified Document bytes blocks final release PASS.
- `internal/block/reader.go` currently skips Document SHA-256 verification when `entry.SHA256 == [32]byte{}`. That means a plaintext read can pass Frame CRC/sequence checks and still return bytes without document-level integrity verification.
- `internal/block/verify.go` has the same skip pattern in `checkDocSHA`. That path is used by `VerifyBlock`, which feeds scrub, repair, and restore verification. Fixing only `reader.go` would leave integrity verification inconsistent.
- Normal writes should not create zero digest entries. `block.Writer.AppendDocument` computes SHA-256 for every Document, and the SHA-256 of an empty Document is not all zeros. Treat all-zero digest as missing/corrupt metadata unless implementation proves a real compatibility exception.
- `internal/block/index.go` stores and decodes the raw `[32]byte` digest but does not validate it. Story 1.6 should fail closed at verification time rather than changing the `.idx` storage format.
- `internal/shard.readDocumentBytes` uses `block.ReadDocumentFromBlock` for plaintext Documents and encrypted frame/decrypt logic for encrypted Documents. Story 1.6 primarily closes the plaintext read and Block verification gap.
- `internal/server` must remain transport-only. Do not move Block, Frame, `.idx`, or SHA-256 verification into server handlers.

### Exact Integrity Contract

- `ReadDocument` is all-or-error. It must verify the selected source before public metadata or bytes can be returned.
- Plaintext local Block verification includes Block header, Frame header CRC, Frame payload CRC, Frame order/count, and final Document SHA-256.
- A missing all-zero SHA-256 digest is not "unknown but acceptable"; it is missing Document integrity metadata and must map to data loss/corruption.
- Shard reads must map Block verification failures through `mapReadDocumentError` to `storeapi.ErrDataLoss`.
- Public gRPC behavior, if exercised, should map this to `codes.DataLoss` with zero successful `ReadDocumentResponse` messages.
- `HeadDocument` and `FindDocuments` are metadata paths. This story is scoped to read verification and Block verification; do not expand metadata semantics unless tests or review prove the zero-digest metadata should also be rejected there.

### Files Likely To Touch

- `internal/block/reader.go`
  - Current behavior: `verifyPass` reads the indexed Frames, hashes payload bytes, then skips digest comparison if `entry.SHA256` is all zeros.
  - Story change: all-zero `entry.SHA256` must fail closed.
  - Preserve: `ReadDocumentTwoPass`, `ReadDocumentFromBlock`, frame sequence validation, and existing `ErrSHA256Mismatch` behavior for non-zero mismatches.
- `internal/block/verify.go`
  - Current behavior: `checkDocSHA` records `CorruptionDocSHA256` only if expected digest is non-zero and mismatched.
  - Story change: all-zero expected digest for plaintext entries should record `CorruptionDocSHA256`.
  - Preserve: encrypted entries still use `checkEncryptedDocPayloadLength` and `CorruptionDocCiphertextLength`.
- `internal/block/twopass_test.go` or `internal/block/reader_test.go`
  - Add the Block read red test.
- `internal/block/verify_test.go`
  - Add the `VerifyBlock` zero-digest corruption test.
- `internal/shard/read_verification_test.go`
  - Add shard-level fail-closed evidence using existing temp-dir Shard helpers.
- `_bmad-output/implementation-artifacts/epic-1-evidence-rollup.md`
  - Add Story 1.6 evidence.
- `_bmad-output/implementation-artifacts/release-evidence-matrix.md`
  - Update the FR-3 row after verification.

### Existing Helpers and Patterns To Reuse

- Block test helpers:
  - `writeSingleDocBlock` in `internal/block/twopass_test.go`
  - `writeTestBlock` in `internal/block/reader_test.go`
  - `writeVerifyTestBlock` in `internal/block/verify_test.go`
  - `block.RecomputeFramePayloadCRC` for corruption tests that preserve header CRCs
- Existing Block tests to keep green:
  - `TestTwoPassCorruptSHA256`
  - `TestReadDocumentTwoPassRejectsFrameSequenceMismatch`
  - `TestVerifyBlock_DocSHA256Mismatch`
  - `TestVerifyBlock_EncryptedEntryVerifiesStoredCiphertext`
- Shard helpers and patterns:
  - `openTestShard`
  - fail-closed assertions in `internal/shard/read_verification_test.go`
  - `corruptShardBlockByte`, `truncateShardIndex`, `corruptShardFrameSequence`
  - pattern: `rc == nil`, `meta == (storeapi.DocumentMeta{})`, `errors.Is(err, storeapi.ErrDataLoss)`

### Implementation Guardrails

- Do not change storage format or wire contracts. No ADR is needed if the fix stays in read/verify behavior and tests.
- Do not add a migration or backwards-compatibility shim that serves zero-digest Documents. AC-1.6.2 allows mapping zero-digest entries to typed corruption failure.
- Do not change Document identity. It remains `(transaction_id, document_name)`; `tenant_id` remains validation-only.
- Do not use Backend inventory, local file existence alone, telemetry, or release evidence as storage authority.
- Do not import `grpc/status` or `grpc/codes` into `internal/block`, `internal/shard`, `internal/index`, or `internal/store`.
- Do not introduce new dependencies, assertion libraries, or mocking frameworks.
- Keep tests deterministic and temp-dir scoped. Do not mutate checked-in fixtures or shared paths.
- Keep logs, metrics, evidence, and errors free of raw `transaction_id`, `document_name`, Backend keys, local paths, key material, or Document bytes.

### Compatibility Decision For Dev To Make Explicit

Use this default decision unless implementation proves otherwise:

- Production-created metadata should never require all-zero SHA-256 for a valid Document.
- Empty Document SHA-256 is `e3b0c442...`, not all zeros.
- Existing tests may have synthetic zero SHA-256 fixtures for apply/index mechanics; those may remain valid for non-read tests.
- Any visible Document read or Block verification that depends on all-zero SHA-256 should fail closed as corruption/data loss.

### Previous Story Intelligence

- Story 1.3 established that `ReadDocument` must complete verification before returning metadata or chunks. Story 1.6 must not reintroduce a reader that can discover integrity failure after public metadata is emitted.
- Story 1.3 established corruption mapping: storage errors use `storeapi.ErrDataLoss`; public gRPC uses `codes.DataLoss`; no least-bad bytes are returned.
- Story 1.5 established restart/rebuild evidence and kept strict read authority in `internal/shard`, `internal/index`, and `internal/block`. Story 1.6 should preserve those boundaries.
- Story 1.5 created `_bmad-output/implementation-artifacts/epic-1-evidence-rollup.md`; Story 1.6 must update it rather than creating a separate competing rollup.
- Story 1.3 and Story 1.5 both used exact Debug Log References, changed-boundary lists, and broad gates before review. Follow that pattern.

### Git Intelligence

- Recent commits are release/gate focused: `c9caa90 chore: finalize SCRAP naming and local gates`, `6231a9d docs(release): flip V2 final gate decision to PASS`, `8f4dce8 fix(release): stabilize release gate blockers`, `13d23dc test(e2e): Shard-scope upload pending-blocks wait`, and `b3fd781 docs: update V2 closure gate to reflect current reality`.
- Current worktree already contains planning-artifact changes from Correct Course. Do not revert or overwrite them while implementing Story 1.6.
- Local pattern from prior Epic 1 stories: red test first, minimal production fix, artifact evidence update, then full local gate.

### Technical Research Notes

- No dependency upgrade or external API research is in scope. The fix uses repo-owned code plus Go standard library SHA-256 behavior.
- Repo-pinned versions remain authoritative: Go `1.26.4`, Pebble `v1.1.5`, etcd Raft `v3.6.0`, gRPC-Go `v1.81.1`, and OpenTelemetry `v1.44.0`.
- ADR 0002 defines SHA-256 as the required Document-level integrity digest and says `ReadDocument` verifies final Document SHA-256 before sending metadata or chunks.
- Go `crypto/sha256` digest size is 32 bytes; internal Store, Block, and Index code store raw `[32]byte` digests.

### Project Structure Notes

- Story 1.6 should stay inside existing packages:
  - `internal/block` owns Block/Frame encoding, `.idx` layout, and verification.
  - `internal/shard` owns Shard read orchestration and Store error mapping.
  - `internal/server` owns transport mapping only and is likely not needed for this story.
- Expected artifact updates are under `_bmad-output/implementation-artifacts/`.
- Do not promote `internal/spike` as release evidence.

### References

- `_bmad-output/planning-artifacts/epics.md` - Story 1.6 source acceptance criteria and cross-epic release-blocking rule.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-3, NFR-1, NFR-7, NFR-8, and release rule for data-integrity bugs.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - all-or-error reads, authority separation, package boundaries, and evidence/redaction standards.
- `_bmad-output/planning-artifacts/sprint-change-proposal-2026-06-14.md` - audit trigger and approved blocker split.
- `_bmad-output/implementation-artifacts/1-3-verified-read-and-metadata-inspection.md` - prior all-or-error read contract and test/evidence pattern.
- `_bmad-output/implementation-artifacts/1-5-core-gateway-restart-and-rebuild-evidence.md` - restart/rebuild evidence, Epic 1 rollup pattern, and previous story intelligence.
- `_bmad-output/implementation-artifacts/epic-1-evidence-rollup.md` - artifact to update with Story 1.6 evidence.
- `_bmad-output/implementation-artifacts/release-evidence-matrix.md` - FR-3 row to update after implementation.
- `CONTEXT.md` - glossary and core storage/read invariants.
- `docs/adr/0002-dual-checksum-architecture.md` - CRC-32C per Frame and SHA-256 per Document; reads verify Document SHA before public output.
- `docs/adr/0003-mirror-block-layout.md` - Block and `.idx` physical format and reader validation expectations.
- `docs/adr/0014-projection-resolution-boundary.md` - strict client read behavior and fail-closed Projection Resolution.
- `docs/go-style-guide.md` - Go style, errors, testing, concurrency, and package conventions.
- `internal/block/reader.go` - `verifyPass` current zero-digest skip.
- `internal/block/verify.go` - `checkDocSHA` current zero-digest skip.
- `internal/block/twopass_test.go` - existing two-pass read tests and `writeSingleDocBlock`.
- `internal/block/verify_test.go` - existing Block verification tests and `writeVerifyTestBlock`.
- `internal/shard/read_verification_test.go` - shard-level fail-closed read tests and helper patterns.
- `internal/shard/shard.go` - `readDocumentFromProjection`, `readDocumentBytes`, and `mapReadDocumentError`.

## Dev Agent Record

### Agent Model Used

GPT-5.5

### Implementation Plan

- Add zero-digest RED tests in `internal/block` for both `ReadDocumentTwoPass` and `VerifyBlock`.
- Add shard-level RED test proving visible zero-digest metadata maps to `storeapi.ErrDataLoss` with no reader and zero metadata.
- Implement the smallest Block-layer change to reject all-zero SHA-256 in read and verify paths.
- Update Story 1.6, Epic 1 rollup, release evidence matrix FR-3 row, and sprint status with exact command results.

### Debug Log References

- FAIL (RED): `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/block -run 'TestP0.*ZeroSHA256|TestP0VerifyBlock' -count=1` - `ReadDocumentTwoPass` succeeded with all-zero SHA-256 and `VerifyBlock` reported no `doc_sha256` corruption.
- FAIL (RED): `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestP0.*ZeroSHA256' -count=1` - `ReadDocument` returned nil error for visible all-zero SHA-256 metadata.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/block -run 'TestP0.*ZeroSHA256|TestP0VerifyBlock|TestTwoPassCorruptSHA256|TestVerifyBlock_DocSHA256Mismatch|TestVerifyBlock_EncryptedEntryVerifiesStoredCiphertext' -count=1`.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestP0.*ZeroSHA256|ReadDocument.*FailsClosed|TestReadDocumentReturnsCommittedMetadataAndBytes' -count=1`.
- FAIL then PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/block/... ./internal/shard/...` - first run overlapped another Shard package test process and panicked in an unrelated concurrent restore test; rerun by itself passed.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/block ./internal/index ./internal/server ./internal/shard`.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build make check` - completed format diff, package boundaries, buf lint/generate/gen diff, golangci-lint, `go test ./...`, `go test -race ./...`, integration tests, and command builds.

### Completion Notes List

- Added P0 Block tests for all-zero Document SHA-256 metadata in `ReadDocumentTwoPass` and `VerifyBlock`.
- Added P0 Shard read test proving visible all-zero SHA-256 metadata returns `storeapi.ErrDataLoss`, no reader, and zero `DocumentMeta`.
- Updated `internal/block/reader.go` so all-zero SHA-256 is treated as missing Document integrity metadata and fails with `ErrSHA256Mismatch`.
- Updated `internal/block/verify.go` so all-zero plaintext SHA-256 records `CorruptionDocSHA256` for Block verification used by scrub/restore.
- Compatibility decision: production-created Document metadata must not rely on all-zero SHA-256; empty Documents still have a non-zero SHA-256 digest. Any visible read or Block verification with all-zero SHA-256 fails closed as corruption/data loss.
- Changed-boundary list: `internal/block` read/verify behavior and tests, `internal/shard` read verification tests, Epic 1 evidence rollup, FR-3 release matrix row, sprint status, and this story artifact. No storage format, proto, Raft command, Backend key, public API, or package ownership boundary changed.
- Redaction proof: no new deployed logs, metrics, traces, public responses, or release evidence rows include raw Document payload bytes, Backend keys, key material, request IDs, trace IDs, or auth claims. Debug command lines intentionally include local `GOCACHE=/tmp/...` paths and are not deployed evidence.
- Code review patches resolved: nil-reader assertion added for the Block read verification error path, redaction wording qualified, and unrelated create-story completion note removed.

### File List

- `_bmad-output/implementation-artifacts/1-6-fail-closed-on-missing-document-sha256-verification.md`
- `_bmad-output/implementation-artifacts/epic-1-evidence-rollup.md`
- `_bmad-output/implementation-artifacts/release-evidence-matrix.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `_bmad-output/planning-artifacts/epics.md`
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md`
- `_bmad-output/planning-artifacts/sprint-change-proposal-2026-06-14.md`
- `_bmad-output/test-artifacts/automation-summary.md`
- `internal/block/reader.go`
- `internal/block/verify.go`
- `internal/block/zero_sha256_test.go`
- `internal/shard/zero_sha256_read_test.go`

### Change Log

- 2026-06-14: Created Story 1.6 Fail Closed on Missing Document SHA-256 Verification context package and marked it ready for development.
- 2026-06-14: Implemented Story 1.6 zero-SHA256 fail-closed behavior, updated evidence, and moved story to review.
- 2026-06-14: Applied code review patches and resolved three review findings.
