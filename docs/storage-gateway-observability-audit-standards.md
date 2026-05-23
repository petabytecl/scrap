# Storage Gateway Observability And Audit Signal Standards

Status: planning gate for GitHub issue `#20`
Last updated: 2026-05-23

This document defines the minimum production logs, metrics, traces, audit
events, and cardinality rules for S.C.R.A.P. storage-gateway code. The goal is
to make durability, corruption, repair, authorization, and operator workflows
observable without turning high-cardinality document identity into metric
labels or leaking document bytes and secrets into evidence.

These standards are vendor-neutral. OpenTelemetry, Prometheus exposition, and
`log/slog` remain acceptable implementation substrates when they preserve the
signal contracts below.

## Signal Boundaries

`internal/observe` may provide helper types for metrics, structured logs,
traces, audit events, and cardinality review. It must not become a shortcut
dependency between unrelated storage packages, and domain packages must not
call global loggers, meters, tracers, or audit sinks directly.

Storage packages own their domain facts:

- shard/workflow packages classify write admission, visibility, repair,
  restore, and idempotency outcomes;
- blockstore packages classify local byte, checksum, fsync, and corruption
  outcomes;
- backend packages classify provider errors, throttling, upload, verify, and
  restore lag;
- crypto envelope packages classify OpenBao availability, key version, cache,
  rewrap, and crypto-unavailable outcomes;
- API/admin/authz boundaries attach request identity, workload identity,
  capability decisions, and transport status mapping.

The observability layer translates those facts into consistent signals. It
must not invent success, retryability, corruption, authorization, or durability
classifications that the owning package did not provide.

## Required Metrics

All metric names below are the required semantic contract. Implementations may
adapt exact names to the chosen instrumentation library, but review should
preserve the same concepts, units, and label limits.

Common allowed labels:

- `cell_id`, `member_id`, `deployment_profile`;
- `priority_class`, `document_class`, `operation_lane`;
- `backend_provider`, `backend_profile`;
- `source` with bounded values such as `local`, `peer`, `backend`;
- `result`, `error_class`, `reason`, `decision`;
- `raft_role`, `openbao_state`;
- `operation_type`, `admin_action`, `audit_action`.

Do not add tenant, transaction, document, request, operation, block, frame,
range, backend key, object key, trace, or idempotency identifiers as metric
labels. Those identifiers belong in logs, traces, audit records, operation
state, or sampled diagnostic artifacts.

| Area | Required metric concepts | Notes |
| --- | --- | --- |
| Write admission | admitted writes, rejected writes, throttled writes, in-flight writes, admission latency, ACK latency, retry outcome, idempotency conflict count | Label by bounded priority/document class, result, and reason. Do not label by tenant, transaction, or document. |
| Disk runway | used bytes, free bytes, reserved bytes, runway seconds, open block bytes, prepare/openlog bytes, critical reserve usage, local durability window remaining | Metrics must distinguish normal, reserve, and blocked states before hard write rejection. |
| Backend lag | upload backlog bytes, oldest upload age, per-lane queue depth and age, provider retry delay, provider error class count, backend verification lag, backend token saturation | Label by backend provider/profile and operation lane, not object key or backend prefix. |
| Repair lag | repair queue depth, oldest repair age, repair attempts, repair success/failure, quarantined source count, peer catch-up lag | Corruption-driven repair must be visible separately from capacity or transient backend repair. |
| Restore lag | restore/prewarm queue depth, oldest restore age, restore attempts, restore success/failure, restore pending responses, archive restore budget usage | Label restore/prewarm lanes separately from ordinary reads. |
| Corruption incidents | checksum mismatch count, all-sources-corrupt count, suspect source quarantines, integrity failure responses, corruption evidence records | Counts should preserve error class and source type. Detailed IDs stay in logs/audit/traces. |
| Raft health | leader availability, role, term changes, commit index lag, apply index lag, proposal failures, snapshot lag, compaction lag, membership change failures | Shard-level metrics require a bounded shard label plan before production use. Prefer aggregate health plus sampled per-shard diagnostics until then. |
| OpenBao health | Transit request latency, Transit error class count, availability state, DEK cache hit/miss, key-version lookup failures, rewrap lag, audit-device health signal | Never expose plaintext DEKs, wrapped DEKs, or OpenBao tokens. Key names/versions may appear in logs/audit where needed, not high-cardinality metric labels. |
| Fsync latency | fdatasync/fsync latency, parent-directory sync latency, sync failures, sync retry count, durability boundary duration | Label by bounded boundary type such as block append, openlog, metadata WAL, snapshot, rename, link, or delete. |
| Runtime behavior | goroutine count, heap bytes, allocation rate, GC pause/duration, file descriptors, worker queue depth, stream count, panic/recover count, shutdown duration | Runtime metrics support capacity and leak detection; they are not substitutes for domain metrics. |

## Logs And Traces

Production logs are structured records for diagnosis and support. Traces are
request/workflow timelines. Both may carry higher-cardinality identifiers that
are forbidden as metric labels.

