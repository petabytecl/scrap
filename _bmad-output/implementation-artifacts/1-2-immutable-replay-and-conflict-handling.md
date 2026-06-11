---
baseline_commit: d970de3d0bbec6b6ec260d94e3722774bc3995e4
created_at: 2026-06-11T00:42:18-04:00
---

# Story 1.2: Immutable Replay and Conflict Handling

Status: done

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a billing service engineer,
I want duplicate writes to distinguish exact replay from conflicting payloads,
so that immutable Document identity is preserved.

## Traceability

- Epic: Epic 1 - Billing ETL Can Trust Immutable Document Writes and Reads.
- Requirements: FR-1.
- Acceptance IDs: AC-1.2.1, AC-1.2.2, AC-1.2.3, AC-1.2.4.
- Governing sources: `_bmad-output/planning-artifacts/epics.md`, `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md`, `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md`, `CONTEXT.md`, ADR 0001, ADR 0014, ADR 0026.
- GitHub issue: not assigned in the current epic artifact. Before implementation PR, link either a tracker issue or this BMAD story artifact.

## Acceptance Criteria

1. **AC-1.2.1 - Exact replay succeeds idempotently.** Given an exact replay for `(transaction_id, document_name)`, when the write is submitted, then the response is idempotent and does not create a second Document. Evidence includes the verification command and a persisted-state check proving one visible Document and unchanged original metadata.
2. **AC-1.2.2 - Conflicting duplicate fails without mutation.** Given a conflicting payload or metadata for the same identity, when the write is submitted, then S.C.R.A.P. rejects it with a typed failure. Evidence proves no overwrite, mutation, duplicate visible Document, or additional visible metadata is created.
3. **AC-1.2.3 - Replay/conflict evidence is redacted.** Given logs, metrics, traces, or test artifacts are emitted, when replay and conflict paths run, then raw identifiers and Document bytes are redacted. Evidence records the leak-scan command and result.
4. **AC-1.2.4 - Corrupt committed state fails closed.** Given replay observes a partial or corrupt committed-log/Projection entry for the same Document identity, when duplicate or conflicting write handling runs, then the response is deterministic and fails closed instead of inventing a second visible Document. Evidence records the corrupt/replay fixture and expected typed failure.

## Tasks / Subtasks

- [x] Add red characterization tests before changing production code. (AC: 1-4)
  - [x] Cover exact replay through `internal/shard.WriteDocument`: first write succeeds; second write with same identity, content type, and bytes returns the original SHA-256, size, and `CreatedAt`; `FindDocuments` returns one Document.
  - [x] Cover conflicting duplicate payload and conflicting duplicate content type through `internal/shard.WriteDocument`: the second write returns a typed failure and `HeadDocument`/`ReadDocument` still expose the first Document only.
  - [x] Cover public gRPC behavior through `server.Register`: exact replay returns a successful `WriteDocumentResponse`; conflicting duplicate maps to `codes.AlreadyExists`; neither test bypasses the `store.Store` boundary.
  - [x] Cover corrupt duplicate state using existing Projection/Block-index corruption fixtures: duplicate handling returns `store.ErrDataLoss`/`codes.DataLoss` and does not append a second visible Document.
  - [x] Cover replay/conflict redaction using existing span/metric/log helpers or a focused leak scan over captured JSON logs, OTel spans, and metric attributes.
- [x] Implement bounded duplicate classification in the Shard write path. (AC: 1, 2, 4)
  - [x] Keep Document identity exactly `(transaction_id, document_name)`; `tenant_id` remains validation-only future routing input.
  - [x] When strict Projection Resolution finds an existing visible Document, consume the incoming body through `store.NewDocumentBodyReader` and a streaming hash/size counter before returning. Do not return before the body is drained, or the current server streaming path can report `write consumer stopped`.
  - [x] Define exact replay as matching committed metadata that is available today: `content_type`, plaintext SHA-256, and total byte size. Return the existing committed `WriteResult`, including the original `CreatedAt`.
  - [x] Treat mismatched payload hash, size, or content type as an immutable identity conflict. The typed error must satisfy `errors.Is(err, store.ErrAlreadyExists)` unless a new store conflict sentinel is introduced and mapped deliberately.
  - [x] If strict Projection Resolution or metadata-read lifecycle checks fail while classifying an existing identity, return `store.ErrDataLoss`; do not append bytes, write Openlog prep, replicate to peers, or propose Raft.
