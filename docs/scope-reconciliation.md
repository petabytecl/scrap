# SCRAP Scope Reconciliation

Status: Draft
Date: 2026-06-10

## Purpose

This document reconciles the current SCRAP repo, accepted ADRs, BMAD planning
artifacts, GitHub issue state, and older project documentation after the
release rule was clarified:

> SCRAP is the major release line. There are no intermediate releases. SCRAP is not
> release-ready until all required SCRAP features are complete.

Implementation phases remain useful sequencing labels, but they are not release
boundaries.

## Source Rules

Use these rules when classifying scope:

1. `CONTEXT.md` and accepted ADRs define the durable SCRAP contract unless later
   ADRs supersede them.
2. V1 material is reference input, not a SCRAP requirement by itself. SCRAP only
   inherits old V1 work when it is re-derived into SCRAP docs, ADRs, PRDs, or
   issues.
3. A closed ADR issue means the decision was accepted. It does not prove the
   feature was implemented.
4. A closed implementation issue means the slice was implemented only for the
   scope stated by that issue. It does not close future phases or deferred
   follow-ups.
5. Release evidence is a final gate. It should not be confused with unfinished
   product features.

## Current Tracker Snapshot

- Branch checked: `main`.
- GitHub milestone `storage-gateway`: `0` open, `110` closed.
- Open SCRAP issue found: `#429 Pre-release release: capture real S3/IAM production
  rehearsal evidence`.
- Local dirty file present before this audit:
  `_bmad-output/planning-artifacts/epics.md`.

The milestone being closed is useful progress evidence, but it is not enough to
claim all required SCRAP features are complete.

## Scope Matrix

| Area | Source | SCRAP classification | Current state | Required next action |
| --- | --- | --- | --- | --- |
| Core Document API, immutable Document identity, Transaction grouping, Block/Frame format, checksum semantics | `CONTEXT.md`, ADR 0001-0005, Phase 1/2 issues | Required | Implemented by earlier SCRAP slices | Keep covered by final regression evidence |
| Multi-voter Raft, peer replication, Member identity, fail-closed Projection Resolution | `CONTEXT.md`, ADR 0014, Phase 2 issues | Required | Implemented by earlier SCRAP slices | Keep covered by final regression/e2e evidence |
| Deep Scrub, Block Quarantine, repair, and rebuild behavior | Phase 2b issues, `CONTEXT.md` | Required | Implemented by closed Phase 2b and follow-up work | Keep covered by final regression/evidence |
| Backend upload, Upload Outbox, upload pressure, Confirmed Upload Catalog | ADR 0009, ADR 0010, Phase 3, regenerated Epic 4 | Required | Implemented and recently hardened | Keep covered by final regression/evidence |
| OpenTelemetry evidence plane | ADR 0012, ADR 0013, issues #312-#318 | Required | Code and evidence stack exist; focused tests pass | Produce final SCRAP evidence matrix and current evidence bundle |
| Prod-like Kind/Cilium verification and `scrapctl` gates | ADR 0015, issue #350 | Required | Implemented as a validation lane | Re-run as part of final SCRAP evidence, not as release-only polish |
| Phase 4 partial local eviction and full-Block restore | ADR 0016, ADR 0017, ADR 0018, issues #372-#380 | Required | Implemented by closed Phase 4 slices | Keep covered by final evidence; do not treat it as Phase 5 cold-only reads |
| Production security mode, mTLS, authz, peer scope, audit, rate limits | ADR 0019, ADR 0024, issues #399, #401-#404, #430-#434 | Required | Implemented by closed Phase 4.5/security follow-ups | Keep covered by final SCRAP security evidence |
| OpenBao Transit envelope encryption and durable rewrap | ADR 0020, ADR 0021, ADR 0023, issues #400, #405-#407 | Required | Implemented by closed Phase 4.5 slices | Keep covered by final SCRAP security/e2e evidence |
| Real S3/IAM production rehearsal | `docs/production-rehearsal.md`, `docs/prd-closure-policy.md`, issue #429 | Required final gate | Incomplete; #429 is open | Run and link real non-local S3/IAM evidence after feature scope is complete |
| `scrapctl` operator surface | ADR 0015, ADR 0016, Phase 4/4.5 docs | Required | `doctor`, `status`, `leader`, `peers`, `upload-pressure`, `fault`, `evidence`, and `eviction` exist | Add OpenBao bootstrap commands for local/prod-like workflows |
| `scrapctl` OpenBao bootstrap | `docs/phase-4.5-security-implementation-slices.md` deferred follow-up, master architecture | Required for local/prod-like workflows unless explicitly superseded | No implementation issue found; production rehearsal script bootstraps OpenBao outside `scrapctl` | Create implementation issue/story for `scrapctl openbao bootstrap` |
| Content Scanner and Content Quarantine | `CONTEXT.md`, ADR 0008, ADR 0025 | Required | Accepted architecture exists; no `internal/avscan`, `QuarantineDocument`, or `scan_status` implementation found | Create PRD/epics/stories |
| Content Quarantine admin operations | ADR 0008 amended by ADR 0025 | Required through existing admin HTTP plus `scrapctl` | Current admin surface is HTTP-oriented; ADR 0025 keeps that shape for SCRAP | Include in Content Scanner/Quarantine backlog |
| Phase 5 cold-only reads / all-local-copy eviction | `CONTEXT.md`, ADR 0016, ADR 0020, ADR 0027 | Required as restore-first cold reads | Not implemented; ADR 0027 rejects direct Backend streaming for SCRAP | Create Phase 5 epics/stories |
| Direct Backend ciphertext streaming | ADR 0016, ADR 0020, ADR 0027 | Out of SCRAP unless re-chartered | Rejected for Phase 4 and out of SCRAP by ADR 0027 | Do not include in SCRAP backlog |
| Multi-Shard routing and fixed hash slots | `CONTEXT.md`, ADR 0024, ADR 0026 | Required | Current `scrapd` app wires single Shard ID `0`; core types carry `shard_id` | Create multi-Shard routing/startup epics/stories |
| Alert definitions, incident workflows, operator runbooks | ADR 0012, production-readiness docs | Required for a production-ready major release unless explicitly deferred | ADR 0012 deferred them out of the telemetry decision, not necessarily out of SCRAP | Create SCRAP operator docs/runbook backlog |
| Documentation closure | README, PRD/BMAD artifacts, ADRs, issue tracker | Required | Docs mix completed phases, deferred work, old generated status, and future scope | Reconcile docs after backlog classification |

