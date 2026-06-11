---
baseline_commit: d970de3d0bbec6b6ec260d94e3722774bc3995e4
---

# Story 1.1: Durable Document Write ACK

Status: done

## Story

As a billing service engineer,
I want Document writes ACKed only after required durability and visibility,
so that upstream billing workflows can trust accepted writes.

## Traceability

- Epic: Epic 1 - Billing ETL Can Trust Immutable Document Writes and Reads.
- Requirements: FR-2.
- Acceptance IDs: AC-1.1.1, AC-1.1.2, AC-1.1.3, AC-1.1.4.
- Governing sources: `_bmad-output/planning-artifacts/epics.md`, `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md`, `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md`, `CONTEXT.md`, ADR 0001, ADR 0013, ADR 0020, ADR 0026.
- GitHub issue: not assigned in the current epic artifact. Before implementation PR, link either a tracker issue or this BMAD story artifact.

## Acceptance Criteria

1. **AC-1.1.1 - Successful ACK ordering.** Given a valid Document write, when the write completes, then ACK is returned only after required local durability, peer durability, committed metadata, and visibility are satisfied. Evidence identifies AC-1.1.1, the verification command, result, changed-boundary list, and redaction proof.
2. **AC-1.1.2 - Durability failure before commit.** Given local or peer durability fails before commit, when the write is attempted, then no ACK is returned and no partial success is exposed. Evidence proves no Document bytes or raw identifiers appear in logs, metrics, traces, or artifacts.
3. **AC-1.1.3 - Routing-boundary coverage.** Given tests exercise the write ACK path through the routing boundary, when a single-Shard fixture is used, then the fixture still routes through the Store-compatible boundary rather than hardcoding Shard ID assumptions. Evidence documents the changed boundary and future multi-Shard compatibility claim.
4. **AC-1.1.4 - Crash and retry determinism.** Given the process crashes after durable local/peer write work but before the client observes ACK, when the client retries the same write, then recovery and replay do not create divergent Document state. Evidence proves retry behavior remains idempotent or fails with a deterministic typed outcome.

## Tasks / Subtasks

- [x] Add characterization tests before changing production code. (AC: 1-4)
  - [x] Cover that `internal/server` only calls `SendAndClose` after `store.Store.WriteDocument` returns success, and maps store errors without a success response.
  - [x] Cover the existing `internal/shard` successful write stage order: openlog prepare, local block append/fsync, peer replication when peers exist, Raft propose, Raft apply/projection visibility, openlog cleanup, then returned result.
  - [x] Cover that a visible Document can be read or headed only after `WriteDocument` returns success.
- [x] Prove successful ACK durability and visibility. (AC: 1)
  - [x] Use existing shard fixtures and/or a focused `DocumentReplicator` fake to prove peer replication is required before Raft propose/apply for multi-peer configurations.
  - [x] Assert the returned `store.WriteResult` matches the committed projection metadata: SHA-256, size, and created_at.
  - [x] Preserve bounded streaming behavior. Do not buffer plaintext Documents in server tests except through existing test helpers; production code must keep streaming through `io.Reader`/`io.Pipe`.
- [x] Prove fail-closed local and peer failure behavior. (AC: 2)
  - [x] Local append/fsync or encryption failure before commit returns an error, does not ACK, and does not make the Document visible.
  - [x] Peer replication failure before quorum returns a typed non-success error, does not ACK, and does not make the Document visible.
  - [x] Failure evidence and logs/spans/metrics do not include raw `transaction_id`, `document_name`, Document bytes, Backend keys, wrapped keys, or plaintext/ciphertext payload.
- [x] Prove routing-boundary compatibility. (AC: 3)
  - [x] Keep public gRPC tests going through `server.Register` with a `store.Store` implementation or Store-compatible router. Do not make `internal/server` import `internal/shard` or assume Shard ID `0`.
  - [x] If a small router/fake is needed for the test, keep it Store-compatible and document why it is not the full Epic 2 `internal/routing` implementation.
  - [x] Document any changed boundary in the Dev Agent Record, including "no changed boundary" if the implementation is test-only.
