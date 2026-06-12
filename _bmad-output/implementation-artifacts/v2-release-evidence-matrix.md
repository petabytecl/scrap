# V2 Release Evidence Matrix

Status: complete
Current V2 release gate: FAIL

Story: 6.1 - V2 Release Evidence Matrix
Baseline commit: `c7e1171fc24558456e572421e8831f1f22368a55`
Branch: `v2`
Generated: 2026-06-12T17:46:51-04:00
Last updated: 2026-06-12T17:51:16-04:00

## Scope

This artifact maps V2 release evidence across FRs, ADR gates, BMAD stories,
GitHub issue state, verification commands, artifact paths, and current closure
status.

This is aggregation evidence only. It does not create missing feature behavior,
runbooks, alert/query references, Tier 2 or Tier 3 evidence, production
rehearsal output, or real S3/IAM proof.

## Source Inputs

| Input | Command or path | Result |
| --- | --- | --- |
| Current branch/ref | `git rev-parse HEAD` after Story 6.1 creation | `c7e1171fc24558456e572421e8831f1f22368a55` |
| Sprint status | `_bmad-output/implementation-artifacts/sprint-status.yaml` | Story 6.1 in review; Epic 6 in progress. |
| Master PRD | `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` | FR-1 through FR-16 and acceptance/evidence matrix. |
| Master architecture | `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` | DG-5 release documentation/evidence standard. |
| Epics | `_bmad-output/planning-artifacts/epics.md` | Epic 6 and Story 6.1 acceptance criteria. |
| ADRs | `docs/adr/*.md` | ADR 0001 through ADR 0027 are all `Accepted`. |
| Closure policy | `docs/prd-closure-policy.md` | Tier 2, Tier 3, production rehearsal, and real S3/IAM proof rules. |
| Production rehearsal docs | `docs/production-rehearsal.md` | `production-rehearsal-security` vs real S3/IAM `production-rehearsal` semantics. |
| Milestone issue query | `gh issue list --repo petabytecl/scrap --milestone storage-gateway-v2 --state all --json number,title,state,labels,milestone,url,updatedAt --limit 200` | 104 issues: 104 closed, 0 open. |
| Real S3/IAM gate issue | `gh issue view 429 --repo petabytecl/scrap --json number,title,state,body,comments,labels,milestone,url,updatedAt` | Open, labels `ready-for-human,production-readiness,v2,e2e`, milestone `NONE`. |

## Matrix Schema

Every release row is evaluated against this schema:

| Column | Meaning |
| --- | --- |
| Requirement type | FR, ADR, Story, issue, or release gate. |
| Requirement ID | Stable identifier such as `FR-1`, `ADR-0025`, or `#429`. |
| Source | Source document, ADR, story, issue, or workflow. |
| Owning story/epic | BMAD owner or release gate owner. |
| GitHub issue/PR | GitHub tracker linkage if known. |
| Evidence command | Command proving the row, or source artifact that records exact commands. |
| Artifact path | Evidence artifact path, workflow artifact, or issue URL. |
| Environment | Local, prod-like Kind/Cilium, Tier 3 evidence Cell, production security rehearsal, or real S3/IAM. |
| Owner | Owner for unresolved gaps. |
| Timestamp | Evidence timestamp from source artifact or live query time. |
| Commit/ref | Commit or branch ref tested by evidence. |
| Expected result | Claim the evidence is expected to prove. |
| Actual result | Observed outcome. |
| Redaction proof | Evidence that public/tracker output is bounded and leak-safe. |
| Freshness decision | Current, scoped, stale, local-only, or missing. |
| Release status | `PASS`, `CONCERNS`, or `FAIL`. |
| Mitigation/next owner | Required action before final V2 release. |

## Current Release Decision

