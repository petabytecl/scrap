# Production Capacity and Compliance Signoff Inputs

Status: owner-confirmed release-profile contract for GitHub issue `#48`
Last updated: 2026-05-23

This document captures what this repository owns before S.C.R.A.P. can publish
production release artifacts, and what remains the responsibility of downstream
GitOps deployment owners.

The repository owns the release profile contract, release evidence shape,
container image build path, generated GitOps YAMLs, local human/integration
evidence, and production write ACK gates. It does not own applying manifests to
a live production cluster, choosing the final object-store account or bucket,
or approving live production capacity numbers for an environment it does not
operate.

Issue `#48` can close when this split contract, owner decisions, and explicit
deferrals are recorded. Downstream deployment facts still need accountable
evidence before any live environment can accept production traffic.

## Owner And Scope Decisions

`@cotocisternas` is the solo system owner and is accountable for the product,
operations, compliance, security, capacity, and release-owner roles for the
repo-owned release contract.

| Area | Decision | Owner | Evidence or source | Status |
| --- | --- | --- | --- | --- |
| Owner model | Solo-owner model; keep role rows separate for auditability. | `@cotocisternas` | Planning session for #48 on 2026-05-23. | Approved |
| Repository boundary | Repo produces release artifacts and release evidence; downstream GitOps applies them to real clusters. | `@cotocisternas` | Planning session for #48 on 2026-05-23. | Approved |
| Release artifact | Production release artifact is generated Kubernetes GitOps YAML plus image and evidence metadata. | `@cotocisternas` | #99 | Approved |
| Local evidence environment | Docker + kind, LocalStack S3-compatible backend, and dev/test OpenBao Transit. | `@cotocisternas` | #99 | Approved |
| Capacity sampling | Local sampling is advisory and proposes thresholds for owner approval; it must not auto-apply production settings. | `@cotocisternas` | #100 | Approved |

## Current Architecture Defaults

The ADRs define the v1 default direction. The owner has confirmed these defaults
for the repo-owned `scrap-prod-v1` release profile unless a future issue records
an explicit override.

| Area | Current v1 direction | Source | Confirmation | Status |
| --- | --- | --- | --- | --- |
| Capacity | Capacity profiles use measured deployment-specific values and fail closed when missing, invalid, or unsafe. | `docs/adr/0008-require-explicit-backend-capacity-profiles.md` | Repo-owned local evidence may propose profile values, but live deployment values remain downstream inputs. | Approved with downstream deferral |
| DR promise | V1 reports measured recovery evidence from drills and real restores, but does not make a formal business RTO/RPO promise. | `docs/adr/0009-scope-v1-disaster-recovery-to-primary-backend-rebuild.md` | No formal v1 RTO/RPO promise is required for the repo-owned release contract. | Approved |
| Secondary backend | Active secondary backend replication remains a post-v1 capability, not part of the v1 release contract. | `docs/adr/0009-scope-v1-disaster-recovery-to-primary-backend-rebuild.md` | No active secondary backend replication is required for v1 release artifacts. | Approved |
| Production ACK | Production write ACK mode stays disabled until target-profile gates prove durability, consistency, compatibility, capacity, security, and operator contracts. | `docs/adr/0012-require-production-readiness-gates-before-write-ack.md` | Production write ACK remains disabled until every gate is satisfied. | Approved |

## Release Profile Identity

| Field | Required input | Owner | Evidence or source | Status |
| --- | --- | --- | --- | --- |
| Deployment/profile ID | `scrap-prod-v1` | `@cotocisternas` | Planning session for #48 on 2026-05-23. | Approved |
| Product owner | `@cotocisternas` | `@cotocisternas` | Planning session for #48 on 2026-05-23. | Approved |
| Operations owner | `@cotocisternas` for repo-owned release flow; downstream deploy owner for live clusters. | `@cotocisternas` | Planning session for #48 on 2026-05-23. | Approved with downstream deferral |
| Compliance owner | `@cotocisternas` for repo policy; downstream deploy owner for environment-specific retention and legal approval. | `@cotocisternas` | Planning session for #48 on 2026-05-23. | Approved with downstream deferral |
| Security owner | `@cotocisternas` for repo evidence; downstream deploy owner for live OpenBao HA, auth, audit, and key custody. | `@cotocisternas` | Planning session for #48 on 2026-05-23. | Approved with downstream deferral |
| Release owner | `@cotocisternas` | `@cotocisternas` | Planning session for #48 on 2026-05-23. | Approved |
| Deployment shape | Kubernetes release manifests generated from this repo; application is GitOps-owned outside this repo. | `@cotocisternas` | #99 | Approved |
| Backend provider class | S3-compatible backend class. Provider, account/project, bucket/container, region, storage class, and restore class are downstream inputs. | `@cotocisternas` | #99 | Approved with downstream deferral |
| Local backend rehearsal | LocalStack in the kind environment. | `@cotocisternas` | #99 | Approved |
| OpenBao release rehearsal | Dev/test OpenBao pod with Transit and audit enabled in the kind environment. | `@cotocisternas` | #99, #86 | Approved for local evidence only |
| Live OpenBao deployment | Cluster identity, storage backend, HA mode, audit device, snapshot/unseal owner, and Transit key namespace for a real deployment. | Downstream deploy owner | Not owned by this repo. | Deferred |
| Storage-node class | Stateful Kubernetes members with local persistent storage assumptions; live node type, disk type, disk count, filesystem, and topology are downstream inputs. | `@cotocisternas` | `docs/adr/0006-run-authoritative-storage-as-stateful-kubernetes-members.md`, `docs/adr/0013-require-linux-local-filesystem-durability-assumptions.md`, #99 | Approved with downstream deferral |