- [x] Preserve authoritative apply/replay determinism. (AC: 1, 2, 4)
  - [x] Keep conflict checks on apply as the authoritative guard: duplicate `CommitDocument` entries must be deterministic no-ops and must not update Projection state.
  - [x] If a duplicate committed entry exactly matches the already visible Document metadata, replay should converge without error when no live proposal is waiting.
  - [x] If a duplicate committed entry conflicts with the already visible Document metadata, replay must still converge without mutating Projection state; a live waiting proposer, if reachable, must receive a typed conflict failure rather than an idempotent success.
  - [x] Do not make Pebble Projection the source of truth. It remains a derived materialized view; use strict Projection Resolution for client-visible checks and the existing lenient path only where ADR 0014 permits recovery/replay tolerance.
- [x] Preserve transport and boundary behavior. (AC: 1-3)
  - [x] Keep `internal/server` as transport mapping only; storage replay/conflict behavior belongs behind `store.Store`.
  - [x] Do not add `internal/shard` imports to `internal/server`; server tests that need this path should use `server.Register` plus a real or fake `store.Store`.
  - [x] Do not change public protobuf messages unless an ADR explicitly approves a wire change. `WriteDocumentResponse` already has SHA-256, size, and created_at for idempotent replay success.
  - [x] If a new store error type is needed for conflict details, keep it in `internal/store`, preserve `errors.Is` behavior, and map it centrally in `internal/server.mapStoreError`.
