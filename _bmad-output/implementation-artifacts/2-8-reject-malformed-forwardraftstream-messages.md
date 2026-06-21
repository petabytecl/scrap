---
baseline_commit: 16895f0
---

# Story 2.8: Reject Malformed `ForwardRaftStream` Messages

Status: done

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a platform operator,
I want malformed streamed Raft messages to fail visibly,
so that peer transport bugs cannot silently drop authority messages.

## Acceptance Criteria

1. **AC-2.8.1 - Malformed stream message returns an observable error.** Given malformed protobuf bytes arrive on `ForwardRaftStream`, when the peer server handles the message, then the stream returns an observable error instead of `nil`. Evidence records the malformed-stream regression test.
2. **AC-2.8.2 - No Raft route side effect on malformed input.** Given a malformed message is received, when handling fails, then no Raft route side effect occurs. Evidence records the no-route assertion.
3. **AC-2.8.3 - Bounded, redacted audit/log output.** Given malformed input is audited, when evidence is reviewed, then audit and log output remains bounded and redacted (no raw identifiers, Document bytes, or unbounded notes). Evidence records the redaction review result.
4. **AC-2.8.4 - Consistent rejection semantics across both forward paths.** Given unary `ForwardRaft` and streaming `ForwardRaftStream` receive malformed messages, when errors are mapped, then both paths have consistent observable rejection semantics (same gRPC code). Evidence records `go test ./internal/peer/...`.

## Traceability

- Epic: Epic 2 - Operators Can Run a Shard-Aware Cell.
- Requirements: FR-4 (Raft and peer replication authority), FR-5 (multi-Shard startup and routing), NFR-3 (authority separation), NFR-8 (data-integrity blocker discipline).
- Release policy: any confirmed or plausible data-integrity defect is release-blocking until fixed or explicitly disproven with current evidence (NFR-8).
- Governing ADRs:
  - `docs/adr/0024-production-topology-and-peer-scope-policy.md` (peer Shard-scope authorization before side effects).
  - `docs/adr/0026-multi-shard-release-boundary.md` (`internal/peer` authorized Shard set source).
- Prerequisites: Stories 2.1 through 2.7 are done. Story 2.7 (bound peer `ReplicateDocument` input before side effects) established the peer "fail before side effects, no raw identifiers in errors" pattern this story mirrors.
- Non-goals: no peer wire-contract change, no generated proto edits, no new Shard authority behavior, no change to `RouteRaftMessage` route-error handling, no broad peer transport error-mapping refactor, no change to unary `ForwardRaft` behavior beyond confirming/aligning it.

## Tasks / Subtasks

- [x] Make `ForwardRaftStream` reject malformed messages with an observable error. (AC: 1, 2, 4)
  - [x] In `internal/peer/server.go`, change `handleForwardRaftStreamRequest` so that when `msg.Unmarshal(req.Message)` fails, it returns a non-nil `status.Errorf(codes.InvalidArgument, "unmarshal raft message: %v", err)` instead of `return recordAllowed, nil`.
  - [x] Keep the existing `s.recordMalformedRaftMessage(ctx, audit.OperationForwardRaftStream, req.ShardId, err)` call before returning, so the counter/log behavior is preserved.
  - [x] Confirm the returned error propagates: `ForwardRaftStream`'s loop already does `if err != nil { return err }`, so a non-nil handler error terminates the stream visibly. Do not swallow it.
  - [x] Verify the unmarshal failure path still occurs strictly before `(*router).RouteRaftMessage(...)`, so no Raft route side effect happens on malformed input (already true; preserve ordering).
  - [x] Match the unary `ForwardRaft` mapping exactly: both paths must reject malformed bytes with `codes.InvalidArgument` and the same `"unmarshal raft message: %v"` message shape.

- [x] Preserve unrelated stream behavior. (AC: 1, 2)
  - [x] Do not change `recordRaftRouteError` / `RouteRaftMessage`-failure handling; route errors after a successful unmarshal are out of scope and must keep their current (logged, non-fatal) behavior.
  - [x] Do not change authorization ordering: `authorizePeerShardScope` and `recordAllowedAudit` must still run before the unmarshal attempt.
  - [x] Keep the `recordAllowed` first-message audit bookkeeping intact for valid messages.