| Gate | Status | Reason | Owner |
| --- | --- | --- | --- |
| Feature-scope evidence through Epic 5 | CONCERNS | Epics 1-5 have current linked artifacts, but several rows are intentionally scoped to local/package or prod-like evidence rather than final release closure. | Release owner / Story 6.1 matrix. |
| Epic 6 release documentation/evidence | FAIL | Runbooks, alert/query references, release evidence bundle, Tier 2/Tier 3 release gates, real S3/IAM rehearsal, and final closure policy are still backlog/incomplete. | Stories 6.2-6.7. |
| Real S3/IAM Backend proof | FAIL | Issue `#429` is open and outside the milestone; real non-local S3/IAM `make production-rehearsal` evidence is missing. | Story 6.6 / issue `#429`. |
| Final V2 release gate | FAIL | FR-16 requires current linked evidence for all required release claims. The missing Epic 6 gates prevent PASS. | Story 6.7. |

## FR Evidence Matrix

| FR | Source | Owning story/epic | Issue/PR | Evidence command/artifact | Environment | Timestamp / commit-ref | Expected result | Actual result | Redaction proof | Freshness | Status | Gap / mitigation |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| FR-1 Immutable Document API | Master PRD | Epic 1, Stories 1.1-1.5 | Milestone issues 250-256 plus related closed issues | `_bmad-output/implementation-artifacts/epic-1-evidence-rollup.md` and Story 1.1-1.5 artifacts record focused tests and `make check`. | Local/package/integration | 2026-06-11 evidence rollup; source artifacts record commands. | Immutable write/read/head/find behavior remains covered. | Runtime evidence PASS; Epic 1 overall CONCERNS due issue linkage hygiene. | Epic 1 leak-scan rows and rollup privacy notes. | Current but tracker linkage scoped. | CONCERNS | Keep issue/PR trace explicit in final closure. |
| FR-2 ACK from local replicated durability | Master PRD | Epic 1 | Milestone issues 250-256 plus related closed issues | Story 1.1 and Epic 1 rollup. | Local/package/integration | 2026-06-11 rollup. | ACK only after durability/visibility requirements. | PASS at runtime evidence scope. | Story 1.1 and Epic 1 redaction evidence. | Current feature evidence. | PASS | Re-link to final release issue/PR evidence before Story 6.7. |
| FR-3 All-or-error reads and corruption handling | Master PRD | Epic 1, Epic 3 | Closed milestone issues; see issue snapshot. | Epic 1 rollup and Epic 3 restore/failure closure artifacts. | Local/package/integration | 2026-06-11/12 artifacts. | Read paths fail closed and never return partial/corrupt bytes. | PASS for feature evidence; some cold-read rows remain scoped. | Story 1.3, Story 3.4/3.5, and Epic 3 scans. | Current with scoped cold-read limits. | CONCERNS | Final closure must keep Epic 3 scoped concerns visible. |
| FR-4 Raft and peer replication authority | Master PRD | Epic 2 | Milestone issues 257-274 and related closed issues | `_bmad-output/implementation-artifacts/epic-2-multi-shard-evidence.md`. | Local/package/prod-like | 2026-06-11 artifact. | Raft/peer authority and wrong-Shard denial are covered. | CONCERNS: multi-Shard restart and wrong-Shard denial pass, but true multi-Shard scrub/rebuild wire identity remains scoped. | Epic 2 redaction scan. | Current but scoped. | CONCERNS | Keep Shard-scoped scrub/rebuild gap explicit if final closure still depends on it. |
| FR-5 Multi-Shard startup and routing | Master PRD / ADR 0026 | Epic 2 | Closed milestone issues for Epic 2 | Epic 2 multi-Shard evidence closure. | Local/package/prod-like | 2026-06-11 artifact. | Deterministic routing, invalid startup failure, non-zero Shard Backend behavior. | CONCERNS with documented rebuild/scrub scope limitation. | Epic 2 scan. | Current but scoped. | CONCERNS | Re-evaluate before Story 6.7; do not silently PASS ADR 0026 from routing tests alone. |
| FR-6 Backend upload and upload pressure | Master PRD | Epic 3 Stories 3.1-3.2 | Closed milestone issues 430-432 and earlier Phase 3 issues | Epic 3 upload confirmation and upload pressure artifacts; Epic 3 closure. | Local/package/LocalStack | 2026-06-12 closure. | Upload confirmation, outbox, pressure, and admission safety. | CONCERNS: package proof current; deployed and real S3/IAM final proof not claimed. | Epic 3 scans classify Backend key and credential matches. | Current but local/prod-like scoped. | CONCERNS | Story 6.6 must link real S3/IAM evidence for Backend claims. |
| FR-7 Partial local eviction and full-Block restore | Master PRD / ADR 0016-0018 | Epic 3 Stories 3.3-3.7 | Closed milestone Phase 4 issues | Epic 3 local eviction, restore-first, restore-failure, and closure artifacts. | Local/package | 2026-06-12 closure. | Eviction, retained metadata, restore, failure semantics. | CONCERNS: P0 local/package proof current; deployed multi-Member/all-copy proof not claimed. | Epic 3 leak scans. | Current but scoped. | CONCERNS | Carry local/deployed distinction into Story 6.5/6.7. |
| FR-8 Phase 5 restore-first cold reads | Master PRD / ADR 0027 | Epic 3 | Closed milestone Phase 4/5-related issues | Epic 3 restore-first and closure artifacts. | Local/package | 2026-06-12 closure. | Restore-first cold reads, no Backend inventory authority, typed failures. | CONCERNS: local/package P0 evidence exists; final release proof remains scoped. | Epic 3 Backend authority and leak scans. | Current but scoped. | CONCERNS | Story 6.5 should decide whether Tier 2/Tier 3 reruns are needed for final PASS. |
| FR-9 Production security mode and surface boundaries | Master PRD / ADR 0019, 0024 | Epic 4 Stories 4.1-4.7 | Closed milestone issues 399, 401-404, 408, 420, 421 | Epic 4 production security closure. | Local production-security rehearsal | 2026-06-12 Story 4.7 artifact. | Production mode, mTLS, authz, audit, rate limits, pprof/test-hook gates. | PASS for local production-security rehearsal; final release still needs linked evidence bundle and policy closure. | Production rehearsal report redaction proof. | Current local production-security proof. | CONCERNS | Story 6.5/6.7 must link final evidence; do not treat filesystem Backend rehearsal as real S3/IAM. |
| FR-10 OpenBao envelope encryption and durable rewrap | Master PRD / ADR 0020-0021, 0023 | Epic 4 | Closed milestone issues 400, 405-407, 408 | Epic 4 encryption, rewrap, and production security closure artifacts. | Local/package/OpenBao testcontainer/local production-security rehearsal | 2026-06-12 Story 4.7 artifact. | Encrypted write/read, Transit failures, durable rewrap, no key leakage. | PASS for local/security scope; production OpenBao ownership remains platform scope. | Epic 4 scans and rehearsal redaction report. | Current but scoped. | CONCERNS | Final release docs must preserve OpenBao deployment ownership boundary. |
| FR-11 Async Content Scanner | Master PRD / ADR 0008 | Epic 5 Stories 5.1-5.2 and 5.7 | Not in milestone issue set; BMAD artifacts own current slice evidence. | Epic 5 content scanner and closure artifacts. | Local/package | 2026-06-12 Story 5.7 artifact. | Post-ACK scanner scheduling, outage visibility, watermarks, rescan. | PASS in Epic 5 closure. | Story 5.7 scanner-sensitive and auxiliary scans. | Current feature evidence. | PASS | Keep exact proof names in final release matrix. |
| FR-12 Content Quarantine read gate and admin operations | Master PRD / ADR 0025 | Epic 5 Stories 5.3-5.7 | Not in milestone issue set; BMAD artifacts own current slice evidence. | Epic 5 Content Quarantine, admin, `scrapctl`, and closure artifacts. | Local/package | 2026-06-12 Story 5.7 artifact. | Raft-owned quarantine, read denial, metadata scan status, confirm/release, CLI workflow. | PASS in Epic 5 closure. | Story 5.7 row-level leak classifications. | Current feature evidence. | PASS | Final Story 6.7 should include Epic 5 closure artifact. |
| FR-13 `scrapctl` operational baseline | Master PRD / ADR 0015 and Epic 4/5 artifacts | Epics 2-5 plus Story 6.4 | Closed milestone issues plus BMAD artifacts. | Existing `scrapctl` story/evidence artifacts; future release bundle Story 6.4. | Local/package/prod-like | Existing artifacts through 2026-06-12. | Operator diagnostics and workflows are evidence-backed. | CONCERNS: operational commands exist, but release evidence bundle is not created yet. | Existing CLI redaction tests and story scans. | Current command evidence; bundle missing. | CONCERNS | Story 6.4 owns release evidence bundle. |
| FR-14 `scrapctl` OpenBao bootstrap | Master PRD / ADR 0023 / DG-4 | Epic 4 Stories 4.5-4.7 | Closed milestone issues 405, 408, 420, 421 | Epic 4 bootstrap and production security closure artifacts. | Local/prod-like/OpenBao testcontainer | 2026-06-12 Story 4.7 artifact. | Fresh/idempotent bootstrap and incompatible-state failure handling. | PASS for bootstrap scope; final release bundle still missing. | Story 4.5/4.6/4.7 redaction evidence. | Current feature evidence. | CONCERNS | Link in Story 6.4 bundle and final Story 6.7 gate. |
| FR-15 OTel evidence plane | Master PRD / ADR 0012-0013 | Earlier telemetry slices plus Story 6.5 | Closed issues 312-318 are in milestone; evidence gate workflow exists. | `.github/workflows/evidence-gate.yml`; `make tier3-evidence-up`; existing evidence stack docs. | Tier 3 evidence Cell / GitHub Actions | Current final Tier 3 run not linked in this artifact. | Metrics/logs/traces/profiles prove runtime behavior and evidence gates. | CONCERNS: workflow/targets exist, but current final Tier 3 evidence bundle is not linked. | Evidence bundle redaction checks are expected by Story 6.5. | Missing final current run. | CONCERNS | Story 6.5 must link green Tier 3 evidence and artifact path. |
| FR-16 Major-release evidence and documentation closure | Master PRD / DG-5 | Epic 6 Stories 6.1-6.7 | Issue `#429` plus future Epic 6 artifacts. | This matrix; future runbooks, alert/query refs, bundle, Tier 2/Tier 3 gates, S3/IAM, final closure policy. | Release evidence/docs | 2026-06-12 matrix generation. | Every release claim has current linked evidence and redaction proof. | FAIL: required Epic 6 outputs are not complete; issue `#429` is open. | This artifact includes leak-scan plan; final redaction proof pending this story verification. | Missing final release evidence. | FAIL | Complete Stories 6.2-6.7 and close/link issue `#429` before release PASS. |