## Explicit Non-Requirements Unless Re-Chartered

The audited docs do not currently make these required for SCRAP release:

- S3-compatible API.
- Public deletion API.
- `tenant_id` as storage identity.
- Tenant-specific key policy.
- Tenant quota authority.
- Cell federation.
- Metadata encryption.
- Hot certificate reload.
- Transparent migration for old unencrypted development Blocks.

If any of these are intended SCRAP release features, they need explicit SCRAP PRD or
ADR work before implementation.

## Required Discussion Points

These are the scope decisions now closed for the next implementation backlog:

1. ADR 0008 Content Scanner / Content Quarantine is required for SCRAP. ADR 0025
   amends the admin surface to existing admin HTTP plus `scrapctl`.
2. Multi-Shard startup/routing is required for SCRAP by ADR 0026.
3. Phase 5 read shape is restore-first cold reads by ADR 0027. Direct Backend
   ciphertext streaming is out of SCRAP unless re-chartered.
4. `scrapctl` owns OpenBao bootstrap for local/prod-like workflows unless
   explicitly superseded. Production OpenBao lifecycle remains platform-owned.
5. Runbooks, alert/query references, evidence instructions, and closure policy
   updates are required for a major release.

## Recommended Next Backlog Order

1. Regenerate SCRAP epics/stories from the master PRD, master architecture, ADR
   0025, ADR 0026, and ADR 0027.
2. Add Content Scanner / Content Quarantine epics and stories.
3. Add multi-Shard routing/startup epics and stories.
4. Add Phase 5 restore-first cold-read epics and stories.
5. Add `scrapctl` OpenBao bootstrap stories.
6. Add operator runbook, alert/query, and documentation-closure stories.
7. Run final evidence only after the above product scope is complete:
   - Tier 2 prod-like Cilium gate.
   - Tier 3 telemetry/evidence bundle.
   - `make production-rehearsal-security`.
   - `make production-rehearsal` with real S3/IAM to close #429.

## Working Verdict

SCRAP is not feature complete.

The current repository appears substantially complete for Phases 1 through 4.5
as previously scoped. The remaining required SCRAP work is not just release
polish: Content Scanner / Content Quarantine, Phase 5 cold-read behavior,
multi-Shard routing, `scrapctl` OpenBao bootstrap, operator docs, and
final evidence all need explicit backlog treatment before SCRAP can be called
release-ready.
