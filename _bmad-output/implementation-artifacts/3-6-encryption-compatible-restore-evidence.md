---
baseline_commit: d5e36e12ec1e7065db9a0b45fce0d696d89cf7b6
created: 2026-06-11T23:37:59-04:00
---

# Story 3.6: Encryption-Compatible Restore Evidence

Status: ready-for-dev

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a security operator,
I want restore-first reads to preserve the encrypted Block contract,
so that cold reads do not bypass OpenBao, checksum, or plaintext verification behavior.

## Traceability

- Epic: Epic 3 - Operators Can Prove Backend Durability and Restore-First Cold Reads.
- Requirements: FR-8.
- Governing ADRs: ADR 0020, ADR 0021, and ADR 0027.
- Prerequisites: Stories 3.1 through 3.5 are done. Story 3.4 proved restore-first cold reads with one fake-Transit encrypted restore path; Story 3.5 proved restore failure taxonomy, public error sanitization, retry budget, cancellation, and no-partial-publish behavior.
- Current implementation intelligence: `internal/shard/restore_test.go` already has `TestEncryptedReadDocumentRestoresThenUsesEnvelopePath`, which writes an encrypted Document, uploads and evicts the Block, restores it, reads through the envelope path, verifies plaintext, asserts the restored Block omits plaintext, and checks envelope metadata. Story 3.6 must turn that into complete encryption-compatible restore evidence and fill the fail-closed, rewrap, wrong-key-version, and redaction gaps.
- Non-goals: direct Backend ciphertext streaming, range streaming, per-Frame remote reads, Block/Frame layout changes, Backend object key changes, public/peer/admin proto changes, production OpenBao deployment proof, real S3/IAM production rehearsal, Story 3.7 final Epic 3 closure, and V2 release readiness.

## Acceptance Criteria

1. **AC-3.6.1 - Restore preserves ciphertext storage and envelope read path.** Given Backend stores ciphertext Blocks, when restore downloads a Block, then Frame CRC verifies stored ciphertext and normal read decrypts through the envelope path. Evidence proves direct Backend ciphertext streaming remains out of V2 scope.
2. **AC-3.6.2 - Transit/key-material unavailability fails closed after restore.** Given Transit is unavailable or key material is invalid, when restored bytes are read, then the read fails closed without returning plaintext or leaking key material, wrapped-key ciphertext, Backend keys, or raw Document identifiers.
3. **AC-3.6.3 - Rewrapped envelope metadata survives restore.** Given rewrap metadata changed after upload, when a restored Document is read, then envelope metadata converges through Raft and plaintext SHA-256 verifies before return. Evidence proves key material and wrapped-key ciphertext are not leaked.
4. **AC-3.6.4 - Fixture boundary is explicit.** Given production OpenBao integration is outside this story's primary changed boundary, when Epic 3 evaluates encryption-compatible restore, then this story uses existing encryption fixtures or a test envelope adapter without claiming final production OpenBao proof. Evidence states which adapter or fixture was used and marks final production OpenBao interaction as release evidence owned by Epic 4.
5. **AC-3.6.5 - Unavailable key service and wrong key version fail closed.** Given the key service is unavailable or a wrong key version is selected, when restored ciphertext is read, then the read fails closed without plaintext leakage. Evidence records unavailable-key-service and wrong-key-version cases.

## Tasks / Subtasks

- [ ] Build the Story 3.6 evidence artifact before production-code changes. (AC: 1-5)
  - [ ] Create `_bmad-output/implementation-artifacts/epic-3-encryption-restore-evidence.md` with AC rows, exact commands, results, adapter/fixture scope, leak-scan allowlist, and remaining concerns.
  - [ ] Record the encryption-compatible restore path: encrypted write -> sealed Block upload -> committed Confirmed Upload Catalog -> local `.blk` eviction -> Backend ciphertext restore -> staged `.blk` verification -> normal local read -> envelope unwrap/decrypt -> plaintext SHA-256 verification.
  - [ ] State that direct Backend ciphertext streaming remains out of V2 per ADR 0027 and is not implemented or claimed.
  - [ ] State that real production OpenBao interaction is Epic 4 release evidence unless a later story explicitly broadens scope.
