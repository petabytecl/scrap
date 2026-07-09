# Sprint Change Proposal: Thermo-Nuclear Remediation (31 Findings)

Date: 2026-07-09
Project: scrap
Prepared for: Coto
Workflow: BMad Correct Course
Mode: Batch
Classification: Major course correction
Baseline commit: `03798da1b57429d2243732c061784ca859f3c343`

## 1. Issue Summary

### Trigger

A full-project thermo-nuclear review of `main` at `03798da` produced **31**
High/Medium findings (`H-01`–`H-19`, `M-01`–`M-12`) that contradict the
current release `PASS` claim in `closure-policy-final-gate-decision.md`.

### Core Problem

V2 scope, DG-1–DG-5, and the six-epic spine remain valid, but stop-ship
authority gaps exist across write/ACK, Raft apply, Projection rebuild,
Upload Outbox, Backend confirmation, production topology, peer trust,
encryption routing, and release evidence freshness. Existing done stories
remain historical evidence; this proposal adds sibling remediation stories.

### Deduplication note

Before publishing new GitHub issues, deduplicate against
`_bmad-output/implementation-artifacts/deferred-work.md`, especially
#454–#458 and #461–#471. Several deferred items overlap Projection
rebuild, Backend restore, scrub, and encryption paths; remediation stories
must reference those issues rather than fork parallel trackers.

## 2. Course-Correction Decision

- Keep V2 PRD scope and six-epic structure (direct adjustment, not replan).
- Reopen all six epics; final release decision is **FAIL** until remediation
  and exact-SHA evidence close.
- Keep Stories 6.8 and 6.9; Story 6.8 executes first (Wave 1).
- Finding IDs are the canonical traceability keys.

## 3. Finding Traceability Matrix