- [x] Record acceptance evidence. (AC: 1-4)
  - [x] Add debug log entries with exact commands and PASS/FAIL result.
  - [x] Include changed-boundary list, exact replay definition, conflict failure mapping, corrupt-state fixture, and redaction proof in Completion Notes.
  - [x] Run at minimum: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server ./internal/shard`.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard` because the story touches Shard write/replay state.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries` if any package boundary, import graph, or server/store/shard boundary changes.

### Review Findings

- [x] [Review][Patch] Conflict errors leak raw Document identity through public status and span errors [internal/shard/replay_conflict.go:31]
- [x] [Review][Patch] Duplicate error paths can return before consuming the replay body [internal/shard/shard.go:363]
- [x] [Review][Patch] Corrupt duplicate metadata with multiple or uncounted entries is not failed closed [internal/shard/projection.go:121]

## Dev Notes

### Current State

- Story 1.1 is complete in this working tree, not necessarily committed. It added `internal/server/write_ack_test.go`, `internal/shard/write_ack_test.go`, and production changes in `internal/shard/openlog.go`, `projection.go`, `replication.go`, and `shard.go`. Do not revert those files while implementing Story 1.2.
- `internal/server/server.go` streams client chunks into an `io.Pipe`, runs `store.WriteDocument` in a goroutine, and treats an early Store return before all chunks are consumed as a stream/consumer stop. Exact replay/conflict classification must therefore drain the body before returning from `Shard.WriteDocument`.
- `internal/store.Store` returns `WriteResult{SHA256, Size, CreatedAt}`. This is sufficient for exact replay success without changing `WriteDocumentResponse`.
- `internal/store.ErrAlreadyExists` currently maps to gRPC `codes.AlreadyExists`. Existing duplicate tests expect `ErrAlreadyExists`; Story 1.2 should update those expectations so exact replay is success and conflicting duplicates are the typed failure.
- `internal/shard.WriteDocument` currently performs a strict duplicate visibility check before Openlog prep. If the Document exists, it returns `ErrAlreadyExists` without comparing body or metadata.
- `internal/shard.applyCommitDocument` performs apply-side duplicate detection by resolving the existing Document from Projection and Block `.idx` data. It currently returns `ErrAlreadyExists` for an existing counted target and does not distinguish exact replay from conflict.
- `internal/index.Resolver` owns Projection Resolution. Strict methods fail closed for visible corruption; `ContainsDocumentLenient` is reserved for recovery/replay paths where ADR 0014 allows tolerance of projection-ahead-of-`.idx` crash windows.
- `internal/shard/openlog.go` recovery already truncates partial local or replicated bytes when no committed Document is visible. Story 1.2 should not rework Openlog beyond tests needed to prove replay/conflict behavior.

### Exact Replay Contract

- Exact replay is intentionally narrow for this story: same `(transaction_id, document_name)`, same content type, same plaintext SHA-256, and same total byte size.
- `idempotency_key` is accepted and validated by the public API and carried in `CommitDocument`, but it is not part of storage identity and is not present in current Block `.idx` metadata. Do not make it storage identity, require it for replay success, or add it to storage format without ADR coverage.
- Exact replay returns success with the original committed metadata. It must not append new visible metadata, increment `DocCount`, add another Block ID to the Transaction, or expose a second Document through `FindDocuments`.
- Conflict means same identity with different content type, SHA-256, or size. Conflict must fail with a typed immutable-identity failure and preserve the first committed Document.
- Corrupt committed state means the system cannot safely prove exact replay or conflict. Fail closed with `ErrDataLoss`; do not "repair" by accepting a second Document.

### Implementation Guardrails

- Do not put Document bytes in Raft. ADR 0001 keeps Raft metadata-only; duplicate classification may hash the incoming stream but must not serialize body bytes into commands, logs, metrics, traces, or evidence.
- Do not add a full multi-Shard router here. Story 2.3 owns public API routing by Transaction, and Story 2.1/2.2 own routing and Cell startup boundaries.
- Do not move Backend upload, restore, eviction, scanner, quarantine, OpenBao, or rewrap behavior into this story.
- Do not use Backend inventory, local file existence alone, or Upload Outbox state as a duplicate authority. The visible Document check comes from strict Projection Resolution plus committed metadata.
- Preserve bounded memory. Hash and count incoming replay bodies via streaming; do not buffer full plaintext Documents in production. Tests may use small byte slices.
- Preserve `log/slog` as the application logging API and existing OTel identity hashing defaults.
- If storage format, wire protocol, dependency/runtime choices, security/encryption contracts, or cross-package ownership change, stop and add or update an ADR before implementation closure.

### Project Structure Notes

- Likely touched production files:
  - `internal/shard/shard.go` - preflight duplicate classification and replay return path.
  - `internal/shard/projection.go` - duplicate/apply helper extraction or exact/conflict comparison near Projection Resolution.
  - `internal/store/errors.go` - only if a new typed conflict error is needed; preserve `errors.Is` matching.
  - `internal/server/server.go` - only if central error mapping must distinguish a new store error type.
- Likely touched tests:
  - `internal/shard/shard_test.go` or a new focused `internal/shard/replay_conflict_test.go`.
  - `internal/shard/write_ack_test.go` if existing retry expectations need exact replay updates.
  - `internal/server/metadata_test.go` or a new focused server replay/conflict test through `server.Register`.
  - `internal/server/write_ack_test.go` only if redaction helpers are reused there.
- Avoid new packages. If helper extraction is useful, keep it close to Shard or Store where the behavior belongs.

### Testing Notes

- Prefer focused tests with externally meaningful outcomes:
  - exact replay returns success and original metadata;
  - conflict returns typed failure;
  - read/head/find still expose only the first Document;
  - corrupt Projection/Block-index duplicate state returns `ErrDataLoss`;
  - no raw identifiers or Document bytes appear in captured evidence.
- Update old duplicate tests rather than leaving them asserting the obsolete "all duplicates are `ErrAlreadyExists`" behavior.
- For corrupt-state tests, reuse `CorruptProjectionForTest`, Block `.idx` corruption patterns, and existing `openTestShard`/`openWriteAckCluster` helpers.
- For server tests, use `server.Register` with a Store-compatible boundary. A fake Store is acceptable for transport mapping, but at least one gRPC replay/conflict test should exercise the real Shard path unless it is too expensive; record the rationale if using a fake only.
- Use `context.Context` with bounded timeouts for any goroutine/stream tests. Do not add sleeps as synchronization.

### Previous Story Intelligence

- Story 1.1 review found that tests must assert exact metadata, not just success. Carry that forward: replay success must assert SHA-256, size, and original `CreatedAt`.
- Story 1.1 review also found server tests bypassed `server.Register`; Story 1.2 server tests must not repeat that mistake.
- Story 1.1 added a multi-peer partial-replica crash/retry test and follower Openlog prep cleanup. Story 1.2 can rely on that Openlog behavior and should focus on duplicate identity semantics after committed visibility.
- Story 1.1 redaction proof used JSON logs, OTel spans, and metric attributes. Reuse that evidence pattern instead of relying on notes alone.

### Git Intelligence

- Recent commits show security and Shard hardening patterns: `d970de3 fix(security): enforce peer Shard scope`, `4013b66 fix(security): harden public API and deploy controls`, `69ad47f feat(shard): coordinate upload pressure pause ownership`, `954bfda feat(shard): harden upload confirmation replay`, and `e0c72ce feat(shard): add upload outbox event boundary`.
- The relevant pattern is narrow, test-backed Shard behavior changes with central Store/server error mapping and no direct server ownership of storage state.

### Technical Research Notes

- Repo-pinned versions remain the authority: Go `1.26.4`, gRPC `v1.81.1`, etcd/raft `v3.6.0`, Pebble `v1.1.5`, OpenTelemetry `v1.44.0`. No dependency upgrade or registry search is in scope.
- Official Go `errors` docs confirm `errors.Is`/`errors.As` inspect wrapped error trees. Keep store errors compatible with sentinel checks. Source: https://pkg.go.dev/errors
- Official gRPC status docs define `ALREADY_EXISTS` for create attempts where the entity already exists. Conflicting immutable duplicate writes should continue to map to `codes.AlreadyExists` unless an ADR changes public error semantics. Source: https://grpc.io/docs/guides/status-codes/
- Official etcd/raft docs for `Node.Propose` state that proposed data appears in committed normal entries only if committed; there is no guarantee a proposal commits. Story 1.2 must keep waiting on the repo apply/projection signal before treating new writes as visible. Source: https://pkg.go.dev/go.etcd.io/raft/v3@v3.6.0

### References

- `_bmad-output/planning-artifacts/epics.md` - Epic 1 and Story 1.2 source acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-1, FR-2, FR-3, FR-4, NFR-2, NFR-3, NFR-4, NFR-7.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - source of truth, package boundaries, authority, error/redaction/evidence patterns.
- `_bmad-output/implementation-artifacts/1-1-durable-document-write-ack.md` - previous story learnings and review fixes.
- `CONTEXT.md` - glossary, write state machine, API contract, Phase 2 write path, apply-side conflict detection, Projection model, Openlog lifecycle.
- `docs/adr/0001-bytes-separate-from-raft.md` - bytes stay outside Raft.
- `docs/adr/0014-projection-resolution-boundary.md` - strict vs lenient Projection Resolution semantics.
- `docs/adr/0026-multi-shard-v2-release-boundary.md` - Store-compatible routing boundary and no hidden Shard ID assumptions in public handlers.
- `docs/go-style-guide.md` - Go style, errors, tests, and package conventions.
- `https://pkg.go.dev/errors` - official Go wrapped-error matching reference.
- `https://grpc.io/docs/guides/status-codes/` - official gRPC status code semantics.
- `https://pkg.go.dev/go.etcd.io/raft/v3@v3.6.0` - official etcd/raft proposal semantics for the repo-pinned module.