- [ ] Strengthen ciphertext restore proof. (AC: 1, 4)
  - [ ] Reuse `TestEncryptedReadDocumentRestoresThenUsesEnvelopePath` rather than creating a second read path.
  - [ ] Prove Backend-resident Block bytes and restored local Block bytes do not contain the plaintext marker.
  - [ ] Prove restore uses committed Confirmed Upload Catalog metadata and full-object Backend `GetObject`, not Backend list, HEAD, direct stream-to-client, or per-Frame/range reads.
  - [ ] Assert the read returns plaintext only after the restored local Block is present and normal envelope decryption succeeds.
- [ ] Prove Transit and key-material failures fail closed after restore. (AC: 2, 5)
  - [ ] Add focused Shard restore tests that write/upload/evict with a working fake Transit, then make `ReadDocument` restore the encrypted Block while Transit unwrap is unavailable, auth denied, missing key, or minimum-version rejected.
  - [ ] For each failure, assert `storeapi.ErrUnavailable` with `storeapi.UnavailableReasonCryptoUnavailable` unless existing mapping documents a narrower safe outcome.
  - [ ] Assert no plaintext bytes are returned to the caller and no public/deployed error text includes plaintext, wrapped-key ciphertext, Backend object keys, Transit tokens, Transaction IDs, Document names, paths, or dependency detail strings.
  - [ ] If restore succeeds before decryption fails, explicitly document that the restored local Block remains ciphertext and the read still fails closed.
- [ ] Prove rewrapped metadata converges through restore. (AC: 3)
  - [ ] Build a scenario around existing `RewrapDocument` behavior: write encrypted Document, seal/upload, rotate fake Transit, rewrap to a newer key version while the leader has the local Block, wait for replacement upload/Confirmed Upload Catalog metadata if the implementation requeues upload generation, evict the local `.blk`, restore, and read successfully.
  - [ ] Assert restored reads use the current index/envelope metadata and return plaintext matching the original Document bytes.
  - [ ] Assert rewrap does not rewrite Block payload bytes and restore does not require a Backend plaintext object.
  - [ ] Assert evidence and failure output do not leak wrapped-key ciphertext or key material.
- [ ] Preserve architecture and security boundaries. (AC: 1-5)
  - [ ] Keep Transit/envelope primitives in `internal/encryption`, restore orchestration in `internal/shard`, Block/Frame verification in `internal/block`, Backend byte storage in `internal/backend`, and public status mapping in `internal/server`.
  - [ ] Do not import OpenBao client types into Shard, Backend, Block, server, peer, admin, or public protobuf packages.
  - [ ] Do not change public, peer, admin, Raft, Block/Frame, Backend object, or Pebble formats without a new ADR and explicit migration plan.
  - [ ] Do not introduce new runtime dependencies, assertion libraries, mocking frameworks, global key caches, plaintext fallbacks, or production fake-Transit allowances.
- [ ] Run focused and regression verification. (AC: 1-5)
  - [ ] Run focused encrypted restore, encrypted read/key-failure, rewrap, Store/server public error, and evidence-bundle tests.
  - [ ] Run package regression for `internal/shard`, `internal/encryption`, `internal/block`, `internal/backend`, `internal/server`, `internal/store`, `internal/rewrap`, and `internal/scrapctl/evidencebundle`.
  - [ ] Run Shard race coverage for encrypted restore and rewrap paths if production concurrency or restore code changes.
  - [ ] Run `make check` before BMAD code review unless a concrete blocker is recorded in the story.
  - [ ] Run credential/identifier leak scans over touched story/evidence files and changed code.

## Dev Notes

### Current State