- [x] Prove crash/retry determinism without taking over Story 1.2. (AC: 4)
  - [x] Cover completed commit but missing/leftover `.prep` on reopen: recovery deletes completed prep and retry produces either exact idempotent success if already supported, or a deterministic typed duplicate outcome.
  - [x] Cover prepared/local bytes without committed Raft metadata on reopen: recovery truncates/discards partial state, deletes `.prep`, and retry produces one visible Document or a deterministic typed failure.
  - [x] Do not implement broad exact-replay/conflicting-payload semantics here unless the minimal fix is required to satisfy AC-1.1.4; Story 1.2 owns the full idempotency and conflict matrix.
- [x] Record acceptance evidence. (AC: 1-4)
  - [x] Add debug log entries with exact commands and PASS/FAIL result.
  - [x] Include changed-boundary list, redaction proof, and crash/retry outcome in Completion Notes.
  - [x] Run focused gates at minimum: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server ./internal/shard`.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard` if tests touch Shard concurrency, Raft/apply coordination, replication fakes, or crash/recovery behavior.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries` if any package boundary, import graph, or routing seam changes.

### Review Findings

- [x] [Review][Patch] ACK metadata tests do not assert exact checksum or created_at propagation [internal/server/write_ack_test.go:52]
- [x] [Review][Patch] Shard visibility proof omits `CreatedAt` equality between `WriteResult` and Projection metadata [internal/shard/write_ack_test.go:35]
- [x] [Review][Patch] Routing-boundary coverage bypasses `server.Register` with direct handler construction [internal/server/write_ack_test.go:191]
- [x] [Review][Patch] Peer durability ordering proof checks stage starts and call count, not blocked completion/quorum acceptance before Raft propose [internal/shard/write_ack_test.go:45]
- [x] [Review][Patch] Crash/retry coverage omits the multi-peer crash window after peer durability work and before observed ACK [internal/shard/write_ack_test.go:80]
- [x] [Review][Patch] Redaction proof is asserted in notes but not backed by log/span/metric/artifact assertions [internal/server/write_ack_test.go:191]

## Dev Notes

### Current State

- `internal/server/server.go` already streams client chunks through `io.Pipe`, starts `store.WriteDocument` in a goroutine, waits for its result, maps errors, and only sends `WriteDocumentResponse` through `SendAndClose` after store success. Preserve this behavior.
- `internal/store/store.go` defines the narrow Store contract used by the public server: `WriteDocument(ctx, txID, docName, contentType, idempotencyKey, body) (WriteResult, error)`.
- `internal/shard/shard.go` currently performs leader validation, metadata validation, duplicate visibility check, openlog prepare, local block append, peer replication, Raft propose, Raft apply wait, openlog completion, and then returns `WriteResult`.
- `internal/shard/openlog_write_attempt.go` records `{transaction_id, document_name, block_id, start_offset, content_type, idempotency_key}` and builds the `CommitDocument` Raft command from the append result.
- `internal/block/writer.go` fsyncs after `AppendDocument`, `AppendDocumentFrames`, and truncation. `internal/block/index.go` fsyncs index entries and directory entries for new index files.
- `internal/shard/replication.go` fans out bytes to peers through `DocumentReplicator`, verifies peer SHA-256 responses, and requires quorum before success.
- `internal/shard/apply.go` applies committed `CommitDocument` commands to projection and resolves waiting proposals through `s.proposals`.
- Existing useful coverage:
  - `internal/server/server_test.go` covers gRPC write/read and metadata validation.
  - `internal/server/upload_pressure_test.go`, `restore_unavailable_test.go`, `not_leader_test.go`, and `telemetry_test.go` show Store-fake server tests and redaction/error mapping patterns.
  - `internal/shard/shard_test.go` covers write/head/read, duplicates, projection corruption, openlog recovery, and rebuild behavior.
  - `internal/shard/write_safety_test.go` covers openlog cleanup and write safety around block sealing.
  - `internal/shard/replication_test.go` covers replica append validation.
  - `internal/shard/write_telemetry_test.go` covers write stage telemetry naming and error recording.

### Implementation Guardrails

- Do not move Backend upload, scanner, or quarantine work into this story. Backend states 6-7 are explicitly outside the write ACK path.
- Do not put Document bytes in Raft. ADR 0001 keeps Raft metadata-only and uses peer fan-out for bytes.
- Do not introduce tenant-specific storage identity or add `tenant_id` to Document identity. Document identity remains `(transaction_id, document_name)`.
- Do not add a full multi-Shard router here unless the tests prove it is necessary. This story must be compatible with a Store-compatible routing boundary; Epic 2 owns production multi-Shard startup/routing.
- Do not add new dependencies or test frameworks. Use Go `testing`, local fakes, existing shard/server helpers, and repo-pinned dependencies.
- Preserve redaction defaults. Raw identifiers are forbidden in logs, metrics, traces, evidence, and public artifacts unless an existing local-only fail-closed override explicitly allows them.
- Preserve `log/slog` as the application logging API.
- If storage format, wire protocol, dependency/runtime choices, or cross-package ownership change, stop and add or update an ADR before implementation closure.

### Project Structure Notes

- Likely touched files:
  - `internal/server/server_test.go`
  - `internal/server/telemetry_test.go`
  - `internal/server/testutil_test.go`
  - `internal/shard/shard_test.go`
  - `internal/shard/write_safety_test.go`
  - `internal/shard/replication_test.go`
  - `internal/shard/write_telemetry_test.go`
- Possible production files only if characterization tests expose a gap:
  - `internal/server/server.go`
  - `internal/shard/shard.go`
  - `internal/shard/openlog_write_attempt.go`
  - `internal/shard/replication.go`
  - `internal/shard/apply.go`
- Avoid package names like `common`, `shared`, `util`, or `helpers`.
- Keep new test helpers close to the package that uses them unless they are already shared locally.

### Testing Notes

- Prefer focused tests that assert externally meaningful outcomes: no ACK, typed gRPC/store error, no projection visibility, no leftover prep after completed recovery, one visible Document after retry.
- For server ACK tests, a blocking or failing Store fake is usually cheaper and clearer than a full Shard fixture.
- For peer durability tests, use `openReplicatingTestShard` and a focused `DocumentReplicator` fake rather than real peer networking unless the current code path requires deployed evidence.
- For crash/retry tests, reuse the openlog and reopen patterns in `internal/shard/shard_test.go`; avoid sleeping-based assertions except for existing leader election waits.
- Redaction proof can reuse assertions from `internal/server/not_leader_test.go` and `internal/server/telemetry_test.go`.

### Technical Research Notes

- The story uses repo-pinned Go, gRPC, etcd/raft, Pebble, and OpenTelemetry dependencies; no dependency upgrade or registry search is in scope.
- Official Go docs confirm `os.File.Sync` commits file contents to stable storage; keep using the repo's existing fsync abstractions and tests rather than adding a new durability library.
- Official etcd/raft docs describe `Propose` as appending data to the Raft log; this story must still wait for the repo's apply/projection signal before ACK, not only a successful propose call.
- Official gRPC status docs confirm service handlers return status errors with optional details; keep server error mapping in `internal/server` and storage errors in store/shard packages.

## References

- `_bmad-output/planning-artifacts/epics.md` - Epic 1 and Story 1.1 source acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - Vision, UJ-1, FR-2, NFR-2, NFR-4, NFR-7.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - boundary map and routing guidance.
- `CONTEXT.md` - glossary, write state machine, Phase 2 write path, Openlog lifecycle, and safety invariants.
- `docs/adr/0001-bytes-separate-from-raft.md` - bytes stay outside Raft.
- `docs/adr/0013-trace-context-in-raft-log.md` - write/apply trace evidence and identifier privacy.
- `docs/adr/0020-openbao-envelope-encryption-contract.md` - write ACK requires successful encryption; Backend is not in ACK path.
- `docs/adr/0026-multi-shard-v2-release-boundary.md` - Store-compatible routing boundary and no hardcoded Shard IDs in public handlers.
- `docs/go-style-guide.md` - Go style and testing conventions.
- `https://pkg.go.dev/os#File.Sync` - official Go `File.Sync` reference.
- `https://pkg.go.dev/go.etcd.io/raft/v3#Node` - official etcd/raft `Node` and `Propose` reference.
- `https://pkg.go.dev/google.golang.org/grpc/status` - official gRPC status error reference.