## Dev Agent Record

### Agent Model Used

GPT-5 Codex

### Debug Log References

- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard ./internal/server -run 'TestWriteDocumentExactReplayReturnsOriginalMetadata|TestWriteDocumentConflictingReplayFailsWithoutMutation|TestWriteDocumentDuplicateWithCorruptCommittedStateFailsClosed|TestApplyCommitDocumentExactDuplicateNoops|TestApplyCommitDocumentConflictingDuplicateNotifiesProposal|TestGRPCWriteDocumentExactReplayThroughRegisteredShard|TestGRPCWriteDocumentConflictReturnsAlreadyExistsThroughRegisteredShard|TestGRPCWriteDocumentReplayConflictEvidenceRedactsRawValues' -count=1` - FAIL before implementation: exact replay returned `document already exists` / gRPC `AlreadyExists`.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard ./internal/server -run 'TestWriteDocumentExactReplayReturnsOriginalMetadata|TestWriteDocumentConflictingReplayFailsWithoutMutation|TestWriteDocumentDuplicateWithCorruptCommittedStateFailsClosed|TestApplyCommitDocumentExactDuplicateNoops|TestApplyCommitDocumentConflictingDuplicateNotifiesProposal|TestGRPCWriteDocumentExactReplayThroughRegisteredShard|TestGRPCWriteDocumentConflictReturnsAlreadyExistsThroughRegisteredShard|TestGRPCWriteDocumentReplayConflictEvidenceRedactsRawValues' -count=1` - PASS: `internal/shard` and `internal/server` focused Story 1.2 coverage passed.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server ./internal/shard` - FAIL during update: older duplicate tests still expected every duplicate to return `ErrAlreadyExists`; updated them to exact-replay success and conflict-only typed failure.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server ./internal/shard` - PASS: `internal/server` and `internal/shard` package tests passed.
- `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard` - PASS: `internal/shard` race test passed.
- `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries` - PASS.
- `env GOCACHE=/tmp/scrap-v2-go-build make check` - FAIL once on lint after implementation: new conflict test exceeded `gocognit`, strict duplicate helper was unused, and the shared gRPC helper had an unvaried content-type parameter.
- `env GOCACHE=/tmp/scrap-v2-go-build make check` - PASS: lint, package boundaries, buf lint/generate diff check, `go test ./...`, `go test -race ./...`, integration tests, and command builds passed.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard ./internal/server -run 'TestWriteDocumentDuplicateWithCorruptCommittedStateFailsClosed|TestApplyCommitDocumentFailsClosedOnUncountedDuplicateInDifferentBlock|TestDuplicateDocumentEntryFailsClosedOnMultipleVisibleMatches|TestApplyCommitDocumentNoopsExistingVisibleDocumentReplay|TestApplyCommitDocumentRejectsConflictingVisibleDocument|TestGRPCWriteDocumentReplayConflictEvidenceRedactsRawValues|TestGRPCWriteDocumentConflictReturnsAlreadyExistsThroughRegisteredShard' -count=1` - PASS: focused review patch coverage for redacted conflict evidence, duplicate body drain on error, and corrupt duplicate metadata fail-closed handling.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server ./internal/shard` - PASS after review fixes: `internal/server` and `internal/shard` package tests passed.
- `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard` - PASS after review fixes: `internal/shard` race test passed.
- `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries` - PASS after review fixes.
- `env GOCACHE=/tmp/scrap-v2-go-build make check` - FAIL once after review fixes on lint complexity in `applyCommitDocument` and `inspectCommitProjectionEntry`; extracted existing-commit and block-scan helpers.
- `env GOCACHE=/tmp/scrap-v2-go-build make check` - PASS after review fixes: lint, package boundaries, buf lint/generate diff check, `go test ./...`, `go test -race ./...`, integration tests, and command builds passed.

