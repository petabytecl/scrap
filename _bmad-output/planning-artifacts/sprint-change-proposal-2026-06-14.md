# Sprint Change Proposal: Data Integrity Release Blockers

Date: 2026-06-14
Project: scrap
Prepared for: Coto
Workflow: BMad Correct Course
Mode: Batch

## 1. Issue Summary

### Trigger

The triggering change is a release policy clarification from the user:

> Any data-integrity bug is release-blocking.

This was triggered by the thermo-nuclear audit of the current `main` state. The
audit found no high-severity production bug with confidence, but it did identify
several integrity-adjacent and release-evidence risks after all sprint stories
were marked `done`.

### Core Problem

The current V2 sprint status and closure artifacts imply release completion, but
the audit found unresolved risks that affect the guarantees behind the V2
release claim:

- `ReplicateDocument` on the peer surface lacks the public write path's metadata,
  chunk, and total Document bounds.
- `ForwardRaftStream` records malformed Raft messages but silently continues
  instead of returning an observable failure.
- The scrub coordinator has concurrency risks around duplicate `scrubID`, a
  single-slot result cache, and channel send while holding the coordinator lock.
- Block read verification skips SHA-256 validation when the stored digest is all
  zeros.
- Release evidence artifacts conflict: `closure-policy-final-gate-decision.md`
  records final `PASS`, while `release-tier-gates-evidence.md` and
  `release-evidence-matrix.md` still record `FAIL`.
- Critical integration coverage is missing for a vertical Shard/Raft/Backend
  S3/encryption/scrub data-integrity path.

### Evidence

Evidence sources reviewed:

- Thermo-nuclear audit findings from the current codebase review.
- V2 master PRD:
  `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md`
- Current epics:
  `_bmad-output/planning-artifacts/epics.md`
- V2 master architecture:
  `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md`
- Sprint status:
  `_bmad-output/implementation-artifacts/sprint-status.yaml`
- Closure policy:
  `docs/prd-closure-policy.md`
- Release evidence artifacts:
  `_bmad-output/implementation-artifacts/release-tier-gates-evidence.md`
  `_bmad-output/implementation-artifacts/release-evidence-matrix.md`
  `_bmad-output/implementation-artifacts/closure-policy-final-gate-decision.md`

## 2. Impact Analysis

### Epic Impact

Epic 1 remains structurally valid, but must reopen for read-side integrity
hardening:

- Add a release-blocking story for missing SHA-256 verification behavior.
- The story maps to FR-3 and NFR-1/NFR-7.

Epic 2 remains structurally valid, but must reopen for peer/Raft authority
hardening:

- Add a release-blocking story for peer `ReplicateDocument` input bounds.
- Add a release-blocking story for malformed `ForwardRaftStream` behavior.
- These stories map to FR-4, FR-5, NFR-2, and NFR-3.

Epic 3 remains structurally valid, but must reopen for scrub/data-integrity
coordination:

- Add a release-blocking story for deterministic scrub coordinator behavior.
- This maps to FR-3, FR-6, FR-8, NFR-1, and NFR-7.

Epic 6 remains structurally valid, but must reopen for release evidence
truthfulness and final data-integrity proof:

- Add a release-blocking story for reconciling contradictory release artifacts.
- Add a release-blocking story for vertical data-integrity evidence.
- These map to FR-15, FR-16, NFR-5, and NFR-7.

No current epic should be removed. No existing architecture decision is
invalidated. This is a direct adjustment to the release closure plan, not a
fundamental replan.

### Artifact Conflicts

PRD conflict:

- Section 4.2 says final release requires current linked evidence, but it does
  not explicitly name data-integrity bugs as non-waivable release blockers.
- Section 8 covers fail-closed storage behavior and risk-based tests, but should
  explicitly state that any unresolved data-integrity bug blocks release.

Epics conflict:

- `sprint-status.yaml` marks all stories `done`, but the audit discovered new
  release-blocking integrity work.
- Cross-epic release rules say missing P0 evidence is FAIL, but do not yet call
  out post-audit data-integrity findings as release blockers.

Release artifact conflict:

- `closure-policy-final-gate-decision.md` says final gate `PASS`.
- `release-tier-gates-evidence.md` says release gate `FAIL`.
- `release-evidence-matrix.md` says release gate `FAIL`.

Architecture impact:

- Existing boundaries remain correct. `internal/peer` should enforce transport
  bounds before side effects without importing Shard internals.
- `internal/shard` remains the owner of scrub coordination.
- No ADR is required unless implementation changes wire contracts, storage
  format, security/encryption contracts, or package ownership boundaries.

UX impact:

- No UI/UX artifact exists. Operator-facing CLI/evidence output is affected only
  through release artifacts and evidence commands.

### Technical Impact

Affected code and test areas:

- `internal/peer/server.go`
- `internal/peer/*_test.go`
- `internal/shard/scrub_coordinator.go`
- `internal/shard/scrub_coordinator_test.go`
- `internal/block/reader.go`
- `internal/block/reader_test.go`
- `scripts/check-e2e-gates.sh`
- `scripts/*_test.go`
- `test/integration/`
- Release evidence artifacts under `_bmad-output/implementation-artifacts/`

## 3. Checklist Results

### Section 1: Understand Trigger and Context

- [x] 1.1 Triggering story identified: audit after Story 6.7 final closure.
- [x] 1.2 Core problem defined: data-integrity bugs are non-waivable release
  blockers, and current closure artifacts do not enforce that policy.
- [x] 1.3 Supporting evidence gathered from audit, PRD, epics, sprint status,
  architecture, closure policy, and release artifacts.

### Section 2: Epic Impact Assessment

- [x] 2.1 Current epic can still complete after reopening release-blocking work.
- [x] 2.2 Existing epic scope should be modified by adding blocker stories.
- [x] 2.3 Remaining planned epics are all marked done but must be reopened where
  ownership applies.
- [x] 2.4 No future epic is invalidated; no new product epic is required.
- [x] 2.5 Priority changes: data-integrity remediation precedes release tagging.

### Section 3: Artifact Conflict and Impact Analysis

- [x] 3.1 PRD needs policy language for data-integrity blockers.
- [x] 3.2 Architecture remains valid; no ADR needed for local fixes.
- [N/A] 3.3 No UX specification exists.
- [x] 3.4 Release evidence artifacts and gate scripts need updates.

### Section 4: Path Forward Evaluation

- [x] 4.1 Direct Adjustment: viable. Effort medium, risk low-medium.
- [x] 4.2 Potential Rollback: not viable. Reverting completed work would not
  reduce the audit risks.
- [x] 4.3 PRD MVP Review: not needed. V2 scope remains intact.
- [x] 4.4 Recommended path: Direct Adjustment with release-blocking stories.

### Section 5: Sprint Change Proposal Components

- [x] 5.1 Issue summary created.
- [x] 5.2 Epic and artifact impacts documented.
- [x] 5.3 Recommended path provided.
- [x] 5.4 V2 release impact and action plan defined.
- [x] 5.5 Handoff plan defined.

### Section 6: Final Review and Handoff

- [x] 6.1 Checklist completion reviewed.
- [x] 6.2 Proposal drafted for user review.
- [x] 6.3 User approval received.
- [x] 6.4 `sprint-status.yaml` update approved for application.
- [x] 6.5 Handoff confirmed by approved proposal.

## 4. Recommended Approach

Recommended path: Direct Adjustment.

Rationale:

- The master PRD, epics, and architecture are still correct.
- The audit findings are focused and can be fixed inside existing package
  boundaries.
- Rollback would increase risk without removing the integrity issues.
- V2 scope does not need to shrink; release closure must become stricter.

Scope classification: Moderate.

This requires backlog reorganization and developer execution, but not a new PRD,
new architecture, or ADR unless an implementation chooses to alter wire/storage
contracts.

## 5. Detailed Change Proposals

### PRD Change Proposal

Artifact:
`_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md`

Section: `4.2 Release Rules`

OLD:

```text
Release-ready requires all required V2 features, documentation, and evidence
gates to be complete.
```

NEW:

```text
Release-ready requires all required V2 features, documentation, and evidence
gates to be complete.

Any confirmed or plausible data-integrity bug is a non-waivable release blocker
until fixed or explicitly disproven with current evidence. This includes bugs
that can return unverified Document bytes, ACK unsafe state, corrupt or fork
replicated state, silently drop Raft authority messages, bypass committed
metadata authority, publish partial restored Blocks, or let release evidence
contradict the actual data-integrity status.
```

Rationale:

The user's release policy must be reflected in the canonical V2 release rules.

Section: `8. Cross-Cutting NFRs`

OLD:

```text
- **NFR-7 Test coverage by risk:** Required features need positive path,
  fail-closed path, restart/rebuild or recovery path where relevant, and the
  narrowest local/deployed gate that proves the claim.
```

NEW:

```text
- **NFR-7 Test coverage by risk:** Required features need positive path,
  fail-closed path, restart/rebuild or recovery path where relevant, and the
  narrowest local/deployed gate that proves the claim.
- **NFR-8 Data-integrity blocker discipline:** Any unresolved data-integrity
  defect or contradictory data-integrity release evidence blocks final V2 PASS.
  Release closure must link the fix, regression test, affected requirement,
  verification command, and artifact before returning to PASS.
```

Rationale:

This makes the policy actionable during story creation and final closure.

### Epics Change Proposal

Artifact:
`_bmad-output/planning-artifacts/epics.md`

Section: `Cross-Epic Release Rules`

OLD:

```text
- Any missing P0 evidence is FAIL. Any high-risk evidence gap with an owner and
  mitigation is CONCERNS. Silent waivers are not allowed.
```

NEW:

```text
- Any missing P0 evidence is FAIL. Any high-risk evidence gap with an owner and
  mitigation is CONCERNS. Silent waivers are not allowed.
- Any confirmed or plausible data-integrity bug discovered after story closure
  reopens the owning feature or evidence epic and blocks final release PASS until
  a fix, regression test, verification command, and release artifact are linked.
```

Rationale:

Completed story status must not override a later integrity finding.

### New Release-Blocking Stories

#### Story 1.6: Fail Closed on Missing Document SHA-256 Verification

Requirements: FR-3, NFR-1, NFR-7, NFR-8.

As a release owner,
I want visible Document reads to fail closed when committed metadata lacks a
valid SHA-256 digest,
So that S.C.R.A.P. never serves unverified bytes.

Acceptance Criteria:

- AC-1.6.1: Given a Block reader entry with an all-zero SHA-256 digest, when
  read verification runs, then the read fails closed instead of skipping Document
  digest verification.
- AC-1.6.2: Given valid historical fixtures, when read verification runs, then
  the implementation either proves all production metadata has non-zero SHA-256
  or maps zero digest entries to a typed corruption failure.
- AC-1.6.3: Evidence includes targeted `internal/block` tests and one
  shard-level read verification test proving no partial or unverified bytes are
  returned.
- AC-1.6.4: Release evidence links the affected FR-3 row and records PASS,
  CONCERNS, or FAIL.

Expected verification:

```sh
go test ./internal/block/... ./internal/shard/...
```

#### Story 2.7: Bound Peer `ReplicateDocument` Input Before Side Effects

Requirements: FR-4, FR-5, NFR-2, NFR-3, NFR-8.

As a platform operator,
I want peer Document replication to enforce the same input bounds as public
writes before allocation-heavy work or side effects,
So that a buggy or compromised peer cannot pressure memory, disk, or replica
state outside the Document contract.

Acceptance Criteria:

- AC-2.7.1: Given a peer replication init with invalid transaction ID, Document
  name, or content type, when `ReplicateDocument` receives it, then the request is
  rejected before Block writer side effects.
- AC-2.7.2: Given a chunk larger than `MaxClientChunkBytes`, when received on the
  peer stream, then the request fails with a bounded typed error before buffering
  the full Document.
- AC-2.7.3: Given total replicated bytes exceeding `MaxDocumentBytes`, when the
  stream continues, then replication fails without publishing accepted state.