- `CONTEXT.md` defines Backend as opaque cold durability and says Documents are immutable after ACK. Do not introduce S3-compatible behavior, Backend inventory authority, or a second API-level Document identity.
- PRD FR-8 requires restore-first cold reads: when all local `.blk` copies are evicted, `ReadDocument` restores the full Block from Backend, verifies it, and serves through the normal local read path. Direct Backend ciphertext streaming, range streaming, and per-Frame remote reads are out of V2 unless re-chartered.
- ADR 0027 says encryption behavior remains unchanged during restore: Backend stores ciphertext Blocks, restore downloads ciphertext Blocks, Frame CRC verifies ciphertext storage integrity, normal read decrypts through the envelope path, and plaintext SHA-256 verifies before return.
- ADR 0020 says Frame CRC covers ciphertext, reads verify plaintext SHA-256 after decrypt, key-material failures fail closed, and tests use deterministic fake Transit for outage, auth-denied, missing-key, rewrap, and minimum-version behavior.
- ADR 0021 says rewrap updates envelope metadata through Raft without rewriting Block payload bytes. For sealed historical Blocks, the leader validates local `.blk` presence before rewrap requeue; upload confirmation carries generation metadata so stale uploads cannot clear replacement obligations.
- Story 3.4 already proved a fake-Transit encrypted restore path but did not claim Story 3.6 production encryption closure. Story 3.6 must explicitly close the fixture/adaptor evidence gap while keeping production OpenBao proof out of scope.
- Story 3.5 added public `DATA_LOSS` sanitization and capped retry behavior. Preserve those public error and redaction rules when adding crypto-unavailable restore tests.

### Existing Implementation To Reuse

- `internal/shard/restore.go`: restore remains full-Block restore through committed Confirmed Upload Catalog metadata, staging, verification, and atomic publish. Do not add direct Backend streaming.
- `internal/shard/restore_test.go`: reuse `stageEvictedConfirmedBlock`, `openEncryptedUploadTestShard`, `assertReadRestoreStartsFromEvictedConfirmedBlock`, `assertRestoredDocument`, `assertRestorePublishedHotBlock`, `assertBlockOmitsPlaintext`, `readOnlyIndexEntry`, and `assertEnvelopeMetadata`.
- `internal/shard/restore_test.go`: `TestEncryptedReadDocumentRestoresThenUsesEnvelopePath` is the primary AC-3.6.1 seed. Strengthen it before adding new fixtures.
- `internal/shard/encryption_test.go`: reuse `mutableTransit`, `TestEncryptedShardReadFailsClosedWhenKeyMaterialUnavailable`, `TestEncryptedShardRewrapUpdatesEnvelopeWithoutRewritingBlock`, `TestEnvelopeDecryptVerifiesPlaintextSHA256`, and `TestEncryptedShardReadMapsInvalidTransitRequest` patterns.
- `internal/encryption/fake.go`: `FakeTransit` supports unavailable, auth-denied, missing-key, `Rotate`, `RequireMinimumVersion`, rewrap, and deterministic wrapped-key parsing. Prefer it over a new fake unless a local wrapper is needed to inject one failure mode.
- `internal/encryption/openbao.go` and tests: OpenBao adapter behavior is covered at the Transit boundary. Do not require a live OpenBao server for Story 3.6 unless scope is explicitly expanded.
- `internal/server/server.go` and `internal/server/restore_unavailable_test.go`: public gRPC mapping patterns for `UNAVAILABLE`, `DATA_LOSS`, and `ErrorInfo` details. Add public crypto-unavailable coverage only if current server tests do not prove the restore encryption surface.
- `internal/scrapctl/evidencebundle/*`: evidence bundle already models `EncryptedRestoreOK`; update or reference these tests if the Story 3.6 evidence artifact changes closure semantics.

### Implementation Guidance