## ADR Gate Matrix

| ADR | Decision gate | Evidence artifact or command | Status | Gap / mitigation |
| --- | --- | --- | --- | --- |
| ADR-0001 | Document bytes stay out of Raft. | Epic 1 and Epic 2 story artifacts; proto/Raft contract tests. | PASS | Keep in final regression evidence. |
| ADR-0002 | CRC-32C per Frame and SHA-256 per Document. | Epic 1 verified read/corruption evidence; Epic 3 restore verification. | PASS | None at feature scope. |
| ADR-0003 | Mirror Block layout across replicas. | Epic 1/2 Block and replication evidence. | PASS | None at feature scope. |
| ADR-0004 | Lean Pebble Projection with metadata tiering. | Epic 1 Projection Resolution and Epic 3/5 Projection evidence. | PASS | None at feature scope. |
| ADR-0005 | Phase 1 spike-store boundary remains a boundary. | Project context and package-boundary checks. | PASS | Keep production packages free of `internal/spike`. |
| ADR-0006 | Build system and CI structure. | `make check`, `make proto-check`, GitHub workflow files. | PASS | Final release should link current CI runs. |
| ADR-0007 | Custom ULID implementation. | Closed milestone issues and package tests. | PASS | None currently identified. |
| ADR-0008 | Async content scanning architecture. | Epic 5 scanner/quarantine closure. | PASS | ADR 0025 amends admin surface. |
| ADR-0009 | Backend object key format. | Epic 2 non-zero Shard Backend evidence; Epic 3 upload/restore evidence. | CONCERNS | Real S3/IAM final proof remains issue `#429`. |
| ADR-0010 | Upload Outbox via Raft. | Epic 3 upload confirmation and pressure evidence. | CONCERNS | Deployed/final release gate evidence still needs Story 6.5/6.7 linkage. |
| ADR-0011 | Pebble Projection key prefixes. | Epic 1/3/5 Projection tests and evidence artifacts. | PASS | None at feature scope. |
| ADR-0012 | OTel evidence plane. | Evidence workflow/targets and prior telemetry tests. | CONCERNS | Current Tier 3 evidence bundle not linked; Story 6.5 owns it. |
| ADR-0013 | Trace context through Raft log. | Prior trace/evidence slices and Raft metadata tests. | CONCERNS | Final Tier 3 evidence should include trace proof. |
| ADR-0014 | Projection Resolution boundary. | Epic 1 restart/rebuild and corruption evidence. | PASS | None at feature scope. |
| ADR-0015 | Prod-like Kind Cell CNI and gates. | `prodlike-e2e.yml`, `ci.yml`, `make tier2-e2e-up`, Epic 2/4 evidence. | CONCERNS | Current final Tier 2 GitHub Actions proof not linked by Story 6.1. |
| ADR-0016 | Phase 4 partial local eviction boundary. | Epic 3 local eviction and closure artifacts. | CONCERNS | Local/package proof current; final deployed/readiness proof remains scoped. |
| ADR-0017 | Local Block Lifecycle module. | Epic 3 lifecycle/eviction/restore evidence. | CONCERNS | Same scoped final-readiness concern as FR-7. |
| ADR-0018 | Eviction Campaign module. | Epic 3 eviction campaign evidence. | CONCERNS | Release bundle and final gates not yet linked. |
| ADR-0019 | Production security boundary. | Epic 4 production security closure. | CONCERNS | Local production-security rehearsal passes; final release evidence bundle and policy closure pending. |
| ADR-0020 | OpenBao envelope encryption contract. | Epic 4 encryption and production security closure. | CONCERNS | Production OpenBao lifecycle is platform-owned; final docs must preserve boundary. |
| ADR-0021 | Durable rewrap Raft command. | Epic 4 durable rewrap evidence. | PASS | Link in final release bundle. |
| ADR-0022 | Testcontainers integration fixtures. | Integration tests and `make check`. | PASS | None currently identified. |
| ADR-0023 | OpenBao API client boundary. | Epic 4 bootstrap and OpenBao client evidence. | PASS | Link in final release bundle. |
| ADR-0024 | Production topology and peer scope policy. | Epic 2/4 peer scope evidence. | CONCERNS | Final topology/runbook docs still pending Story 6.2/6.7. |
| ADR-0025 | Content Quarantine admin surface. | Epic 5 admin HTTP plus `scrapctl` closure. | PASS | No new admin gRPC surface added. |
| ADR-0026 | Multi-Shard V2 release boundary. | Epic 2 multi-Shard evidence. | CONCERNS | Multi-Shard scrub/rebuild wire identity remains scoped; final closure must address or accept explicitly. |
| ADR-0027 | Phase 5 restore-first cold reads. | Epic 3 restore-first closure. | CONCERNS | Local/package evidence is current; final Tier evidence remains pending. |