- [x] Add regression tests in `internal/peer`. (AC: 1, 2, 3, 4)
  - [x] Updated `internal/peer/raft_observability_test.go` (`TestForwardRaftStreamRejectsMalformedMessagesWithoutRouting`) to drive `ForwardRaftStream` with a malformed `Message` and assert the returned error is non-nil with `status.Code(err) == codes.InvalidArgument`. (This test previously encoded the buggy `io.EOF` silent-drop behavior.)
  - [x] Assert the configured `RaftRouter` test double records **zero** `RouteRaftMessage` calls for malformed input (reuses `recordingRaftRouter.calls`).
  - [x] Added `TestForwardRaftMalformedRejectionParity` proving unary `ForwardRaft` and streaming `ForwardRaftStream` reject malformed bytes with the same `codes.InvalidArgument` code and neither routes (AC-2.8.4).
  - [x] Added an assertion that the malformed-message log does not leak the raw message bytes and only carries bounded fields (`audit.surface`, `audit.operation`, `scrap.shard_id`, `malformed_raft_messages`, decode `err`) (AC-2.8.3).
  - [x] Kept existing happy-path and wrong-Shard tests passing (`TestPeerServerDenies*`, `TestPeerServerAuditsUnauthorizedStreamShardWithoutAllowedEvent`) — full peer package green.
  - [x] Reused existing test doubles: `forwardRaftStream`, `recordingRaftRouter`, `marshalRaftMessage`, and peer auth helpers.

- [x] Verification and story evidence. (AC: 1-4)
  - [x] Ran `go test ./internal/peer/... -run 'ForwardRaft' -count=1` (red before the fix, green after).
  - [x] Ran `go test ./internal/peer/... ./internal/cmd/... -count=1`.
  - [x] Ran `make package-boundaries`.
  - [x] Ran `git diff --check`.
  - [x] Recorded exact commands and results in the Debug Log below.

## Dev Notes

### Current State

- `internal/peer/server.go` owns peer gRPC ingress. Two RPCs forward Raft messages to the local Raft node through the narrow `RaftRouter` interface (`internal/peer/transport.go`: `RouteRaftMessage(ctx, shardID, msg) error`).
- **Unary `ForwardRaft`** (`internal/peer/server.go:437`) already fails closed on malformed input:
  ```go
  var msg raftpb.Message
  if err := msg.Unmarshal(req.Message); err != nil {
      s.recordMalformedRaftMessage(ctx, audit.OperationForwardRaft, req.ShardId, err)
      return nil, status.Errorf(codes.InvalidArgument, "unmarshal raft message: %v", err)
  }
  ```
- **Streaming `ForwardRaftStream`** (`internal/peer/server.go:456`) delegates each received message to `handleForwardRaftStreamRequest` (line 478). That helper currently **swallows** malformed input:
  ```go
  var msg raftpb.Message
  if err := msg.Unmarshal(req.Message); err != nil {
      s.recordMalformedRaftMessage(ctx, audit.OperationForwardRaftStream, req.ShardId, err)
      return recordAllowed, nil   // <-- BUG: returns nil, stream silently continues
  }
  ```
- This is the defect: a malformed streamed Raft message is recorded but produces **no observable error**, so a peer transport bug can silently drop authority messages. The unary and streaming paths disagree.
- `recordMalformedRaftMessage` (line 498) increments `s.malformedRaftMsgs` and logs at power-of-two counts with bounded, already-redacted fields (`audit.surface`, `audit.operation`, `scrap.shard_id`, `malformed_raft_messages`, decode `err`). The decode `err` is a protobuf wire-decode error and carries no Document identifiers.
- `ForwardRaftStream`'s receive loop (line 465) already returns any handler error to the gRPC runtime, so the only change needed is in `handleForwardRaftStreamRequest`.

### What This Story Changes

- `handleForwardRaftStreamRequest` returns `status.Errorf(codes.InvalidArgument, "unmarshal raft message: %v", err)` (non-nil) on malformed bytes instead of `nil`, terminating the stream visibly and matching unary `ForwardRaft`.
- Adds malformed-input regression tests for both forward paths plus a no-route-side-effect assertion and a redaction assertion.

### What Must Be Preserved