- Start with the evidence artifact and failing/strengthened tests. If existing tests already close an AC, record the exact command and avoid production-code churn.
- The desired implementation shape is almost certainly tests and evidence first. Production code should change only if tests prove a real gap in restore/encryption semantics.
- Keep plaintext markers as test-only byte sequences and assert they are absent from Backend/restored Block bytes. Do not put real secrets, tokens, wrapped keys, or production identifiers in fixtures.
- For key-version failure, prefer `FakeTransit.RequireMinimumVersion(2)` against a version-1 envelope or an equivalent existing fake path that maps to `encryption.ErrMinimumVersion`.
- For unavailable key service, prefer `FakeTransit{Unavailable: true}` or `mutableTransit.unwrapErr = encryption.ErrUnavailable` after the encrypted Block has already been written/uploaded/evicted with a working Transit.
- For rewrap-after-upload, ensure the sequence respects ADR 0021: if `RewrapDocument` requires the leader's local `.blk`, perform rewrap before removing the local Block, then wait for any replacement upload/Confirmed Upload Catalog state before eviction/restore.
- Public evidence can mention bounded reason strings such as `crypto_unavailable`, but must not expose dependency error strings, Transit policy details, wrapped-key ciphertext, Backend keys, raw `transaction_id`, raw `document_name`, filesystem paths, or request metadata.
- Do not mark AC-3.6.4 as production OpenBao proof. The correct result is fixture-backed PASS for Story 3.6 behavior plus an explicit remaining Epic 4 release-evidence item for real OpenBao production interaction.

### Project Structure Notes

Likely update:

- `_bmad-output/implementation-artifacts/3-6-encryption-compatible-restore-evidence.md` - story status, debug log, completion notes, file list, and review findings during dev.
- `_bmad-output/implementation-artifacts/epic-3-encryption-restore-evidence.md` - Story 3.6 evidence table, fixture boundary, commands, leak-scan allowlist, and concerns.
- `_bmad-output/implementation-artifacts/sprint-status.yaml` - status transitions.
- `internal/shard/restore_test.go` - encrypted restore, key-service failure after restore, wrong-key/minimum-version failure after restore, and rewrap-after-upload restore proof.
- `internal/shard/encryption_test.go` - only if a reusable encryption helper or existing read/rewrap assertion needs tightening.
- `internal/server/*_test.go` - only if public gRPC crypto-unavailable restore mapping lacks coverage.
- `internal/scrapctl/evidencebundle/*_test.go` - only if Story 3.6 evidence changes bundle closure expectations.

Likely avoid:

- `proto/`, `gen/`, Block/Frame layout code, Backend object key construction, Pebble key prefixes, public/peer/admin wire contracts, peer transfer code, routing/placement, production security policy, OpenBao bootstrap policy, real S3/IAM deployment evidence, and release closure docs.
- `internal/backend/*` unless a provider-neutral opaque-byte fixture bug blocks encrypted restore evidence.
- `internal/encryption/openbao.go` unless the story deliberately expands to OpenBao adapter behavior, which should remain a Transit-boundary concern and not production OpenBao proof.

### Testing Requirements

Run focused gates first:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestEncryptedReadDocumentRestoresThenUsesEnvelopePath|TestReadDocumentEncryptedRestore.*|TestEncryptedRestore.*|TestReadDocumentRestore.*Key|TestReadDocumentRestore.*Transit|TestReadDocumentRestore.*Rewrap' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestEncryptedShardReadFailsClosedWhenKeyMaterialUnavailable|TestEncryptedShardReadMapsInvalidTransitRequest|TestEncryptedShardRewrapUpdatesEnvelopeWithoutRewritingBlock|TestEnvelopeDecryptVerifiesPlaintextSHA256' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/encryption -run 'TestFakeTransit|TestOpenBaoTransit|TestErrorClass|TestProductionCapable' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server -run 'TestReadDocument.*Crypto|TestReadDocument.*Unavailable|TestReadDocument.*DataLoss' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl/evidencebundle -run 'TestGenerateFailsWhenEncryptedRestoreProofIsMissing|TestGenerate|TestGate' -count=1 -v
```

Run regression gates before code review:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard ./internal/encryption ./internal/block ./internal/backend ./internal/server ./internal/store ./internal/rewrap ./internal/scrapctl/evidencebundle -count=1
env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard -run 'Test.*Encrypted.*Restore|Test.*Restore.*Key|Test.*Restore.*Transit|Test.*Restore.*Rewrap|Test.*Rewrap' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build make check
```

