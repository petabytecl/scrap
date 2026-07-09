# Story 6.9 — Vertical Data-Integrity Evidence

Artifact status: local/package evidence for remediation baseline
Release gate status: FAIL (Tier 2/Tier 3 and exact-SHA regeneration still required)

Story: 6.9 - Add Vertical Data-Integrity Evidence Across Shard, Raft, Backend, Encryption, and Scrub
Remediation baseline: `03798da` plus in-tree thermo-nuclear remediation changes
Generated: 2026-07-09

## Scope

This artifact records the vertical integrity path covered by package and
integration tests after Waves 1–8 remediation. It does **not** claim final
SCRAP release PASS. Exact-SHA Tier 2/Tier 3 and real OpenBao/S3 rehearsal remain
required before Story 6.9 can contribute to final PASS.

## Boundaries Covered Locally

| Boundary | Evidence | Command | Status |
| --- | --- | --- | --- |
| Shard write ACK + proposal correlation | `internal/shard` consensus remediation tests; `proposal_id` on CommitDocument | `go test ./internal/shard/ -run 'CommitProposal\|ApplyEntry\|OpenlogWriteAttempt'` | PASS (package) |
| Raft apply fail-closed | unknown command rejection; doc-count preflight | `go test ./internal/shard/ -run 'ApplyEntry\|Preflight'` | PASS (package) |
| Term-fenced replication | stale-term rejection | `go test ./internal/shard/ -run 'ValidateReplicationFence'` | PASS (package) |
| Byte-ready read fence | missing Frames fail closed on read path | `go test ./internal/shard/ -run 'EnsureCommitDocumentBytesReady'` | PASS (package) |
| Content Quarantine replay-safe apply | index confirm/release idempotency | `go test ./internal/index/ ./internal/shard/ -run 'Quarantine\|ContentQuarantine'` | PASS (package) |
| Seal intent / Upload Outbox | seal intent tests | `go test ./internal/shard/ -run 'SealIntent'` | PASS (package) |
| Verified upload / leadership fence | upload controller boundary tests | `go test ./internal/shard/ -run 'Upload'` | PASS (package) |
| Read all-or-error spool | block spool read tests | `go test ./internal/block/ -run 'Spool\|TwoPass'` | PASS (package) |
| Restore budget | restore semaphore/timeout | `go test ./internal/shard/ -run 'Restore'` | PASS (package) |
| Deep Scrub read-parity + durable checkpoint | VerifyHeader before VerifyBlock; `.deep-scrub-checkpoint` | `go test ./internal/shard/ -run 'DeepScrub'` | PASS (package) |
| Process-wide byte admission | `internal/admission` shared budget on write + transfer | `go test ./internal/admission/ ./internal/shard/ -run 'ByteAdmission\|Budget'` | PASS (package) |
| Streaming peer TransferBlock | `TransferBlockToFiles` writes staging files | `go test ./internal/peer/ ./internal/scrub/ ./internal/shard/ -run 'Transfer\|Repair\|Replica'` | PASS (package) |
| Multi-Shard Light Scrub admin hook | admin fan-out across local Shards | `go test ./internal/cmd/ -run 'MultiShardTestHooks'` | PASS (package) |
| Content Scanner production composition | `UnavailableEngine` (never false-CLEAN) | `go test ./internal/avscan/ ./internal/cmd/` | PASS (package; real ClamAV/YARA still deferred) |
| Encryption Transit routing / monotonic Rewrap | encryption router + rewrap tests | `go test ./internal/encryption/ ./internal/shard/ -run 'Rewrap\|Router\|Transit'` | PASS (package) |
| Release evidence contradiction fail-closed | Story 6.8 consistency script | `go test ./scripts/ -run 'ReleaseEvidenceConsistency'`; `bash scripts/check-release-evidence-consistency.sh` | PASS (aligned FAIL) |

## Corruption / Fail-Closed Fixtures

- Missing Document Frames at committed offset → `ErrDataLoss` on read readiness check.
- MaxUint16 Transaction cardinality → preflight rejects before `.idx` mutation.
- Stale replication term → `ErrFailedPrecondition`.
- Closure PASS vs matrix/tier FAIL → consistency gate rejects.

## Redaction

This artifact contains no Document payloads, Backend keys, credentials, trace IDs,
request IDs, or host-absolute paths beyond repository-relative test paths.

## Deferred to Tier 2 / Tier 3 / Exact-SHA

| Boundary | Why deferred | Owner |
| --- | --- | --- |
| Multi-Member Tier 2 E2E | Requires Kind/Cilium Cell on candidate SHA | Release owner |
| Tier 3 evidence bundle | Requires `make tier3-evidence-up` on candidate SHA | Release owner |
| Real OpenBao/S3 rehearsal | Exact-SHA `commit_ref` gate (`H-19`) | Release owner |
| 128 MiB Document under 512 MiB pod | Needs deployed memory evidence | Release owner / Story 6.11 |
| Fresh thermo-nuclear review | Required after Waves 1–8 merge | Release owner |

## Decision

Story 6.9 local/package vertical evidence is recorded. Final release remains
**FAIL** until exact-SHA Tier 2/Tier 3, real S3/IAM, memory evidence, and a
fresh thermo-nuclear review close with no unresolved Medium-or-higher findings.