- `internal/peer` remains a transport boundary connected to Shard behavior only through the narrow `RaftRouter` interface; do not import `internal/shard`.
- Peer Shard-scope authorization (ADR 0024) must still run before the unmarshal attempt and before any route side effect.
- `recordMalformedRaftMessage` counter/log semantics (power-of-two log throttle, bounded redacted fields) stay unchanged.
- `RouteRaftMessage`-failure handling via `recordRaftRouteError` (post-unmarshal route errors) stays unchanged — this story only changes the malformed-decode path.
- No `proto/scrap/v1/peer.proto`, `gen/`, Block/Frame, Backend object, or Raft command shape changes.
- Keep client/peer-driven malformed input out of server `ERROR` logs; it is a `WARN` + typed gRPC status, not an `ERROR`.

### Implementation Guidance

- Minimal change: one return statement in `handleForwardRaftStreamRequest`. Resist refactoring the loop or the audit bookkeeping.
- The handler signature is `(bool, error)`. Return `(recordAllowed, status.Errorf(codes.InvalidArgument, "unmarshal raft message: %v", err))` so audit-recorded bookkeeping for the first message is preserved while the error still propagates.
- Use the same `codes.InvalidArgument` and message string as the unary path so AC-2.8.4 "consistent semantics" is literally true and easy to assert.
- Decode errors from `raftpb.Message.Unmarshal` are bounded protobuf errors (e.g. wiretype/illegal tag); they are safe to wrap with `%v`. Do not add `req.Message` bytes, Shard payloads, or raw identifiers to the error or logs.

### Project Structure Notes

Likely update:

- `internal/peer/server.go` - change `handleForwardRaftStreamRequest` malformed-decode return.
- `internal/peer/authorization_test.go` or new `internal/peer/forward_raft_malformed_test.go` - malformed-input regression tests for both paths.
- `_bmad-output/implementation-artifacts/2-8-reject-malformed-forwardraftstream-messages.md` - record implementation evidence during dev.
- `_bmad-output/implementation-artifacts/sprint-status.yaml` - story status updates during workflow.

Likely avoid:

- `proto/scrap/v1/peer.proto`, `gen/go/scrap/v1/*` - no wire change needed.
- `internal/peer/transport.go` - `RaftRouter` interface is unchanged.
- `internal/cmd/*`, `internal/shard/*` - routing/authority unchanged.

### Previous Story Intelligence

- Story 2.7 (`2-7-bound-peer-replicatedocument-input-before-side-effects.md`, done) established the exact pattern this story extends: validate/reject malformed peer input **before** side effects, return typed bounded gRPC errors, keep raw identifiers out of errors and logs, and prove zero side effects with same-package white-box tests.
- Story 2.4 established the required no-side-effect assertion style for peer failures (wrong-Shard RPCs denied before any route/sink/file side effect). Mirror that style for the no-route assertion (AC-2.8.2).
- Story 2.7 used focused `go test ./internal/peer/... ./internal/cmd/...` + `make package-boundaries` as the narrow gate, with broader gates only if shared behavior changed. This story is narrower (single return + tests), so the focused gate set is appropriate; run `make check` only if lint shape flags the change.

### Git Intelligence Summary

- `f96fd36 docs(deferred): record peer ReplicateDocument input validation gaps` and `27e0cde fix(block): fail closed on missing document digest` show the active pattern: data-integrity defects get focused red/green regression tests plus evidence updates. This story follows the same shape.
- The Story 2.7 review explicitly deferred broader peer transport error-mapping (ctx/ENOSPC → wrong codes) as pre-existing and out of scope. Do not pull that deferred work into this story; scope is the malformed-decode path only.

### Latest Technical Information

- Repo-pinned versions remain authoritative: Go `1.26.4`; gRPC and protobuf versions per `go.mod`. No dependency changes required.
- `raftpb.Message` comes from `go.etcd.io/raft/v3/raftpb`; `Unmarshal` returns a non-nil error on malformed wire bytes. The test can produce malformed input with arbitrary non-empty bytes that are not a valid `Message` encoding.
- gRPC bidi streaming: returning a non-nil error from the server method terminates the stream and surfaces the status to the client; this is the intended "observable error" for AC-2.8.1.

### Testing Requirements