Suggested leak scans. Keep patterns in shell variables so evidence files do not self-match credential-shaped terms copied into prose:

```bash
cred_pattern='(?i)(api[_-]?[k]ey|[s]ecret|[p]assword|[t]oken|[b]earer|[a]uthorization|aws_access_key_[i]d|aws_[s]ecret_access_[k]ey|private [k]ey|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-)'
identifier_pattern='([t]ransaction_id|[d]ocument_name|[i]dempotency|Backend [k]ey|Backend object [k]ey|wrapped[- ][k]ey|data [k]ey|Transit [t]oken|trace [I]D|request [I]D|gRPC [m]etadata|auth [c]laims|peer [a]ddress|[c]ertificate|/shards/|/tmp/|/home/)'
rg -n --pcre2 "$cred_pattern" _bmad-output/implementation-artifacts/3-6-encryption-compatible-restore-evidence.md _bmad-output/implementation-artifacts/epic-3-encryption-restore-evidence.md internal/shard internal/encryption internal/server internal/scrapctl/evidencebundle
rg -n --pcre2 "$identifier_pattern" _bmad-output/implementation-artifacts/3-6-encryption-compatible-restore-evidence.md _bmad-output/implementation-artifacts/epic-3-encryption-restore-evidence.md internal/shard internal/encryption internal/server internal/scrapctl/evidencebundle
```

If a command is not run, record it as skipped with a reason in the evidence artifact. Do not mark an AC as pass from intent alone.

### Previous Story Intelligence

- Story 3.5 moved restore failure semantics to done and added public `DATA_LOSS` sanitization. New crypto restore public errors must stay similarly sanitized.
- Story 3.5 review found that public error messages can leak internal details if Store errors are passed through directly. If Story 3.6 adds public coverage, assert stable sentinel text and bounded `ErrorInfo` reason values.
- Story 3.5 added retry coverage for transient Backend stream-read errors and capped explicit retry attempts. Do not disturb restore retry behavior while adding encryption tests.
- Story 3.4 review found unbounded channel waits and fake Backend release cleanup defects in restore tests. New encrypted restore tests must use existing bounded wait helpers or bounded polling.
- Story 3.4 scoped encryption proof to fake Transit and explicitly did not claim Story 3.6 production encryption closure. Story 3.6 may close fixture-backed encryption-compatible restore, but still must not claim final production OpenBao proof.
- Story 3.3 review emphasized redaction proof must cover public output, not only internal helper errors. Apply that lesson to crypto-unavailable public/status/evidence surfaces.

### Latest Technical Information

- OpenBao Transit API documentation still supports the behavior this repo models: datakey generation through `/transit/datakey/:type/:name`, rewrap through `/transit/rewrap/:name` without returning plaintext, `key_version` selection for rewrap, and `min_decryption_version` for rejecting old ciphertext. Source: https://openbao.org/api-docs/secret/transit/
- OpenBao Transit docs describe key rotation followed by rewrap to upgrade existing ciphertext without revealing plaintext, matching ADR 0020 and ADR 0021. Source: https://openbao.org/docs/secrets/transit/
- GitHub repo/code searches for reusable Go OpenBao encrypted-restore implementations returned no candidate worth adopting. Use this repo's `internal/encryption` fake/OpenBao adapter and Shard restore fixtures instead of adding a new dependency.
- No package-registry search is required for this story because no new runtime or test dependency should be added; the repo already has deterministic fake Transit, OpenBao adapter tests, and Backend restore fixtures.

