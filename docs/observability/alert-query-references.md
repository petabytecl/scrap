# SCRAP Alert and Query References

Status: current for Story 6.3

Use this reference to connect SCRAP release risks to low-cardinality,
redacted observability signals. It is a release-facing index, not an alert
deployment manifest, notification-routing policy, or dashboard pack.

The existing evidence query pack remains in
`deploy/kustomize/components/evidence-stack/queries.md`. This document adds
operator triage references, runbook links, and explicit gap rows for release
risks that do not yet have dedicated telemetry.

## Query Conventions

- PromQL examples use the Prometheus-normalized names exported from the OTel
  instruments. For OTel counter names that already end in `.total`, the
  exporter exposes a `_total_total` counter family; the source metric names
  are recorded in the evidence artifact.
- Rate expressions use a `[5m]` window to match the evidence query pack and
  the current OTel export cadence.
- Alert labels must stay stable and bounded. Use labels for routing dimensions
  such as `severity`, `service`, `scrap_cell_id`, and `scrap_shard_id`.
- Put changing values in annotations such as `summary`, `description`, and
  `runbook_url`, not in labels.
- Do not add raw Document identifiers, Backend object keys, credential values,
  trace identifiers, request identifiers, auth claims, peer addresses, host
  paths, or dependency error strings to alert labels, log fields, public
  evidence, or screenshots.

Status values:

- `PASS` means implemented telemetry and an operator response path exist.
- `CONCERNS` means the release risk is covered by a safe operator reference,
  but a dedicated metric/query, runbook link, or final evidence owner is
  missing.
- `FAIL` means the reference is missing or would require invented telemetry.

## Release-Risk Matrix