- Use the Go stdlib test stack (`testing`); no assertion/mocking libraries.
- Prefer same-package peer tests for white-box assertions and to reuse `forwardRaftStream`, `marshalRaftMessage`, and peer auth helpers.
- Tests must assert both the returned error (code) and the absence of side effects (router call count == 0).
- Suggested test names:
  - `TestForwardRaftStreamRejectsMalformedMessage`
  - `TestForwardRaftStreamMalformedMessageDoesNotRoute`
  - `TestForwardRaftRejectsMalformedMessage` (unary parity, if not already covered)
  - `TestForwardRaftMalformedAuditOutputRedacted`
- Suggested focused verification:

```sh
go test ./internal/peer/... -run 'ForwardRaft' -count=1
go test ./internal/peer/... ./internal/cmd/... -count=1
make package-boundaries
git diff --check
```

## References

- `_bmad-output/planning-artifacts/epics.md#Story 2.8` - Story 2.8 acceptance criteria (lines 865-894).
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-4 (lines 257-269), FR-5 (lines 271-285), NFR-3 (lines 513-515), NFR-8 (lines 529-532).
- `_bmad-output/project-context.md` - package boundaries, testing rules, redaction rules, workflow rules.
- `CONTEXT.md` - glossary and storage gateway invariants; Raft carries metadata/commands, peer gRPC carries bytes.
- `docs/adr/0024-production-topology-and-peer-scope-policy.md` - peer Shard-scope authorization before side effects.
- `docs/adr/0026-multi-shard-release-boundary.md` - `internal/peer` authorized Shard set source.
- `docs/go-style-guide.md` - Go errors, concurrency, tests, and logging rules.
- `_bmad-output/implementation-artifacts/2-7-bound-peer-replicatedocument-input-before-side-effects.md` - prior peer fail-before-side-effects pattern and evidence shape.
- `internal/peer/server.go` - `ForwardRaft` (line 437), `ForwardRaftStream` (line 456), `handleForwardRaftStreamRequest` (line 478, the fix site), `recordMalformedRaftMessage` (line 498), `recordRaftRouteError` (line 512).
- `internal/peer/transport.go` - `RaftRouter` interface (lines 22-30).
- `internal/peer/authorization_test.go` - peer auth context helpers and `marshalRaftMessage` (line 458).
- `internal/peer/audit_ratelimit_test.go` - `forwardRaftStream` test double and `forwardRouter`/`streamRouter` call-count pattern (lines 132-247).

## Dev Agent Record

### Agent Model Used

Cascade (dev-story workflow).

### Debug Log References

- CREATE-STORY: ran from sprint-status recommendation; target `2-8-reject-malformed-forwardraftstream-messages` (first backlog story by epic/story order).
- CREATE-STORY: resolved workflow customization (no activation hooks; persistent fact `_bmad-output/project-context.md`).
- CREATE-STORY: loaded epics Story 2.8, V2 master PRD FR-4/FR-5/NFR-3/NFR-8, previous Story 2.7, ADR 0024/0026 references, and read the fix-site code in `internal/peer/server.go` plus existing `ForwardRaft*` tests.
- DEV: discovered the existing `TestForwardRaftStreamLogsMalformedMessagesWithoutRouting` test asserted the buggy behavior (`errors.Is(err, io.EOF)` — malformed message silently dropped). Updated it to assert the corrected observable rejection, matching AC-2.8.1.
- ENV: `go test` failed with `compile: version "go1.26.1" does not match go tool version "go1.26.4"`. Root cause: `GOROOT` pinned to `1.26.1` (mise) while the active toolchain binary is `go1.26.4`. Worked around by running tests with `env -u GOROOT` so the 1.26.4 toolchain uses its own GOROOT. This is an environment skew, not a code issue.
- RED: `env -u GOROOT GOCACHE=/tmp/scrap-v2-go-build go test ./internal/peer/... -run 'ForwardRaft' -count=1` → FAIL (`ForwardRaftStream malformed error = EOF (Unknown), want InvalidArgument`). Log confirmed redaction: `err="proto: wrong wireType = 6 for field Vote"`, no raw bytes.
- GREEN: changed `handleForwardRaftStreamRequest` malformed return to `status.Errorf(codes.InvalidArgument, "unmarshal raft message: %v", err)`; rerun → `ok internal/peer 0.715s`.
- PASS: `env -u GOROOT GOCACHE=/tmp/scrap-v2-go-build go test ./internal/peer/... ./internal/cmd/... -count=1` → both `ok`.
- PASS: `env -u GOROOT GOCACHE=/tmp/scrap-v2-go-build make package-boundaries` → clean.
- PASS: `git diff --check` → clean.
- PASS: `env -u GOROOT go vet ./internal/peer/...` → clean.