Identifiers allowed in logs and traces:

- `request_id`, `correlation_id`, `trace_id`;
- `operation_id`, `write_attempt_id`, `client_idempotency_key` fingerprint;
- `tenant_id`, `transaction_id`, `document_id`;
- `shard_id`, `member_id`, `block_id`, `frame_index`, `range_start`,
  `range_length`;
- backend object key or provider request ID when needed for operator diagnosis;
- OpenBao key name and key version, but never tokens, plaintext DEKs, wrapped
  DEK blobs, document bytes, or plaintext secrets.

Logs and spans should record:

- request start and completion for public/admin APIs;
- typed failure class, retryability, transport status, and operator action;
- write admission decision, ACK boundary, duplicate/idempotency outcome, and
  unknown-client-outcome recovery handle;
- read source choice, restore-pending reason, and corruption fail-closed reason;
- backend upload, verify, restore, repair, scrub, rewrap, and cleanup job state
  transitions;
- policy reload attempts, rejected policies, and the active policy generation;
- degraded states such as stale leader, backend throttling, OpenBao unavailable,
  disk runway unsafe, or peer catch-up lag.

Logs and traces must use redaction helpers for fields that can carry secrets,
PII, customer document content, raw hashes where policy requires masking, or
provider credentials. Support bundles should default to metadata-only evidence.

## Audit Events

Audit events are durable security and operator evidence. They are not ordinary
debug logs. They must be written for denied requests and for successful critical
actions even when metrics and traces already exist.

Every audit event must include:

- event ID, event type, timestamp, cell ID, member ID, and service version;
- request ID, correlation ID, trace ID when present;
- workload identity, authenticated principal, capability, and policy version;
- action, resource reference, decision, result, and reason code;
- operation ID or plan ID for durable admin workflows;
- dry-run versus execute mode when an action has a plan phase;
- actor-supplied reason, TTL, and approval reference when required;
- before/after configuration generation for policy, capacity, or key changes;
- retention, legal-hold, or evidence-impact classification when relevant.

Mandatory denied-request audit events:

- authentication failure at a boundary that receives production traffic;
- authorization or capability denial for public, admin, backend, restore,
  repair, lifecycle, capacity, or key actions;
- validation denial for critical ingest, admin operation start, tombstone,
  restore, prewarm, key rotation, policy reload, and capacity override;
- rejected hot-reload authorization policy or rejected production-risk config;
- denied request that attempts to use caller metadata as a security principal.

Mandatory successful critical-action audit events:

- `CRITICAL_INGEST` writes and any future write class that changes durability or
  retention guarantees;
- successful ephemeral writes when policy requires separate retention evidence;
- explicit restore, prewarm, repair, scrub, backend verification, and DR drill
  requests;
- admin drain, member replacement, placement override, tombstone, lifecycle,
  legal hold, and capacity override workflows;
- authorization policy load/reload, production risk-mode change, and config
  changes that affect admission, retention, durability, or serving readiness;
- OpenBao key rotation, rewrap, minimum-decryption-version change, and crypto
  profile changes;
- release-gate evidence acceptance, exception approval, and production write ACK
  enablement.

Normal hot reads and ordinary writes do not require full audit events by
default. They remain observable through request logs, metrics, traces, and
immutable metadata unless a later compliance mode requires per-access audit.

## Request And Correlation IDs

API boundaries accept request and correlation IDs through gRPC metadata.
Recommended metadata keys are:

- `x-request-id` for the caller's per-request diagnostic ID;
- `x-correlation-id` for a workflow, job, or ETL transaction diagnostic ID;
- W3C `traceparent` and `tracestate` when distributed tracing is enabled.

Boundary rules:

- validate caller-provided IDs as printable ASCII, bounded length values;
- generate a request ID when one is missing;
- preserve a caller correlation ID when valid, otherwise use the request ID as
  the initial correlation ID for the request;
- never treat request ID, correlation ID, trace context, `created_by_service`,
  tenant, or priority fields as the authenticated security principal;
- propagate IDs to structured logs, traces, audit records, operation records,
  async job state, backend/OpenBao calls where safe, and client-visible response
  metadata where useful;
- keep idempotency keys separate from correlation IDs. Idempotency keys define
  retry semantics; correlation IDs define diagnosis grouping.

Background workers must continue the originating correlation ID when processing
durable operation state. If a worker creates follow-up work, it records both the
parent operation ID and the current operation ID so audit and traces can connect
the chain without metric-label cardinality growth.

## Review Checklist

Before merging production storage-gateway code that adds a workflow, endpoint,
or background job, reviewers should verify:

- Are required metric concepts emitted with bounded labels?
- Are high-cardinality identifiers kept out of metric labels?
- Do logs/traces contain enough IDs to diagnose without document bytes or
  secrets?
- Are denied requests and successful critical actions audited?
- Are request/correlation IDs accepted, generated, propagated, and kept separate
  from security identity and idempotency semantics?
- Do tests or harnesses assert the key metrics and failure artifacts for the
  behavior under review?