## Dev Agent Record

### Agent Model Used

GPT-5 Codex

### Debug Log References

- 2026-06-10: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server` - PASS after adding server ACK-gating characterization tests.
- 2026-06-10: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server ./internal/shard` - initial FAIL on an unused test import, then FAIL on missing-Transaction vs missing-Document assertion wording, then PASS after test fixes.
- 2026-06-10: `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard` - PASS.
- 2026-06-10: `env GOCACHE=/tmp/scrap-v2-go-build make check` - initial FAIL on lint (`fmt.Errorf` without formatting in test fake), then PASS after replacing it with `errors.New`. This gate included static checks, package-boundaries, proto checks, lint, `go test ./...`, `go test -race ./...`, integration tests, and command builds.
- 2026-06-10: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server ./internal/shard` - review-patch run initially FAILed on Shard `CreatedAt` precision mismatch, then PASS after normalizing the write timestamp to persisted metadata precision.
- 2026-06-10: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestWriteDocumentAckAfterPeerReplicationRaftApplyAndVisibility|TestWriteDocumentPeerDurabilityFailureDoesNotCommitOrAck|TestWriteDocumentPeerDurabilityQuorumAllowsOnePeerFailure|TestOpenlogRecoveryCompletedPrepRetryHasDeterministicTypedOutcome|TestOpenlogRecoveryPartialLocalWriteAllowsSingleRetryVisibility|TestOpenlogRecoveryMultiPeerPartialReplicaWriteAllowsSingleRetryVisibility|TestEncryptedShardWriteFailsClosedWhenTransitUnavailable' -count=1` - PASS.
- 2026-06-10: `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard` - PASS after review patches.
- 2026-06-10: `env GOCACHE=/tmp/scrap-v2-go-build make check` - review-patch run initially FAILed on lint (`cyclop`, deprecated OpenTelemetry value rendering, and `unparam`), then PASS after extracting replicated append validation, replacing deprecated rendering, and simplifying the read helper. This gate included static checks, package-boundaries, proto checks, lint, `go test ./...`, `go test -race ./...`, integration tests, and command builds.