| Finding | Severity | FR/NFR | Governing ADR | Owning epic | Remediation story | Depends on | Verification |
| --- | --- | --- | --- | --- | --- | --- | --- |
| H-02 | High | FR-2 | ADR-0033 (new) | Epic 1 | 1-7: Proposal-scoped CommitDocument waiters | none | `go test ./internal/shard/...` |
| H-05 | High | FR-3,FR-4 | ADR-0034 (new) | Epic 1 | 1-8: Rebuild Projection from Raft/quorum authority | 2-9 | `go test ./internal/shard/...` |
| M-11 | Medium | FR-3 | ADR-0034 (new) | Epic 1 | 1-9: Durable Projection swap and complete Transaction Resolution | 1-8 | `go test ./internal/shard/... ./internal/index/...` |
| M-01 | Medium | FR-3 | ADR-0002 amend | Epic 1 | 1-10: Immutable verified snapshot before ReadDocument stream | none | `go test ./internal/block/... ./internal/shard/...` |
| H-03 | High | FR-4 | ADR-0034 (new) | Epic 2 | 2-9: Propagate apply failures and reject unknown Raft commands | none | `go test ./internal/shard/... ./internal/raft/...` |
| H-01 | High | FR-2,FR-4 | ADR-0033 (new) | Epic 2 | 2-10: Term-fenced ReplicateDocument and write leadership recheck | 2-9 | `go test ./internal/shard/... ./internal/peer/...` |
| H-04 | High | FR-2,FR-4 | ADR-0033 (new) | Epic 2 | 2-11: Byte-ready voter ledger and leadership fence | 2-10 | `go test ./internal/shard/...` |
| H-14 | High | FR-2,NFR-2 | ADR-0033 (new) | Epic 2 | 2-12: Concurrent quorum replication with per-peer deadlines | 2-10 | `go test ./internal/shard/... ./internal/peer/...` |
| H-09 | High | FR-5,FR-9 | ADR-0024 amend | Epic 2 | 2-13: Explicit production multi-voter membership | none | `go test ./internal/cmd/...` |
| H-11 | High | FR-5 | ADR-0035 (new) | Epic 2 | 2-14: Persist placement identity; reject silent Transaction remap | none | `go test ./internal/cmd/...` |
| H-12 | High | FR-4,FR-9 | ADR-0024 amend | Epic 2 | 2-15: Bind peer principal to Raft message sender | none | `go test ./internal/peer/...` |
| H-13 | High | FR-4,NFR-2 | ADR-0036 (new) | Epic 2 | 2-16: Protocol-valid Block transitions and streaming transfers | 2-15 | `go test ./internal/peer/... ./internal/shard/...` |
| M-03 | Medium | FR-4,FR-5 | ADR-0026 amend | Epic 2 | 2-17: Shard-scoped Light Scrub peer RPC routing | 2-15 | `go test ./internal/scrub/... ./internal/cmd/...` |
| M-10 | Medium | FR-5 | ADR-0024 amend | Epic 2 | 2-18: Explicit per-Member public address map for LeaderHint | 2-13 | `go test ./internal/cmd/... ./internal/server/...` |
| H-07 | High | FR-6 | ADR-0037 (new) | Epic 3 | 3-9: Durable SealBlock intent and closed-Block Upload Outbox reconciliation | 2-9 | `go test ./internal/shard/...` |
| H-08 | High | FR-6 | ADR-0037 (new) | Epic 3 | 3-10: Verified upload, immutable generations, term-fenced workers | 3-9,2-10 | `go test ./internal/shard/... ./internal/backend/...` |
| H-10 | High | FR-6,FR-9 | ADR-0024 amend | Epic 3 | 3-11: Reject Member-local filesystem Backend in production | none | `go test ./internal/cmd/...` |
| M-02 | Medium | FR-8,NFR-2 | ADR-0027 amend | Epic 3 | 3-12: Bounded cold restore worker and timeouts | 3-10 | `go test ./internal/shard/...` |
| M-04 | Medium | FR-3 | ADR-0002 amend | Epic 3 | 3-13: Deep Scrub read-parity verification and durable checkpoint | none | `go test ./internal/block/... ./internal/shard/...` |
| H-17 | High | FR-9,FR-10 | ADR-0019 amend | Epic 4 | 4-8: Trusted CA bundle in image or explicit mount contract | none | `go test ./internal/cmd/...; docker build smoke` |
| H-18 | High | FR-10 | ADR-0038 (new) | Epic 4 | 4-9: Route unwrap/rewrap by stored Transit envelope identity | none | `go test ./internal/encryption/... ./internal/shard/...` |
| M-06 | Medium | FR-10 | ADR-0038 (new) | Epic 4 | 4-10: Monotonic Rewrap key version enforcement | 4-9 | `go test ./internal/shard/... ./internal/encryption/...` |
| M-07 | Medium | FR-9,NFR-2 | ADR-0019 amend | Epic 4 | 4-11: Write stream idle deadlines and per-principal quotas | none | `go test ./internal/server/...` |
| M-08 | Medium | FR-9 | ADR-0019 amend | Epic 4 | 4-12: Context-bounded graceful shutdown | none | `go test ./internal/cmd/... ./internal/peer/...` |
| M-12 | Medium | FR-9 | ADR-0019 amend | Epic 4 | 4-13: Distinct server/client TLS configuration per surface | none | `go test ./internal/security/...` |
| H-06 | High | FR-12 | ADR-0025 amend | Epic 5 | 5-8: Replay-safe Content Quarantine lifecycle commands | 2-9 | `go test ./internal/shard/... ./internal/index/...` |
| M-05 | Medium | FR-11 | ADR-0008 amend | Epic 5 | 5-9: Wire production plaintext Content Scanner engine | 4-9 | `go test ./internal/avscan/... ./internal/cmd/...` |
| H-19 | High | FR-16,NFR-5 | ADR-0006 amend | Epic 6 | 6-10: Exact-SHA release evidence gate and deny-by-default .dockerignore | 6-8 | `go test ./scripts/...; make gates-check` |
| H-15 | High | NFR-2 | ADR-0036 (new) | Epic 6 | 6-11: Stream/spool replication and Backend upload; weighted memory budget | 2-12 | `go test ./internal/shard/... ./internal/backend/...` |
| H-16 | High | FR-16 | ADR-0015 amend | Epic 6 | 6-12: Cilium-only network policy component | none | `kustomize build local; Kind apply` |
| M-09 | Medium | FR-9,NFR-4 | ADR-0019 amend | Epic 6 | 6-13: Production telemetry and Member identity fail-closed | 2-13 | `go test ./internal/cmd/... ./internal/telemetry/...` |
| *(release truth)* | — | FR-16, NFR-5, NFR-8 | ADR-0006 amend | Epic 6 | 6-8: Reconcile release evidence | none | `go test ./scripts/...`; `make gates-check` |
| *(vertical integrity)* | — | FR-3,FR-4,FR-6,FR-8,FR-10,FR-16 | DG-5 | Epic 6 | 6-9: Vertical data-integrity evidence | Waves 1–8 | `go test ./test/integration/...`; Tier 2/3 |

