# OpenTelemetry evidence plane before Phase 4

Status: Accepted

Date: 2026-05-28

## Context

Phase 3 added Backend upload, upload pressure, and local stress harness work.
Phase 4 will introduce partial local eviction, so it must not begin until Phase 3
behavior is observable under sustained load. The system needs evidence about
client latency, write-path stages, Raft health, peer replication, upload lag,
scrub interference, and resource saturation.

The earlier SCRAP substrate named Prometheus (`client_golang`, service-local
registry, `/metrics`) as the observability contract. That was sufficient for
early upload and scrub counters, but it is too narrow for Phase 3 stress
evidence. Metrics alone cannot correlate slow requests with Raft proposals,
peer replication, Backend upload retries, structured logs, and runtime profiles.

S.C.R.A.P. needs a production-grade telemetry and evidence plane before Phase 4.
Alerting policy, incident workflows, and runbooks are important but are not part
of this decision.

## Decision

OpenTelemetry is the application telemetry producer contract for `scrapd`.
New application telemetry is emitted as OTLP data, not as Prometheus
`client_golang` instrumentation. The existing `/metrics` surface is a migration
target, not the long-term contract. Health endpoints are operational APIs and
remain separate from telemetry export.

The self-hosted local evidence stack is Grafana with OTLP-capable stores:
Mimir-compatible metrics storage, Loki-compatible log storage, Tempo-compatible
trace storage, and Pyroscope-compatible profiling storage. A collector layer
receives and routes telemetry, applies resource enrichment, batches exports, and
performs tail sampling for traces.

The required signals are:

- Metrics: low-cardinality OTel metrics for client RPCs, write-path stage
  latency, Raft state, peer replication, upload lag, pressure, scrub work, and
  process/runtime resources.
- Logs: `scrapd` keeps structured `slog` JSON on stdout/stderr. The collector
  reads container logs, enriches them with Kubernetes and S.C.R.A.P. resource
  attributes, and exports OTLP logs. `scrapd` must not block request handling on
  log-export backpressure.
- Traces: instrument key request and background-work paths. The collector keeps
  all error and slow traces plus a small baseline sample of normal traces.
- Profiles: Go pprof is exposed only on the admin listener, disabled by default,
  enabled explicitly for evidence environments, and restricted by NetworkPolicy
  to telemetry collectors and operators.

Telemetry must not use raw `transaction_id` or `document_name` as metric
attributes. Logs and traces use stable hashed identifiers by default. Raw
identifiers are allowed only behind an explicit local debug override and must not
be enabled in production-like evidence runs.

Each `scrapd` process emits a stable `service.instance.id` resource attribute so
replica-local metric series do not collapse into one ambiguous target. The
fallback order is explicit instance ID, Member slot identity, durable Member ID,
then `local`.

The `scrap.rpc.server.duration` histogram records seconds and uses explicit
bucket boundaries tuned for the Document service latency target:
`0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10`.
The instrument unit remains `s`, so Prometheus-compatible exporters continue to
emit `scrap_rpc_server_duration_seconds_bucket`.

Before Phase 4 begins, Phase 3 must produce a timestamped evidence bundle for
throughput, mixed read/write/head, and upload-pressure stress scenarios. The
bundle includes run configuration, load-generator output, selected metric
snapshots, log/trace/profile exports or stable query references, and pass/fail
checks for the evidence gates.

## Consequences

- `CONTEXT.md` no longer treats Prometheus `/metrics` as the locked
  observability substrate.
- Current Prometheus metrics, dashboards, and E2E assertions must be migrated to
  OTel-backed equivalents in later implementation work.
- The local stress environment becomes heavier because it includes collectors and
  telemetry stores, but the evidence it produces is durable and reviewable.
- Pprof exposure becomes a security-sensitive admin feature and must remain
  opt-in and network-restricted.
- Alert definitions, incident workflows, and operator runbooks remain deferred.
