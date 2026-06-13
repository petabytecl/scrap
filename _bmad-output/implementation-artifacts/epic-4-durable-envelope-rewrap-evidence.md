---
story: 4.4-durable-envelope-rewrap-workflow
status: done
created: 2026-06-12T02:26:58-04:00
story_context_baseline: ceb447dd663e40a0f22c83f31ed2d7aa0c6c2153
implementation_start_commit: 9c541f1dc6f767ce549af598b77c521a691e6538
owner: Coto
---

# Epic 4 Durable Envelope Rewrap Evidence

## Scope

Story 4.4 closes the local durable envelope rewrap workflow for FR-10. It proves:

- authorized rewrap changes Document envelope metadata through committed Raft state;
- retries, stale apply, already-applied replay, proposal IDs, and failure paths are idempotent and bounded;
- encrypted reads still verify plaintext after rewrap while Block payload bytes stay unchanged; and
- admin health, audit, error responses, and evidence stay redacted for key material and sensitive operational details.

Story 4.4 does not claim OpenBao bootstrap UX, real production outage rehearsal, metadata encryption, transparent migration for old unencrypted Blocks, direct Backend ciphertext streaming, or final production security evidence-bundle closure. Story 4.7 owns prod-like real mTLS/OpenBao rehearsal and final security evidence aggregation.

## Files Reviewed

- `CONTEXT.md`
- `_bmad-output/project-context.md`
- `_bmad-output/planning-artifacts/epics.md`
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md`
- `_bmad-output/planning-artifacts/architecture.md`
- `docs/adr/0020-openbao-envelope-encryption-contract.md`
- `docs/adr/0021-durable-rewrap-raft-command.md`
- `docs/phase-4.5-security-implementation-slices.md`
- `_bmad-output/implementation-artifacts/4-3-openbao-backed-encrypted-write-and-read.md`
- `_bmad-output/implementation-artifacts/epic-4-openbao-encrypted-write-read-evidence.md`
- `_bmad-output/implementation-artifacts/4-4-durable-envelope-rewrap-workflow.md`
- `_bmad-output/implementation-artifacts/epic-4-durable-envelope-rewrap-evidence.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `proto/scrap/v1/raft.proto`
- `internal/rewrap/types.go`
- `internal/encryption/transit.go`
- `internal/encryption/fake.go`
- `internal/encryption/openbao.go`
- `internal/encryption/fake_test.go`
- `internal/encryption/openbao_test.go`
- `internal/shard/rewrap.go`
- `internal/shard/rewrap_apply_test.go`
- `internal/shard/rewrap_internal_test.go`
- `internal/shard/encryption_test.go`
- `internal/shard/restore_test.go`
- `internal/shard/upload.go`
- `internal/shard/upload_apply_test.go`
- `internal/shard/upload_outbox_boundary_test.go`
- `internal/shard/upload_controller.go`
- `internal/shard/block_upload_lifecycle.go`
- `internal/shard/projection_rebuilder_test.go`
- `internal/admin/rewrap.go`
- `internal/admin/server.go`
- `internal/admin/server_test.go`
- `internal/admin/authorization_test.go`
- `internal/admin/audit_ratelimit_test.go`
- `internal/cmd/app.go`
- `internal/store/proto_raft_contract_test.go`
- `test/integration/openbao_transit_test.go`
- `test/integration/testinfra/openbao/openbao.go`

## Final Coverage Matrix