| ID | Release risk | What happened | Confirm with | Operator next action | Runbook | Status |
| --- | --- | --- | --- | --- | --- | --- |
| AQR-001 | Public availability | Public Document RPCs are failing, unavailable, or slow. | Public RPC request/error rate and p99 latency. | Confirm production readiness first, then collect an evidence bundle for the affected scenario. | [Startup/security readiness](../runbooks/startup-security-readiness.md), [Evidence collection](../runbooks/evidence-collection.md) | PASS |
| AQR-002 | Peer availability | Peer traffic, Shard routing, or peer authorization appears degraded. | Shard leader/apply metrics, authorization-denial metrics, and `scrapctl peers`. Dedicated peer RPC availability telemetry is not implemented. | Use the multi-Shard routing runbook and preserve the gap as release evidence. | [Multi-Shard routing health](../runbooks/multi-shard-routing-health.md) | CONCERNS |
| AQR-003 | Admin availability | Operator status or control-plane evidence collection is unavailable. | `scrapctl status`, `scrapctl doctor`, production security rehearsal output, and admin runbook evidence. Dedicated admin HTTP request telemetry is not implemented. | Treat as an operator-readiness issue and do not claim a metrics-backed admin alert. | [Startup/security readiness](../runbooks/startup-security-readiness.md), [Evidence collection](../runbooks/evidence-collection.md) | CONCERNS |
| AQR-004 | Write ACK latency | `WriteDocument` ACKs are slower than the release target or a write stage is saturated. | RPC p99 latency, `scrap.write.stage.duration`, and TraceQL write/apply spans. | Identify the slow stage, then collect bounded write-path evidence before changing load or admission settings. | [Evidence collection](../runbooks/evidence-collection.md), [Backend upload pressure](../runbooks/backend-upload-pressure.md) | PASS |
| AQR-005 | Read failures | Public read/head/find requests are failing or returning non-OK gRPC status. | Public RPC error rate by method/status and related traces. | Distinguish normal not-found/client errors from restore, quarantine, or authority failures. | [Restore failures](../runbooks/restore-failures.md), [Block Quarantine repair](../runbooks/block-quarantine-repair.md), [Content Quarantine response](../runbooks/content-quarantine-response.md) | PASS |
| AQR-006 | Restore failures | A cold read or explicit restore path cannot restore a required Block. | Restore counters, restore duration, restore-failed Block gauges, and Backend upload status. | Follow the restore runbook; do not publish partial bytes or use Backend inventory as authority. | [Restore failures](../runbooks/restore-failures.md) | PASS |
| AQR-007 | Backend upload lag | Sealed Blocks are not uploading fast enough or pending upload bytes/blocks keep growing. | Upload pending bytes/blocks, upload result rate, upload duration, and verify results. | Check Backend dependency health, upload outbox state, and preserve evidence before forcing retries. | [Backend upload pressure](../runbooks/backend-upload-pressure.md) | PASS |
| AQR-008 | Upload pressure | Admission pressure is near or at critical because pending upload work exceeds budget. | `scrap.upload.pressure_level`, pending bytes/blocks, and RESOURCE_EXHAUSTED RPC status. | Protect write ACK safety; reduce load or repair Backend dependency before treating pressure as healthy. | [Backend upload pressure](../runbooks/backend-upload-pressure.md) | PASS |
| AQR-009 | Block Quarantine and scrub | Deep Scrub found corrupt Block data or Block Quarantine is active. | Deep Scrub counters, quarantine gauges, and scrub progress. | Follow Block Quarantine repair; affected Documents must fail closed until repair completes. | [Block Quarantine repair](../runbooks/block-quarantine-repair.md) | PASS |
| AQR-010 | Content Scanner lag or outage | Scanner work is lagging, unavailable, failing, or duplicating schedules. | Content Scanner run/block/failure/lag/engine-unavailable metrics. | Keep scanning asynchronous; route engine outage, lag, failures, or flagged Documents through Content Quarantine response. | [Content Quarantine response](../runbooks/content-quarantine-response.md) | PASS |
| AQR-011 | Content Quarantine state | Operators need to inspect, confirm, release, or prove Content Quarantine state. | Content Scanner metrics and `scrapctl quarantine` evidence. A dedicated Content Quarantine count metric is not implemented. | Use admin and `scrapctl` quarantine workflows; record the metric gap as `CONCERNS`. | [Content Quarantine response](../runbooks/content-quarantine-response.md) | CONCERNS |
| AQR-012 | Transit outage | OpenBao Transit is unavailable or encryption fails closed. | Production security rehearsal, bootstrap evidence, and startup readiness output. A direct runtime Transit outage metric is not implemented. | Leave production encryption failed closed and escalate to the platform OpenBao owner. | [OpenBao Transit dependency](../runbooks/openbao-transit-dependency.md), [Startup/security readiness](../runbooks/startup-security-readiness.md) | CONCERNS |
| AQR-013 | Audit sink failure | Production security evidence cannot prove audit delivery. | Production security readiness and rehearsal evidence. A direct audit sink failure metric is not implemented. | Do not claim final release readiness until audit evidence is current and linked. | [Startup/security readiness](../runbooks/startup-security-readiness.md) | CONCERNS |
| AQR-014 | Rate-limit or authorization denials | A surface is denying requests due to rate limits or authorization policy. | `scrap.security.rate_limit.denials` and `scrap.security.authorization.denials`. | Check whether denials match expected policy; escalate unexpected denials without exposing claims. | [Startup/security readiness](../runbooks/startup-security-readiness.md), [Multi-Shard routing health](../runbooks/multi-shard-routing-health.md) | PASS |
| AQR-015 | Shard leader and apply health | A Shard has no leader, conflicting leaders, or stalled apply progress. | `scrap.raft.is_leader`, leader ID, apply index, commit index, and apply lag. | Use Shard routing diagnostics before treating local files, hostnames, or Backend objects as authority. | [Multi-Shard routing health](../runbooks/multi-shard-routing-health.md) | PASS |
| AQR-016 | Evidence leak-scan status | Release evidence may contain sensitive identifiers or unredacted operational output. | Story evidence scans and future release bundle/Tier gate scans. A live evidence leak metric is not implemented. | Keep release status at `CONCERNS` until Story 6.4 and Story 6.5 produce final bundle/gate evidence. | [Evidence collection](../runbooks/evidence-collection.md) | CONCERNS |

## PromQL References

### Public RPC Availability and Latency

```promql
# Request rate by public Document method and gRPC status.
sum(rate(scrap_rpc_server_requests_total{rpc_service="scrap.v1.DocumentService"}[5m])) by (rpc_method, rpc_grpc_status_code)

# Server/dependency failure ratio across public RPCs. Evaluate the ratio only
# when request rate is non-zero; keep expected client/status outcomes separate.
sum(rate(scrap_rpc_server_requests_total{rpc_service="scrap.v1.DocumentService"}[5m])) > 0
and
sum(rate(scrap_rpc_server_requests_total{rpc_service="scrap.v1.DocumentService",rpc_grpc_status_code=~"2|4|13|14|15"}[5m])) /
clamp_min(sum(rate(scrap_rpc_server_requests_total{rpc_service="scrap.v1.DocumentService"}[5m])), 0.001)

# Non-OK investigation by public method and status.
sum(rate(scrap_rpc_server_requests_total{rpc_service="scrap.v1.DocumentService",rpc_grpc_status_code!="0"}[5m])) by (rpc_method, rpc_grpc_status_code)

# p99 request latency by method.
histogram_quantile(0.99, sum(rate(scrap_rpc_server_duration_seconds_bucket{rpc_service="scrap.v1.DocumentService"}[5m])) by (le, rpc_method))
```