## Story Status Matrix

| Epic | Sprint status | Closure/evidence artifact | Release status | Gap / mitigation |
| --- | --- | --- | --- | --- |
| Epic 1 | `in-progress`; Stories 1.1-1.5 `done` | `_bmad-output/implementation-artifacts/epic-1-evidence-rollup.md` | CONCERNS | Runtime evidence PASS; tracker linkage concern remains. |
| Epic 2 | `in-progress`; Stories 2.1-2.6 `done` | `_bmad-output/implementation-artifacts/epic-2-multi-shard-evidence.md` | CONCERNS | Scoped multi-Shard rebuild/scrub concern. |
| Epic 3 | `in-progress`; Stories 3.1-3.7 `done` | `_bmad-output/implementation-artifacts/epic-3-backend-durability-cold-read-closure-evidence.md` | CONCERNS | Local/package and real S3/IAM scope concerns. |
| Epic 4 | `in-progress`; Stories 4.1-4.7 `done` | `_bmad-output/implementation-artifacts/epic-4-production-security-rehearsal-closure-evidence.md` | CONCERNS | Local production-security PASS; real S3/IAM final gate remains. |
| Epic 5 | `in-progress`; Stories 5.1-5.7 `done` | `_bmad-output/implementation-artifacts/epic-5-content-safety-closure-evidence.md` | PASS | No P0 content-safety gap open. |
| Epic 6 | `in-progress`; Story 6.1 `review`; Stories 6.2-6.7 `backlog` | This artifact; future Epic 6 artifacts | FAIL | Release docs/evidence gates incomplete. |