| AC | Status | Current proof | Remaining evidence needed |
| --- | --- | --- | --- |
| AC-4.4.1 rewrap state is Raft authority | PASS | `Shard.RewrapDocument` builds `RewrapDocumentEnvelope`, proposes through Raft, waits for apply, and uses `proposal_id` only to notify the local waiter. `TestRaftCommandRewrapDocumentEnvelopeRoundTrip`, `TestApplyRewrapRequeuesWithEntryIndexGeneration`, `TestApplyRewrapNotifiesMatchingProposalID`, and `TestEncryptedShardRewrapConvergesAcrossMembersWithoutRewritingBlocks` prove the wire contract, committed apply, proposal isolation, and three-Member full envelope metadata convergence. | None for Story 4.4. Story 4.7 owns prod-like security evidence aggregation. |
| AC-4.4.2 retry and interruption are idempotent and redacted | PASS | `TestEncryptedShardRewrapUpdatesEnvelopeWithoutRewritingBlock` proves idempotent retry for an already-current key version. `TestProposeRewrapDocumentForgetsProposalOnContextCancel`, `TestApplyRewrapRequeuesAlreadyAppliedReplay`, `TestApplyRewrapRejectsStaleEnvelopeWithoutReplacingIndex`, `TestApplyRewrapReopensCurrentIndexAfterStaleEnvelope`, `TestApplyRewrapReopensCurrentIndexAfterAlreadyAppliedEnvelope`, and `TestEncryptedShardRewrapFailureRecordsHealthAndPreservesRead` prove interrupted proposal-wait cleanup, replay, stale command, current writer cleanup, failure health, and readable-Document preservation. Admin and OpenBao tests prove bounded errors and provider failure redaction. | None for Story 4.4. |
| AC-4.4.3 reads remain valid and Block bytes unchanged | PASS | `TestEncryptedShardRewrapUpdatesEnvelopeWithoutRewritingBlock` proves single-Shard `.blk` bytes unchanged, envelope version updated, normal read succeeds, and idempotent retry does not rewrite. `TestEncryptedShardRewrapConvergesAcrossMembersWithoutRewritingBlocks` proves all local three-Member replicas converge on byte-identical replacement envelope metadata, every Member's Block bytes remain unchanged, and plaintext reads succeed on the original leader and a distinct replacement leader. `TestReadDocumentEncryptedRestoreUsesRewrappedEnvelope` proves restored encrypted reads use the rewrapped envelope while Block bytes still omit plaintext. | None for Story 4.4. |
| AC-4.4.4 in-flight old/new metadata does not orphan envelopes | PASS | `TestApplyRewrapRejectsStaleEnvelopeWithoutReplacingIndex` proves stale old-version commands cannot downgrade newer metadata. `TestApplyRewrapRequeuesAlreadyAppliedReplay` proves replay of already-applied replacement metadata is safe and keeps replacement upload obligation. `TestApplyConfirmUploadIgnoresStaleGenerationAndKeepsPending`, `TestApplyConfirmUploadIgnoresStaleDuplicateGeneration`, `TestApplySealBlockPreservesPendingRewrapGeneration`, `TestUploadOutboxRejectsStaleUploadConfirmedGeneration`, `TestBackendKeyPrefixIncludesNonZeroUploadGeneration`, and `TestProjectionRebuilderPreservesPendingRewrapUploadOverConfirmedAuthority` prove stale pre-rewrap upload confirmations and rebuild cannot clear or overwrite replacement state. | None for Story 4.4. |

## Source Evidence Notes

- `internal/shard.RewrapDocument` requires leader authority, enabled Shard encryption, an encrypted index entry, and Transit `RewrapDataKey` before proposing a `RewrapDocumentEnvelope` Raft command.
- `internal/shard.proposeRewrapDocument` includes old/new key versions, replacement envelope bytes, rewrap timestamp, and a generated proposal ID. The local caller reports success only after the apply path signals the proposal.
- `internal/shard.applyRewrapDocumentEnvelope` validates command shape, parses replacement envelope metadata, compares current envelope version to the command old/new versions, replaces only the `.idx` envelope, and treats stale already-replaced commands as no-op apply.
- `internal/shard.finishRewrapApplyLocked` requeues replacement Backend upload only for changed sealed historical Blocks. Current open Blocks need no replacement upload.
- `internal/shard.applyConfirmUpload` and `internal/shard.uploadOutbox` preserve `upload_generation`; stale confirmations are ignored and do not clear newer replacement upload obligations.
- `internal/admin.handleRewrapDocument` authorizes `admin_operator` before side effects, calls only the configured `rewrap.RewrapService`, and maps domain errors to bounded HTTP responses. The authorized operator result contract echoes requested Document identity; this is classified as allowed response identity, not log/metric/health/evidence leakage.
- `internal/admin.applyRewrapHealth` exposes bounded rewrap status, reason, key version, mount/key names, timestamp, and failure counts. Health output does not include raw Transaction IDs, Document names, wrapped-key ciphertext, plaintext, data keys, provider response bodies, or tokens.

## Current Test Evidence Scope

The implementation and review-fix diffs add two missing tests:

- `TestEncryptedShardRewrapConvergesAcrossMembersWithoutRewritingBlocks`
- `TestProposeRewrapDocumentForgetsProposalOnContextCancel`

The multi-Member test writes an encrypted Document through the existing three-Member Raft test harness, verifies replication to all Members, rotates fake Transit, rewraps through the elected leader, waits for every Member to converge on key version 2 with byte-identical replacement envelope metadata, checks every Member's `.blk` bytes are unchanged, reads plaintext from the current leader, closes that leader, waits for a replacement leader, and reads the same plaintext again through the replacement leader. Direct follower reads are intentionally not asserted because the current Shard read path enforces leader reads.