### Completion Notes List

- Root defect: `handleForwardRaftStreamRequest` recorded malformed Raft bytes but returned `(recordAllowed, nil)`, silently dropping the message and letting the stream continue — so a peer transport bug could silently drop authority messages. Unary `ForwardRaft` already returned `codes.InvalidArgument`.
- Fix is a single return statement, mapping malformed stream input to the same `codes.InvalidArgument` + `"unmarshal raft message: %v"` shape as the unary path (AC-2.8.4). The stream now terminates visibly (AC-2.8.1).
- No Raft route side effect on malformed input: the unmarshal failure returns before `RouteRaftMessage`; tests assert router call count == 0 (AC-2.8.2).
- Audit/log output remains bounded and redacted: preserved `recordMalformedRaftMessage` (power-of-two log throttle, bounded fields) and added a regression assertion that raw message bytes are not logged (AC-2.8.3).
- Preserved scope: no change to `proto/`, `gen/`, `RaftRouter` interface, `recordRaftRouteError` post-unmarshal route handling, authorization ordering, or unary `ForwardRaft` behavior.
- Skipped broader `make check`/`make lint` (full-repo lint): change is a one-line error return with no added complexity; focused peer/cmd tests + `package-boundaries` + `go vet` cover the blast radius per NFR-7 narrowest-gate guidance. Flag for reviewer if a full Tier 1 gate is desired.

### File List

- `internal/peer/server.go`
- `internal/peer/raft_observability_test.go`
- `_bmad-output/implementation-artifacts/2-8-reject-malformed-forwardraftstream-messages.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

## Senior Developer Review (AI)

- **Reviewer:** Cascade (adversarial review, autonomous mode)
- **Date:** 2026-06-21
- **Outcome:** Approve. 0 Critical, 0 High, 0 Medium. 2 Low (acknowledged, no action).
- **AC validation:** AC-2.8.1 (observable `InvalidArgument` on malformed stream input), AC-2.8.2 (no `RouteRaftMessage` side effect — router call count asserted 0), AC-2.8.3 (bounded/redacted log preserved; added raw-bytes-not-logged assertion), and AC-2.8.4 (unary/stream parity test) are all IMPLEMENTED and test-backed.
- **File List vs git:** exact match; no undocumented or phantom changes.
- **Preservation check:** confirmed the peer gRPC server (`internal/cmd/tls.go:newPeerGRPCServerOptions`) wires only auth/identity interceptors + OTel stats handler; none log handler-returned statuses at ERROR level, so returning `InvalidArgument` keeps malformed peer input out of server ERROR logs (only the existing WARN), consistent with the pre-existing unary `ForwardRaft` behavior.
- **Action Items:** none.
- **Low (no action):**
  - [Low] Malformed message now terminates the entire `ForwardRaftStream` (previously logged-and-continued). This is AC-mandated and matches unary semantics; the leader transport reconnects and the malformed counter still increments. Acknowledged design trade-off.
  - [Low] The redaction regression asserts the specific ASCII payload (`"not a raft message"`) is absent from the log; adequate for this input, could be broadened later.

## Change Log

- 2026-06-21: Created Story 2.8 from sprint backlog; identified the malformed-decode swallow in `handleForwardRaftStreamRequest` as the root defect; set status to ready-for-dev.
- 2026-06-21: Implemented fix (malformed `ForwardRaftStream` messages now return `codes.InvalidArgument`, matching unary `ForwardRaft`); updated the prior EOF-asserting test and added a unary/stream parity + redaction test; focused peer/cmd tests, package-boundaries, vet, and `git diff --check` all green. Status → review.
- 2026-06-21: Adversarial code review — Approve (0 Critical/High/Medium, 2 acknowledged Low). Verified ACs, File List accuracy, and ERROR-log preservation. Status → done.