## Capacity Inputs

The repo-owned release flow must fail closed when capacity values are missing,
invalid, or unsafe. Local evidence can sample and propose values, but live
production numbers must come from the deployment environment that will run the
release artifacts.

| Area | Required input | Owner | Evidence or source | Status |
| --- | --- | --- | --- | --- |
| Accepted sustained ingress | Sustained accepted write bytes/sec and documents/sec. | Downstream deploy owner | Proposed locally by #100; live values deferred. | Deferred |
| Accepted burst ingress | Burst bytes/sec, documents/sec, burst duration, and expected burst frequency. | Downstream deploy owner | Proposed locally by #100; live values deferred. | Deferred |
| Document shape | P50/P95/P99 document size, max supported size, file types, and expected document count/day. | Downstream deploy owner | Proposed locally by #100; live values deferred. | Deferred |
| Local disk capacity | Usable bytes per storage member after filesystem, reserve, and replication overhead. | Downstream deploy owner | Proposed locally by #100; live values deferred. | Deferred |
| Local disk performance | Write throughput, read throughput, fsync/fdatasync latency, and failure-mode assumptions. | Downstream deploy owner | Proposed locally by #100; live values deferred. | Deferred |
| Storage member count | Number of eligible storage nodes and placement labels for the target environment. | Downstream deploy owner | GitOps manifest shape from #99; live values deferred. | Deferred |
| Durability window | Required local/peer retention window while backend upload is delayed or degraded. | Downstream deploy owner | Proposed locally by #100; live values deferred. | Deferred |
| Disk guard bands | Admission, warning, critical, and fail-closed thresholds. | Downstream deploy owner | Proposed locally by #100; live values deferred. | Deferred |
| Backend write budget | Upload bytes/sec, object puts/sec, multipart limits, request ramp policy, and throttling behavior. | Downstream deploy owner | Proposed locally by #100; live values deferred. | Deferred |
| Backend read/restore budget | Restore bytes/sec, gets/sec, range-read behavior, archive restore latency, and restore throttles. | Downstream deploy owner | Proposed locally by #100; live values deferred. | Deferred |
| Backend durability profile | Versioning, object lock if used, lifecycle policy, checksum support, and regional durability assumptions. | Downstream deploy owner | Not owned by this repo. | Deferred |
| Lane budgets | Minimum and maximum capacity for ingest upload, repair, restore, DR copy, rewrap, and scrub lanes. | Downstream deploy owner | Proposed locally by #100; live values deferred. | Deferred |
| Circuit breakers | Backend, disk, OpenBao, and operation-backlog breaker thresholds and recovery policy. | Downstream deploy owner | Proposed locally by #100; live values deferred. | Deferred |
| OpenBao throughput | Transit wrap, unwrap, and rewrap throughput and latency under expected concurrency. | Downstream deploy owner | Proposed locally by #100 and local smoke from #86; live values deferred. | Deferred |
| OpenBao availability | HA topology, backup/snapshot cadence, unseal/recovery procedure, tested restore time, and audit-device capacity. | Downstream deploy owner | Not owned by this repo. | Deferred |

## Compliance Inputs

Legal hold is required for v1 planning. Exact retention periods, hold authority,
and environment-specific legal approvals remain explicit downstream inputs.