The cancellation test proposes a rewrap command with a canceled context and proves the local proposal waiter is removed, so interrupted clients do not strand local waiter state before retry/resume.

All other PASS rows are based on current reruns of existing tests, not unstated intent.

## Command Evidence

- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run TestEncryptedShardRewrapConvergesAcrossMembersWithoutRewritingBlocks -count=1 -v` - PASS after the focused test was added and the test cleanup was corrected.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'Rewrap|rewrap|UploadGeneration|ConfirmUpload|BackendKeyPrefix|RestoreUsesRewrappedEnvelope|EncryptedShardRewrap' -count=1 -v` - PASS. Covered Shard rewrap, three-Member convergence, stale apply/replay, upload generation, stale confirmation, Backend generation keys, projection rebuild, encrypted restore, and health preservation.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'Rewrap|rewrap|UploadGeneration|ConfirmUpload|BackendKeyPrefix|RestoreUsesRewrappedEnvelope|EncryptedShardRewrap|ProposeRewrapDocumentForgetsProposalOnContextCancel' -count=1 -v` - PASS after code-review fixes. Covered the tightened multi-Member envelope convergence proof and interrupted proposal-wait cleanup fixture.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/admin -run 'Rewrap|AuthorizationDeniesRewrap|AuditsRewrap|HealthEndpointReportsBoundedRewrapStatus' -count=1 -v` - PASS. Covered admin authorization, audit, health status, safe response shape, request validation, methods, and service error mapping.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/encryption -run 'Rewrap|FakeTransit|OpenBao' -count=1 -v` - PASS. Covered fake Transit rewrap/rotation/failures, OpenBao rewrap adapter mapping, provider failure classification, context preservation, readiness, and token redaction.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/store -run 'RewrapDocumentEnvelope|ConfirmUpload' -count=1 -v` - PASS. Covered protobuf/Raft command round trips for rewrap and upload generation.
- `docker info --format '{{.ServerVersion}}'` - PASS, Docker server `29.5.2`.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/encryption ./internal/block ./internal/shard ./internal/store ./internal/admin ./internal/cmd ./internal/server -count=1` - PASS. Covered affected package regression.
- `env GOCACHE=/tmp/scrap-v2-go-build go test -tags integration ./test/integration -run TestIntegrationOpenBaoTransitContainerRoundTrip -count=1 -v` - PASS. Testcontainers started `openbao/openbao:2.5.4` and completed Transit data-key, unwrap, and rewrap round trip.
- `git diff --check` - PASS.
- `env GOCACHE=/tmp/scrap-v2-go-build make check` - PASS. Covered format/generation checks, package boundaries, lint, all unit tests, race tests, integration tests, and `scrapd`/`scrapctl` builds.

## Leak Scan Evidence

Final scans covered `_bmad-output/implementation-artifacts/4-4-durable-envelope-rewrap-workflow.md`, `_bmad-output/implementation-artifacts/epic-4-durable-envelope-rewrap-evidence.md`, and the package scope `internal/encryption internal/block internal/shard internal/store internal/admin internal/cmd internal/server`.

| Scan | Status | Classification |
| --- | --- | --- |
| credential vocabulary scan | PASS | 222 broad matches. Matches are allowed test/evidence/security-policy vocabulary such as authorization roles, token redaction tests, key names, wrapped-key prose, and Story 4.4 scan patterns. No hardcoded credential values were found. |
| sensitive identifier vocabulary scan | PASS | 183 broad matches. Matches are allowed fixture names, story/evidence prose, bounded admin response tests, and privacy-policy references. No deployed log or metric path was added in this story. |
| strict shaped-value scan | PASS | 0 matches for shaped cloud keys, GitHub tokens, Slack tokens, private-key blocks, or AWS credential assignment forms. |

## Production Rehearsal Split

Story 4.4 ran local unit/package tests, local three-Member Raft harness evidence, affected package regression, and OpenBao adapter integration. It did not run `make production-rehearsal-security` or claim final production security evidence-bundle closure. Story 4.7 owns real mTLS/OpenBao production rehearsal and final aggregation evidence.

## Final Decision

PASS. Story 4.4 acceptance criteria are closed for local durable rewrap evidence. Story 4.7 remains open for prod-like real mTLS/OpenBao security rehearsal and final evidence-bundle aggregation.