- AC-2.7.4: Given the `replicationSink` path is configured, when oversized input
  arrives, then it is bounded before `bytes.Buffer` can grow without limit.
- AC-2.7.5: Evidence includes tests in `internal/peer` and confirms `internal/peer`
  remains a transport boundary connected to Shard behavior through narrow
  interfaces.

Expected verification:

```sh
go test ./internal/peer/... ./internal/cmd/...
```

#### Story 2.8: Reject Malformed `ForwardRaftStream` Messages

Requirements: FR-4, FR-5, NFR-3, NFR-8.

As a platform operator,
I want malformed streamed Raft messages to fail visibly,
So that peer transport bugs cannot silently drop authority messages.

Acceptance Criteria:

- AC-2.8.1: Given malformed protobuf bytes on `ForwardRaftStream`, when the peer
  server handles the message, then the stream returns an observable error instead
  of `nil`.
- AC-2.8.2: Given a malformed message, when handling fails, then no Raft route
  side effect occurs.
- AC-2.8.3: Given malformed input is audited, when evidence is reviewed, then
  audit/log output remains bounded and redacted.
- AC-2.8.4: Unary `ForwardRaft` and streaming `ForwardRaftStream` have consistent
  error semantics for malformed Raft messages.

Expected verification:

```sh
go test ./internal/peer/...
```

#### Story 3.8: Make Scrub Coordination Concurrency Deterministic

Requirements: FR-3, FR-6, FR-8, NFR-1, NFR-7, NFR-8.

As a storage operator,
I want scrub coordination to behave deterministically under duplicate and
overlapping requests,
So that integrity verification and repair workflows cannot hang or lose results.

Acceptance Criteria:

- AC-3.8.1: Given a duplicate `scrubID`, when a second consistency check is
  proposed, then behavior is deterministic and the first waiter cannot hang
  indefinitely.
- AC-3.8.2: Given overlapping scrubs with different IDs, when results apply,
  then each result remains retrievable by ID for the defined retention window or
  is explicitly rejected with a documented policy.
- AC-3.8.3: Given `applyConsistencyCheck` notifies a waiter, when the send
  occurs, then the coordinator does not hold the mutex across a potentially
  blocking send.
- AC-3.8.4: Evidence includes deterministic concurrency tests using channels,
  contexts, or bounded polling, not sleeps.
- AC-3.8.5: Evidence includes race-sensitive verification.

Expected verification:

```sh
go test ./internal/shard/...
go test -race ./internal/shard/...
```

#### Story 6.8: Reconcile Release Evidence and Fail Closed on Contradictions

Requirements: FR-16, NFR-5, NFR-8.

As a release owner,
I want release artifacts and gate scripts to fail closed when evidence
contradicts itself,
So that SCRAP cannot report final PASS while required data-integrity or tier gate
evidence still reports FAIL.

Acceptance Criteria:

- AC-6.8.1: Given `closure-policy-final-gate-decision.md` says final `PASS` while
  `release-tier-gates-evidence.md` or `release-evidence-matrix.md` says `FAIL`,
  when gate validation runs, then final closure fails.
- AC-6.8.2: Given Tier 2/Tier 3 evidence has current PASS runs, when Story 6.5
  artifacts are updated, then all release artifacts cite the same commit/ref,
  run links, artifact names, retention, and redaction proof.
- AC-6.8.3: Given a release artifact references stale/local-only evidence, when
  final closure is evaluated, then the result is FAIL or CONCERNS, never PASS.
- AC-6.8.4: Evidence includes fixture tests for `check-e2e-gates.sh` and the
  release tier/closure validator path.

Expected verification:

```sh
go test ./scripts/...
make gates-check
```

#### Story 6.9: Add Vertical Data-Integrity Evidence Across Shard, Raft, Backend, Encryption, and Scrub

Requirements: FR-3, FR-4, FR-6, FR-8, FR-10, FR-16, NFR-7, NFR-8.

As a release owner,
I want one vertical data-integrity test path covering Shard authority, Raft
metadata, Backend storage, encryption, and scrub verification,
So that final release closure is not based only on isolated adapter tests.

Acceptance Criteria:

- AC-6.9.1: Given an encrypted Document is written through a Shard-backed path,
  when metadata commits and Backend upload/restore behavior is exercised, then
  reads return verified bytes or a typed failure.
- AC-6.9.2: Given corruption is introduced in the Block, Backend object, or
  committed metadata fixture, when read/scrub/restore runs, then S.C.R.A.P. fails
  closed without returning partial or unverified bytes.
- AC-6.9.3: Given scrub or repair evidence is collected, when artifacts are
  reviewed, then raw Document identifiers, Backend keys, key material, and
  Document bytes are absent.
- AC-6.9.4: Evidence states which boundaries are covered locally, which require
  Tier 2/Tier 3, and which are explicitly deferred with owner and mitigation.

Expected verification:

```sh
go test ./test/integration/...
make tier2-e2e-up
make tier3-evidence-up STRESS_SCENARIO=throughput
```

### Sprint Status Change Proposal

Artifact:
`_bmad-output/implementation-artifacts/sprint-status.yaml`

OLD:

```yaml
  epic-1: in-progress
  1-1-durable-document-write-ack: done
  ...
  6-7-closure-policy-and-final-gate-decision: done
```

NEW:

```yaml
  epic-1: in-progress
  1-6-fail-closed-on-missing-document-sha256-verification: backlog

  epic-2: in-progress
  2-7-bound-peer-replicatedocument-input-before-side-effects: backlog
  2-8-reject-malformed-forwardraftstream-messages: backlog

  epic-3: in-progress
  3-8-make-scrub-coordination-concurrency-deterministic: backlog

  epic-6: in-progress
  6-8-reconcile-release-evidence-and-fail-closed-on-contradictions: backlog
  6-9-add-vertical-data-integrity-evidence-across-shard-raft-backend-encryption-and-scrub: backlog
```

Rationale:

All affected epics must remain open until the release-blocking integrity stories
are completed and evidenced.

## 6. Implementation Handoff

Scope classification: Moderate.

Recommended routing:

- Product Owner / planning agent: approve the new blocker stories and update
  `epics.md` plus `sprint-status.yaml`.
- Developer agent: implement the stories one at a time with failing tests first.
- Test Architect: run ATDD/traceability for the data-integrity stories before
  final closure.
- Release owner: rerun Tier 2/Tier 3/final closure evidence after blockers are
  fixed.

Recommended implementation order:

1. Story 6.8: Reconcile release evidence and make contradictions fail closed.
2. Story 2.7: Bound peer `ReplicateDocument` input before side effects.
3. Story 2.8: Reject malformed `ForwardRaftStream` messages.
4. Story 3.8: Make scrub coordination concurrency deterministic.
5. Story 1.6: Fail closed on missing Document SHA-256 verification.
6. Story 6.9: Add vertical data-integrity evidence and rerun release closure.

Why this order:

- Evidence truth comes first so later fixes cannot be hidden behind inconsistent
  artifacts.
- Peer and Raft authority risks are next because they sit near replication and
  metadata authority.
- Scrub coordination follows because it affects integrity verification and
  repair workflows.
- SHA-256 zero handling is narrower but directly touches read verification.
- Vertical evidence should run after focused fixes to avoid chasing known
  failures.

## 7. Success Criteria

The course correction is complete when:

- PRD release rules explicitly mark data-integrity bugs as non-waivable release
  blockers.
- Epics contain release-blocking stories 1.6, 2.7, 2.8, 3.8, 6.8, and 6.9.
- `sprint-status.yaml` marks those stories as `backlog` or later workflow
  states.
- Each story has test-first implementation evidence.
- `release-tier-gates-evidence.md`, `release-evidence-matrix.md`, and
  `closure-policy-final-gate-decision.md` no longer contradict each other.
- Final release closure returns PASS only after the data-integrity blockers are
  fixed, verified, and linked.

## 8. Approval Status

Status: approved by user on 2026-06-14.

Approved updates:

- PRD release rules and NFRs.
- Epics cross-epic release rules and blocker story split.
- Sprint status backlog entries for the new blocker stories.

Next route to story creation and development.