### Completion Notes List

- Added server ACK-gating tests through the registered gRPC boundary proving `WriteDocument` does not call `SendAndClose` until the Store-compatible boundary returns success, and does not send a success response when the Store returns an error.
- Added exact ACK metadata assertions for SHA-256, size, and `CreatedAt` propagation at the server boundary and between Shard `WriteResult` and committed projection metadata.
- Added shard ACK-contract tests proving peer replication is blocked until peer durability returns, Raft propose/apply do not start before quorum acceptance, ACKed writes are visible via `HeadDocument`, and peer durability failure below quorum returns no ACK and no visible Document.
- Added quorum-boundary coverage proving a three-Shard write can ACK with one peer failure after the required peer durability quorum succeeds.
- Extended the existing encrypted-write outage test to prove pre-commit encryption failure leaves no visible Document.
- Added openlog recovery retry tests for completed-commit leftover prep, partial local bytes without committed metadata, and multi-peer partial replica bytes after peer durability work but before observed ACK. Completed commits retry with deterministic `ErrAlreadyExists`; partial local or replicated bytes are truncated on recovery and retry produces one visible Document.
- Changed-boundary list: server tests now exercise `server.Register`; `internal/shard` now records Openlog prep for replicated follower appends and cleans committed follower prep after projection apply; no package/import boundary changed.
- Routing-boundary proof: the server tests use the existing `store.Store` boundary through `server.Register`; `internal/server` still does not import `internal/shard` or assume Shard ID `0`.
- Redaction proof: JSON logs, OpenTelemetry spans, and metric attributes were asserted not to contain raw `transaction_id`, `document_name`, Document bytes, Backend keys, wrapped keys, or payload values.

### File List

- `_bmad-output/implementation-artifacts/1-1-durable-document-write-ack.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `internal/shard/encryption_test.go`
- `internal/shard/openlog.go`
- `internal/shard/projection.go`
- `internal/shard/replication.go`
- `internal/shard/shard.go`
- `internal/shard/shard_test.go`
- `internal/shard/write_ack_test.go`
- `internal/server/write_ack_test.go`

### Change Log

- 2026-06-10: Implemented Story 1.1 Durable Document Write ACK as test hardening around existing server/shard write ACK behavior; verified focused, race, and full local gates.
- 2026-06-10: Applied all code-review patch findings for ACK metadata, routing-boundary coverage, peer durability ordering, multi-peer crash/retry recovery, and redaction evidence; verified focused, race, and full local gates.