| Area | Required input | Owner | Evidence or source | Status |
| --- | --- | --- | --- | --- |
| Legal hold required | Legal hold applies to immutable documents, backend artifacts, authoritative metadata, published metadata, manifests, tombstones, and repair history. | `@cotocisternas` | Planning session for #48 on 2026-05-23. | Approved |
| Legal hold authority | Who can place, release, and audit a hold. | Downstream deploy owner | Not owned by this repo. | Deferred |
| Document retention | Required minimum and maximum retention for immutable documents and backend artifacts. | Downstream deploy owner | Not owned by this repo. | Deferred |
| Metadata retention | Required retention for authoritative metadata, published metadata, manifests, tombstones, and repair history. | Downstream deploy owner | Not owned by this repo. | Deferred |
| Audit evidence retention | Required retention for admin actions, denied requests, critical successful actions, OpenBao audit records, release evidence, and DR drill evidence. | Downstream deploy owner | Not owned by this repo. | Deferred |
| Corrupt-byte evidence | Whether corrupt bytes must be retained, quarantined, sampled, summarized as metadata-only evidence, or destroyed under policy. | Downstream deploy owner | Not owned by this repo. | Deferred |
| Immutability requirement | Whether WORM/object-lock, signed evidence, external audit export, or other tamper-evidence is required. | Downstream deploy owner | Not owned by this repo. | Deferred |
| Sensitive data limits | Logs, traces, audit records, metrics, support bundles, and local evidence must not include document bytes, plaintext DEKs, wrapped DEKs, OpenBao tokens, backend credentials, or customer payloads. | `@cotocisternas` | Existing evidence artifact rules and OpenBao smoke coverage. | Approved |

## Product And Operations Decisions

| Decision | Current default | Required answer | Approver | Evidence or source | Status |
| --- | --- | --- | --- | --- | --- |
| Does v1 avoid formal business RTO/RPO promises? | Yes. V1 reports measured DR evidence only. | Yes | `@cotocisternas` | Planning session for #48 on 2026-05-23. | Approved |
| Does active secondary backend replication remain post-v1? | Yes. Primary-backend rebuild is the v1 DR scope. | Yes; active secondary backend replication is post-v1. | `@cotocisternas` | Planning session for #48 on 2026-05-23. | Approved |
| Is the first target release allowed without cross-region or cross-cloud failover? | Yes, if product and operations accept measured DR evidence only. | Yes for repo-owned release artifacts. | `@cotocisternas` | Planning session for #48 on 2026-05-23. | Approved |
| Are legal-hold and audit-retention requirements satisfied by planned policies? | Unknown until downstream retention inputs are filled. | Legal hold required; exact retention deferred. | `@cotocisternas` | Planning session for #48 on 2026-05-23. | Approved with downstream deferral |
| Can production write ACK mode remain disabled until every target-profile gate, release-owner signoff, and downstream deployment approval artifact is satisfied? | Yes. This is required by ADR 0012. | Yes | `@cotocisternas` | Planning session for #48 on 2026-05-23 and #89 gate implementation. | Approved with downstream deferral |

## Signoff Matrix

| Role | Required signoff | Signoff record | Date | Status |
| --- | --- | --- | --- | --- |
| Capacity owner | Confirms repo-owned local evidence is advisory and live production values are downstream deployment inputs. | `@cotocisternas` | 2026-05-23 | Approved with downstream deferral |
| Compliance owner | Confirms legal hold is required for documents and metadata, with exact retention inputs deferred to downstream deployment policy. | `@cotocisternas` | 2026-05-23 | Approved with downstream deferral |
| Product owner | Confirms v1 product promises: measured DR evidence only, no formal RTO/RPO, and no v1 active secondary backend replication. | `@cotocisternas` | 2026-05-23 | Approved |
| Operations owner | Confirms this repo owns release artifacts and local evidence, not live GitOps application or production operations. | `@cotocisternas` | 2026-05-23 | Approved |
| Security owner | Confirms local OpenBao smoke can satisfy repo-owned API/evidence shape only; live HA/key-custody approval remains downstream. | `@cotocisternas` | 2026-05-23 | Approved with downstream deferral |
| Release owner | Confirms production write ACK stays fail-closed until release gates and downstream deployment evidence are explicitly satisfied. | `@cotocisternas` | 2026-05-23 | Approved |

## Issue 48 Closing Checklist

- [x] Owner and role accountability are recorded for the repo-owned release
      contract.
- [x] The `scrap-prod-v1` release profile boundary is defined.
- [x] Product and operations owner confirmed that v1 avoids formal RTO/RPO
      promises.
- [x] Product and operations owner confirmed that active secondary backend
      replication remains post-v1.
- [x] Compliance owner confirmed legal hold is required for documents and
      metadata.
- [x] Live deployment capacity, retention, OpenBao HA, and backend-provider
      details are explicitly deferred to downstream GitOps/deployment owners.
- [x] Follow-up implementation issues exist for repo-owned release artifacts
      (#99) and advisory capacity threshold sampling (#100).
