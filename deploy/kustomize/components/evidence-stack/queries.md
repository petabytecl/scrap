# S.C.R.A.P. Evidence Query Pack

Saved queries for Phase 3 stress evidence review.

> **Rate window:** all `rate()` queries use a `[5m]` window. The OTel SDK
> `PeriodicReader` exports every 60s, so a `[1m]` window would often see a single
> sample and return empty. This matches the window used in `scripts/evidence-bundle.sh`.

## Dashboards

Three method-based boards are provisioned in the Evidence folder:

- **scrap-overview** — health at a glance (error rate, p99, leader, apply lag, pressure, disk).
- **scrap-red** — Rate/Errors/Duration per RPC and per write-path stage.
- **scrap-use** — Utilization/Saturation/Errors per resource (raft apply pipeline, RPC concurrency, upload outbox, local storage, runtime, scrub).

Two Kubernetes boards are also provisioned in the Evidence folder:

- **kubernetes-cluster** — kube-state-metrics health, node readiness, pod phases, allocatable capacity, PVC phases, and cAdvisor namespace resource usage.
- **kubernetes-workloads** — deployment/statefulset/daemonset readiness, failed jobs, pod readiness, restarts, waiting containers, and terminated containers.

## Metrics (PromQL via Mimir)

### Kubernetes Scraping

The OTel collector scrapes kube-state-metrics on `kube-state-metrics.monitoring.svc:8080`
and kubelet node/cAdvisor metrics through the Kubernetes API server. The scrape
jobs write to the same Mimir datasource (`uid=mimir`) used by the S.C.R.A.P.
dashboards.

```promql
# Scrape health
up{job=~"kube-state-metrics|kubernetes-nodes|kubernetes-cadvisor"}

# Node readiness
sum(kube_node_status_condition{condition="Ready",status="true"})

# Pod phase distribution
sum(kube_pod_status_phase) by (phase)

# Workload readiness gaps
sum(clamp_min(kube_deployment_spec_replicas - kube_deployment_status_replicas_ready, 0)) by (namespace, deployment)

# Recent restarts
sum(increase(kube_pod_container_status_restarts_total[5m])) by (namespace, pod, container)

# Namespace CPU/memory from cAdvisor
sum(rate(container_cpu_usage_seconds_total{container!="",pod!=""}[5m])) by (namespace)
sum(container_memory_working_set_bytes{container!="",pod!=""}) by (namespace)
```

### Client API

```promql
# Request rate by method and status
sum(rate(scrap_rpc_server_requests_total[5m])) by (rpc_method, rpc_grpc_status_code)

# p99 latency by method
histogram_quantile(0.99, sum(rate(scrap_rpc_server_duration_seconds_bucket[5m])) by (le, rpc_method))

# Error rate
sum(rate(scrap_rpc_server_requests_total{rpc_grpc_status_code!="0"}[5m])) / sum(rate(scrap_rpc_server_requests_total[5m]))
```

### Write Path Stages

```promql
# Average stage latency
sum(rate(scrap_write_stage_duration_seconds_sum[5m])) by (scrap_write_stage) / sum(rate(scrap_write_stage_duration_seconds_count[5m])) by (scrap_write_stage)

# p99 stage latency
histogram_quantile(0.99, sum(rate(scrap_write_stage_duration_seconds_bucket[5m])) by (le, scrap_write_stage))
```

### Upload Pipeline

```promql
# Pressure level (0=ok, 1=warn, 2=pressure, 3=critical)
scrap_upload_pressure_level

# Pending bytes and blocks
scrap_upload_pending_bytes
scrap_upload_pending_blocks

# Upload success/failure rate
sum(rate(scrap_upload_total[5m])) by (status)
```

### Raft Health

```promql
# Leader state
scrap_raft_is_leader

# Applied index growth (should increase steadily under write load)
rate(scrap_raft_applied_index[5m])

# Apply lag (saturation): committed entries not yet applied
max(scrap_raft_commit_index) - max(scrap_raft_applied_index)
```

### RPC Concurrency (USE)

```promql
# Utilization: RPCs currently in flight, by method
sum(scrap_rpc_server_in_flight) by (rpc_method)

# Saturation: load shed as RESOURCE_EXHAUSTED (gRPC status 8)
sum(rate(scrap_rpc_server_requests_total{rpc_grpc_status_code="8"}[5m])) by (rpc_method)
```

### Local Storage (USE)

```promql
# Disk utilization on the data volume
scrap_disk_used_bytes
scrap_disk_free_bytes

# Pebble projection on-disk size
scrap_pebble_disk_bytes
```

### Process Resources

```promql
# Goroutine count
process_runtime_go_goroutines

# Heap memory
process_runtime_go_mem_heap_alloc_bytes

# GC pause rate
rate(process_runtime_go_gc_pause_seconds_total[5m])
```

## Traces (TraceQL via Tempo)

> Two linked traces per document: `document.write` (states 1-5) and the per-Block
> `block.upload` (states 6-7). They share a deterministic block trace_id =
> hash(cell, shard, block); each write's apply span forward-links to the upload
> trace, and both carry `scrap.block_id` for cross-trace search. See ADR 0013.

```traceql
# The state-machine apply, visible on every voter
{ name =~ "scrap.apply/.*" }

# One document's apply across all replicas
{ name = "scrap.apply/commit_document" && span.scrap.block_id = 42 }

# The block upload trace (seal -> put -> verify -> confirm)
{ name =~ "scrap.upload/.*" }

# Everything for one block (writes + upload) via the shared attribute
{ span.scrap.block_id = 42 }

# Slow write operations (>100ms)
{ span.scrap.write.stage = "raft_propose" } | duration > 100ms

# All errors
{ status = error }

# RPC traces by method
{ span.rpc.method = "WriteDocument" }
```

## Phase 4 Readiness Gate Queries

```promql
# GATE: Error rate must be below 1% under sustained load
sum(rate(scrap_rpc_server_requests_total{rpc_grpc_status_code!="0"}[5m])) / sum(rate(scrap_rpc_server_requests_total[5m])) < 0.01

# GATE: Upload pressure must not reach critical during normal throughput
scrap_upload_pressure_level < 3

# GATE: Raft must maintain a leader throughout the stress run
scrap_raft_is_leader == 1
```