Coverage: 19 High + 12 Medium = 31 findings mapped exactly once (H-19 owned by Story 6.10).

## 4. Epic Ownership

- **Epic 1:** H-02, H-05, M-01, M-11
- **Epic 2:** H-01, H-03, H-04, H-09, H-11–H-14, M-03, M-10
- **Epic 3:** H-07, H-08, H-10, M-02, M-04
- **Epic 4:** H-17, H-18, M-06–M-08, M-12
- **Epic 5:** H-06, M-05
- **Epic 6:** H-15, H-16, H-19, M-09 (+ Stories 6.8, 6.9)

## 5. Remediation Wave Order

1. Story 6.8 + Story 6.10 (H-19 evidence/build baseline)
2. Story 2.9 + Story 1.7 (H-03, H-02)
3. Stories 2.10–2.12 (H-01, H-04, H-14)
4. Stories 1.8–1.9 + 5.8 (H-05, M-11, H-06)
5. Stories 3.9–3.12 (H-07, H-08, H-10, M-02)
6. Stories 2.13–2.18 (H-09, H-11–H-13, M-03, M-10)
7. Stories 1.10, 6.11, 4.9–4.13, 6.13 (M-01, H-15, H-18, M-06–M-08, M-12, M-09)
8. Stories 3.13, 5.9, 6.12, 4.8 (M-04, M-05, H-16, H-17)
9. Story 6.9 + exact-SHA Tier gates + fresh thermo-nuclear review

## 6. Required Artifact Updates

- Master PRD: add production gates for voter count, shared Backend, placement
  identity, peer-to-Raft binding, stored Transit identity, scanner-engine
  composition, and exact-SHA release evidence.
- `epics.md`: append remediation stories listed above.
- `sprint-status.yaml`: mark remediation stories `backlog`; keep historical
  done stories unchanged; all epics remain `in-progress`.
- Reconcile `closure-policy-final-gate-decision.md`,
  `release-evidence-matrix.md`, and `release-tier-gates-evidence.md` to one
  **FAIL** baseline citing the 31 findings.
- Update `docs/prd-closure-policy.md` so unresolved integrity findings, stale
  evidence, `make static`, and `make vuln` are non-waivable blockers.

## 7. Architecture Decision Gates

Before production code for the affected waves, accept:

- **ADR-0033** write authority (proposal correlation, term/epoch fence,
  disabled follower forwarding, byte-ready eligibility)
- **ADR-0034** Raft apply and Projection rebuild authority
- **ADR-0037** Backend durability (seal intent, reconciliation, verified
  upload, leadership-fenced workers, immutable generations)
- **ADR-0035** durable placement identity + transfer prerequisite
- **ADR-0036** bounded peer transfer/replication and process-wide byte admission
- **ADR-0038** stored Transit mount/key routing, readiness, TLS trust, monotonic Rewrap
- Amend ADR 0025 (quarantine idempotency), 0015 (Cilium composition),
  0019 (per-surface TLS), 0006/0008/0024/0027 as needed

## 8. Success Criteria

- Every finding ID maps exactly once to accepted story evidence.
- Release artifacts agree on FAIL until Waves 1–9 close on the exact SHA.
- Final PASS requires Story 6.9 vertical integrity evidence and a fresh
  thermo-nuclear review with no unresolved Medium-or-higher findings.

## 9. Approval Status

Status: approved for implementation via Thermo-Nuclear Remediation Plan
(2026-07-09). PM/Architect must accept ADR drafts before production code for
cross-package contracts; PO/Developer sequences stories; Test Architect owns
fault-injection and Tier 2/3 acceptance.