### Write ACK Latency

```promql
# p99 write-stage latency by bounded stage name.
histogram_quantile(0.99, sum(rate(scrap_write_stage_duration_seconds_bucket[5m])) by (le, scrap_write_stage))

# Average write-stage latency by bounded stage name.
sum(rate(scrap_write_stage_duration_seconds_count[5m])) by (scrap_write_stage) > 0
and
sum(rate(scrap_write_stage_duration_seconds_sum[5m])) by (scrap_write_stage) /
clamp_min(sum(rate(scrap_write_stage_duration_seconds_count[5m])) by (scrap_write_stage), 0.001)
```

### Read and Restore Failures

```promql
# Public read/head/find failures by bounded method and status.
sum(rate(scrap_rpc_server_requests_total{rpc_service="scrap.v1.DocumentService",rpc_method=~"ReadDocument|HeadDocument|FindDocuments",rpc_grpc_status_code!="0"}[5m])) by (rpc_method, rpc_grpc_status_code)

# Restore operation outcomes by bounded reason, result, and failure reason.
sum(rate(scrap_eviction_restore_total_total[5m])) by (reason, result, failure_reason)

# Current restore failure inventory by bounded reason.
scrap_eviction_restore_failures_by_reason
```

### Backend Upload Lag and Pressure

```promql
# Upload lag indicators.
scrap_upload_pending_bytes
scrap_upload_pending_blocks

# Upload outcomes by bounded status.
sum(rate(scrap_upload_total_total[5m])) by (status)

# Pressure level: 0=ok, 1=warn, 2=pressure, 3=critical.
scrap_upload_pressure_level

# Backend-auth pause indicator.
scrap_upload_auth_paused
```

### Scrub, Block Quarantine, and Content Scanner

```promql
# Deep Scrub corruption and quarantine activity.
sum(rate(scrap_scrub_deep_corruptions_total[5m])) by (scrap_shard_id)
sum(rate(scrap_scrub_deep_quarantines_total[5m])) by (scrap_shard_id)
scrap_scrub_deep_blocks_quarantined
scrap_eviction_quarantined_blocks

# Scanner lag and outage signals.
scrap_avscan_lag_blocks
scrap_avscan_in_flight_blocks
sum(rate(scrap_avscan_failures_total[5m])) by (scrap_shard_id, reason)
sum(rate(scrap_avscan_engine_unavailable_total[5m])) by (scrap_shard_id)
absent_over_time(scrap_avscan_runs_total[30m])
```

### Security Denials

```promql
# Rate-limit denials by bounded surface and operation.
sum(rate(scrap_security_rate_limit_denials_total[5m])) by (scrap_surface, scrap_operation)

# Authorization denials by bounded surface, operation, and reason/status.
sum(rate(scrap_security_authorization_denials_total[5m])) by (scrap_surface, scrap_operation, scrap_reason, scrap_authorization_status)
```

### Shard Leader and Apply Health

```promql
# Leader anomaly by Shard: zero, missing, or multiple leaders are unhealthy.
sum(scrap_raft_is_leader) by (scrap_shard_id) != 1
or
absent(scrap_raft_is_leader)

# Apply lag by bounded Member/Shard identity. Positive growth means commit is
# outrunning apply on at least one reporting Member.
max by (scrap_shard_id, scrap_member_id) (scrap_raft_commit_index - scrap_raft_applied_index)
or
absent(scrap_raft_commit_index)
or
absent(scrap_raft_applied_index)
```

## TraceQL References

Use TraceQL for investigation, not for alert labels. Do not publish trace
identifiers in release evidence.

```traceql
# Slow write proposal spans.
{ span.scrap.write.stage = "raft_propose" } | span:duration > 100ms

# Apply spans across voters.
{ span:name = "scrap.apply/commit_document" }

# Upload spans for sealed Blocks.
{ span:name =~ "scrap.upload/.*" }

# Error spans.
{ span:status = error }
```

## LogQL References

LogQL references should stay bounded by service, namespace, Cell, Shard, or
surface labels. Do not parse payload fields, dependency error strings, auth
claims, or request metadata into labels.
No Story 6.3 release-risk row depends on a LogQL query. Add one only after the
log stream and bounded selectors are implemented and validated.

## Open Gaps

These gaps are intentionally recorded as `CONCERNS` rather than hidden behind
invented metrics:

- Dedicated peer RPC availability telemetry.
- Dedicated admin HTTP request/error/latency telemetry.
- Dedicated Content Quarantine inventory metric.
- Direct runtime OpenBao Transit outage metric.
- Direct audit sink failure metric.
- Live release evidence leak-scan metric before Story 6.4 and Story 6.5.