## GitHub Issue Snapshot

| Issue set | Query | Count / state | Release status | Gap / mitigation |
| --- | --- | --- | --- | --- |
| `storage-gateway-v2` milestone | `gh issue list --repo petabytecl/scrap --milestone storage-gateway-v2 --state all --json number,title,state,labels,milestone,url,updatedAt --limit 200` | 104 issues returned; 104 closed; 0 open. | CONCERNS | Closed milestone issues are progress evidence, not release closure. |
| Real S3/IAM gate | `gh issue view 429 --repo petabytecl/scrap --json number,title,state,body,comments,labels,milestone,url,updatedAt` | `OPEN`; labels `ready-for-human,production-readiness,v2,e2e`; milestone `NONE`. | FAIL | Issue `#429` must be linked into Story 6.6/6.7 and completed before release PASS. |

Milestone issue numbers represented by the live query:

```text
250,251,252,253,254,255,256,257,258,259,260,261,262,263,264,265,266,267,268,269,270,271,272,273,274,275,276,277,278,279,280,281,282,283,284,285,286,287,288,289,290,291,292,293,301,302,303,304,305,306,307,308,312,313,314,315,316,317,318,327,328,329,330,331,332,333,334,335,336,337,350,351,353,355,356,357,358,359,372,373,374,375,376,377,378,379,380,381,398,399,400,401,402,403,404,405,406,407,408,420,421,430,431,432
```

