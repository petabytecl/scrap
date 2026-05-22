# Production Capacity and Compliance Signoff Inputs

Status: draft input record for GitHub issue `#48`
Last updated: 2026-05-22

This document captures the deployment and compliance facts required before a
S.C.R.A.P. storage-gateway deployment can be evaluated for production write ACK
readiness.

Implementation agents must not invent these values. Issue `#48` can close only
after each required input has an accountable owner, evidence source, decision
date, and explicit approval or deferral.

## Current Architecture Defaults To Confirm

The existing ADRs define the default v1 direction, but production owners still
need to confirm that these defaults remain acceptable for the first target
deployment.

| Area | Current v1 direction | Source | Required confirmation |
| --- | --- | --- | --- |
| Capacity | Production capacity profiles use measured deployment-specific values and fail closed when missing, invalid, or unsafe. | `docs/adr/0008-require-explicit-backend-capacity-profiles.md` | Capacity owner confirms measured ingress, disk, backend, lane, guard-band, and OpenBao inputs. |
| DR promise | V1 reports measured recovery evidence from drills and real restores, but does not make a formal business RTO/RPO promise. | `docs/adr/0009-scope-v1-disaster-recovery-to-primary-backend-rebuild.md` | Product and operations owners confirm no formal v1 RTO/RPO promise is required. |
| Secondary backend | Always-on secondary backend replication remains post-v1. | `docs/adr/0009-scope-v1-disaster-recovery-to-primary-backend-rebuild.md` | Product and operations owners confirm active secondary backend replication remains post-v1. |
| Production ACK | Production write ACK mode stays disabled until target-profile readiness gates prove durability, consistency, compatibility, capacity, security, and operator contracts. | `docs/adr/0012-require-production-readiness-gates-before-write-ack.md` | Release owner confirms the target deployment has named readiness evidence. |

## Target Deployment Identity

| Field | Required input | Owner | Evidence or source | Status |
| --- | --- | --- | --- | --- |
| Deployment/profile ID | Stable name for the first target production profile. | TBD | TBD | Missing |
| Product owner | Person or group accountable for product-level promises. | TBD | TBD | Missing |
| Operations owner | Person or group accountable for operating the deployment. | TBD | TBD | Missing |
| Compliance owner | Person or group accountable for retention and legal-hold requirements. | TBD | TBD | Missing |
| Security owner | Person or group accountable for authz, key, and audit evidence review. | TBD | TBD | Missing |
| Cloud/on-prem location | Provider, region, cluster, namespace, and data-residency constraints. | TBD | TBD | Missing |
| Backend provider | Backend type, account/project, bucket/container, region, storage class, and restore class. | TBD | TBD | Missing |
| OpenBao deployment | Cluster identity, storage backend, HA mode, audit device, snapshot/unseal owner, and Transit key namespace. | TBD | TBD | Missing |
| Storage-node class | Node type, disk type, disk count, filesystem, and Kubernetes storage topology. | TBD | TBD | Missing |

## Measured Capacity Inputs

All values need units, measurement method, date measured, and the evidence link
or artifact name. Synthetic spike values are not production evidence.

| Area | Required input | Owner | Evidence or source | Status |
| --- | --- | --- | --- | --- |
| Accepted sustained ingress | Sustained accepted write bytes/sec and documents/sec. | TBD | TBD | Missing |
| Accepted burst ingress | Burst bytes/sec, documents/sec, burst duration, and expected burst frequency. | TBD | TBD | Missing |
| Document shape | P50/P95/P99 document size, max supported size, file types, and expected document count/day. | TBD | TBD | Missing |
| Local disk capacity | Usable bytes per storage member after filesystem, reserve, and replication overhead. | TBD | TBD | Missing |
| Local disk performance | Write throughput, read throughput, fsync/fdatasync latency, and failure-mode assumptions. | TBD | TBD | Missing |
| Storage member count | Number of eligible storage nodes and placement labels for the first profile. | TBD | TBD | Missing |
| Durability window | Required local/peer retention window while backend upload is delayed or degraded. | TBD | TBD | Missing |
| Disk guard bands | Admission, warning, critical, and fail-closed thresholds. | TBD | TBD | Missing |
| Backend write budget | Upload bytes/sec, object puts/sec, multipart limits, request ramp policy, and throttling behavior. | TBD | TBD | Missing |
| Backend read/restore budget | Restore bytes/sec, gets/sec, range-read behavior, archive restore latency, and restore throttles. | TBD | TBD | Missing |
| Backend durability profile | Versioning, object lock if used, lifecycle policy, checksum support, and regional durability assumptions. | TBD | TBD | Missing |
| Lane budgets | Minimum and maximum capacity for ingest upload, repair, restore, DR copy, rewrap, and scrub lanes. | TBD | TBD | Missing |
| Circuit breakers | Backend, disk, OpenBao, and operation-backlog breaker thresholds and recovery policy. | TBD | TBD | Missing |
| OpenBao throughput | Transit wrap, unwrap, and rewrap throughput and latency under expected concurrency. | TBD | TBD | Missing |
| OpenBao availability | HA topology, backup/snapshot cadence, unseal/recovery procedure, tested restore time, and audit-device capacity. | TBD | TBD | Missing |