### Completion Notes List

- Exact replay is implemented in Shard write classification as same `(transaction_id, document_name)`, same content type, same plaintext SHA-256, and same total byte size. Duplicate bodies are fully streamed through hash/size accounting before returning the original committed `WriteResult`, including `CreatedAt`.
- Conflicting duplicate payload, size, or content type now fails with `store.ErrAlreadyExists`, which continues to map centrally to gRPC `codes.AlreadyExists`; public protobuf messages and store/server boundary contracts were not changed.
- Corrupt duplicate committed state uses strict Projection Resolution and metadata lifecycle checks, so duplicate classification fails closed with `store.ErrDataLoss` before Openlog prep, peer replication, or Raft proposal.
- Apply-side replay remains authoritative: exact duplicate `CommitDocument` entries no-op and clean committed Openlog prep, while conflicting duplicate applies preserve Projection state and notify live proposers with the typed duplicate error.
- Server coverage exercises `server.Register` with a real Shard-backed `store.Store`, and redaction proof covers captured JSON logs, OTel spans, and metric data without raw Transaction IDs, Document names, or Document bytes.
- No dependency, ADR, storage format, Raft body, or protobuf changes were introduced.
- Review fixes redacted immutable-conflict errors before public gRPC status and span status/event recording, so raw Transaction IDs, Document names, and body bytes are not exposed on conflict paths.
- Review fixes drain replay bodies before returning duplicate-classification errors from corrupt Projection or metadata-read lifecycle paths, preserving the server streaming contract even on fail-closed outcomes.
- Review fixes fail closed on corrupt duplicate metadata with multiple visible matches or uncounted duplicates from a different Block, while preserving same-Block index-append crash repair for matching committed metadata.

### File List

- `_bmad-output/implementation-artifacts/1-2-immutable-replay-and-conflict-handling.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `internal/server/replay_conflict_test.go`
- `internal/server/write_ack_test.go`
- `internal/shard/projection.go`
- `internal/shard/projection_test.go`
- `internal/shard/replay_conflict.go`
- `internal/shard/replay_conflict_internal_test.go`
- `internal/shard/replay_conflict_test.go`
- `internal/shard/shard.go`
- `internal/shard/write_ack_test.go`

### Change Log

- 2026-06-11: Created Story 1.2 Immutable Replay and Conflict Handling context package and marked it ready for development.
- 2026-06-11: Implemented immutable exact-replay success, conflicting duplicate failure, corrupt-state fail-closed handling, apply-side replay determinism, and Story 1.2 evidence coverage; moved story to review.
- 2026-06-11: Applied code review patches for redacted conflict errors, duplicate body-drain error paths, and corrupt duplicate metadata fail-closed handling; moved story to done.