### References

- `CONTEXT.md` - Document, Transaction, Block, Frame, Shard, Cell, Member, Backend, Confirmed Upload Catalog, and Local Block Lifecycle glossary.
- `_bmad-output/planning-artifacts/epics.md` - Epic 3 and Story 3.6 acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-8 and DG-3.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - encrypted Backend opacity, Transit/envelope boundaries, and evidence requirements.
- `_bmad-output/project-context.md` - package boundaries, testing rules, telemetry/redaction rules, and commit rules.
- `_bmad-output/implementation-artifacts/3-4-restore-first-cold-read-path.md` - previous restore-first story and encrypted restore seed.
- `_bmad-output/implementation-artifacts/3-5-restore-failure-and-corruption-semantics.md` - previous failure-taxonomy story and public-error redaction fixes.
- `_bmad-output/implementation-artifacts/epic-3-restore-first-cold-read-evidence.md` - current restore-first evidence and Story 3.6 scoped concerns.
- `_bmad-output/implementation-artifacts/epic-3-restore-failure-evidence.md` - current restore failure evidence and redaction scope.
- `docs/adr/0020-openbao-envelope-encryption-contract.md` - encrypted Backend-resident Block contract.
- `docs/adr/0021-durable-rewrap-raft-command.md` - durable envelope rewrap through Raft and upload generation.
- `docs/adr/0027-phase-5-restore-first-cold-reads.md` - restore-first cold-read decision and encryption interaction.
- `internal/shard/restore.go`
- `internal/shard/restore_test.go`
- `internal/shard/encryption_test.go`
- `internal/shard/rewrap.go`
- `internal/encryption/fake.go`
- `internal/encryption/envelope.go`
- `internal/encryption/openbao.go`
- `internal/server/server.go`
- `internal/scrapctl/evidencebundle/bundle.go`

## Dev Agent Record

### Agent Model Used

GPT-5 Codex.

### Debug Log References

- CREATE-STORY: Resumed from clean `v2...origin/v2` after Story 3.5 code-review fixes were committed and pushed as `d5e36e12ec1e7065db9a0b45fce0d696d89cf7b6`.
- CREATE-STORY: Loaded BMAD create-story workflow, customization block, `CONTEXT.md`, `_bmad-output/project-context.md`, sprint status, Epic 3, Story 3.6 ACs, FR-8, architecture/readiness warnings, ADR 0020, ADR 0021, ADR 0027, Story 3.5, current restore/encryption/rewrap code, and recent git history.
- CREATE-STORY: Exa search checked current OpenBao Transit docs for datakey, rewrap, key version, and minimum decryption version behavior.
- CREATE-STORY: GitHub repo/code searches found no reusable external implementation candidate; Story 3.6 should reuse local fake Transit, OpenBao adapter tests, and Shard restore fixtures.
- CREATE-STORY: Current baseline commit is `d5e36e12ec1e7065db9a0b45fce0d696d89cf7b6`.

### Completion Notes List

- Ultimate context engine analysis completed - comprehensive developer guide created.
- Scoped Story 3.6 to fixture-backed encryption-compatible restore evidence, crypto-unavailable fail-closed behavior after restore, rewrapped envelope restore, wrong-key/minimum-version failure, and redaction proof.
- Preserved non-goals for direct Backend streaming, production OpenBao proof, real S3/IAM rehearsal, Story 3.7 final closure, and V2 release readiness.
- Identified existing implementation to reuse: `internal/shard/restore_test.go`, `internal/shard/encryption_test.go`, `internal/encryption/fake.go`, `internal/encryption/openbao.go`, `internal/shard/rewrap.go`, and `internal/scrapctl/evidencebundle`.

### File List

- `_bmad-output/implementation-artifacts/3-6-encryption-compatible-restore-evidence.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

## Change Log

- 2026-06-11: Created Story 3.6 Encryption-Compatible Restore Evidence context and moved status to ready-for-dev.