## Current-Run Verification

| Gate | Command | Result |
| --- | --- | --- |
| Whitespace diff check | `git diff --check` | PASS |
| Proto compatibility | `make proto-check` | PASS |
| E2E gate policy | `scripts/check-e2e-gates.sh` | PASS |
| Broad local gate | `env GOCACHE=/tmp/scrap-v2-go-build make check` | PASS |
| Secret shape scan | `rg -n --pcre2 "$secret_shape_pattern" $scan_scope` | PASS - no matches |
| Release-sensitive scan | `rg -n --pcre2 "$release_sensitive_pattern" $scan_scope` | PASS with classified safe matches |

The release-sensitive scan should cover shaped credentials, private-key blocks,
raw Document identifiers, Backend keys, trace/request IDs, file paths, auth
claims, raw logs, token values, generated certificate material, OpenBao
initialization data, Document payloads, and raw Backend object keys.

Release-sensitive scan matches:

| Locations | Classification |
| --- | --- |
| `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md:160`; `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md:161`; `_bmad-output/implementation-artifacts/6-1-v2-release-evidence-matrix.md:52`; `_bmad-output/implementation-artifacts/6-1-v2-release-evidence-matrix.md:123` | Negative requirement prose and redaction instructions only. No actual sensitive values are present. |

## Final Notes

- This artifact intentionally marks current V2 release readiness as `FAIL`.
- That `FAIL` is the correct Story 6.1 result because missing final evidence is
  visible, owned, and not silently converted into `PASS`.
- No production behavior gap is fixed or hidden by this artifact.
- Epic 6 remains in progress until Stories 6.2 through 6.7 complete and code
  review accepts the final closure decision.