## Compliance Inputs

These values must come from compliance or legal owners. Engineering defaults are
not enough to close issue `#48`.

| Area | Required input | Owner | Evidence or source | Status |
| --- | --- | --- | --- | --- |
| Legal hold required | Whether legal hold can apply to documents, metadata, backend artifacts, audit records, corrupt-byte evidence, or deleted/tombstoned documents. | TBD | TBD | Missing |
| Legal hold authority | Who can place, release, and audit a hold. | TBD | TBD | Missing |
| Document retention | Required minimum and maximum retention for immutable documents and backend artifacts. | TBD | TBD | Missing |
| Metadata retention | Required retention for authoritative metadata, published metadata, manifests, tombstones, and repair history. | TBD | TBD | Missing |
| Audit evidence retention | Required retention for admin actions, denied requests, critical successful actions, OpenBao audit records, release evidence, and DR drill evidence. | TBD | TBD | Missing |
| Corrupt-byte evidence | Whether corrupt bytes must be retained, quarantined, sampled, summarized as metadata-only evidence, or destroyed under policy. | TBD | TBD | Missing |
| Immutability requirement | Whether WORM/object-lock, signed evidence, external audit export, or other tamper-evidence is required. | TBD | TBD | Missing |
| Sensitive data limits | Required redaction rules for logs, traces, audit records, metrics, and support bundles. | TBD | TBD | Missing |

## Product And Operations Decisions

| Decision | Current default | Required answer | Approver | Evidence or source | Status |
| --- | --- | --- | --- | --- | --- |
| Does v1 avoid formal business RTO/RPO promises? | Yes. V1 reports measured DR evidence only. | Yes/No | TBD | TBD | Missing |
| Does active secondary backend replication remain post-v1? | Yes. Primary-backend rebuild is the v1 DR scope. | Yes/No | TBD | TBD | Missing |
| Is the first target deployment allowed to run without cross-region or cross-cloud failover? | Yes, if product and operations accept measured DR evidence only. | Yes/No | TBD | TBD | Missing |
| Are legal-hold and audit-retention requirements satisfied by the planned storage, metadata, audit, and backend policies? | Unknown until compliance inputs are filled. | Yes/No | TBD | TBD | Missing |
| Can production write ACK mode remain disabled until every target-profile gate is satisfied? | Yes. This is required by ADR 0012. | Yes/No | TBD | TBD | Missing |

## Signoff Matrix

| Role | Required signoff | Signoff record | Date | Status |
| --- | --- | --- | --- | --- |
| Capacity owner | Confirms measured capacity profile inputs are complete and safe to use for readiness evaluation. | TBD | TBD | Missing |
| Compliance owner | Confirms legal-hold and audit evidence retention requirements. | TBD | TBD | Missing |
| Product owner | Confirms v1 product promises, including measured DR evidence rather than formal RTO/RPO if accepted. | TBD | TBD | Missing |
| Operations owner | Confirms operational acceptability of the target profile, DR scope, and secondary-backend decision. | TBD | TBD | Missing |
| Security owner | Confirms OpenBao, audit, and authorization evidence requirements are represented. | TBD | TBD | Missing |
| Release owner | Confirms all signoffs are linked from the production readiness gate before production write ACK mode is enabled. | TBD | TBD | Missing |

## Issue 48 Closing Checklist

- [ ] First target deployment has measured ingress, disk, backend, and OpenBao
      capacity inputs recorded above.
- [ ] Compliance owners have confirmed legal-hold and audit evidence retention
      requirements above.
- [ ] Product and operations owners have confirmed whether v1 still avoids
      formal RTO/RPO promises.
- [ ] Product and operations owners have confirmed whether active secondary
      backend replication remains post-v1.
- [ ] The completed signoff record is linked from the production readiness gate
      evidence for issue `#15`.
