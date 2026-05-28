# S.C.R.A.P. Evidence Query Pack

Saved queries for Phase 3 stress evidence review.

## Metrics (PromQL via Mimir)

### Client API
```promql
# Request rate by method and status
sum(rate(scrap_rpc_server_requests_total[1m])) by (rpc_method, rpc_grpc_status_code)

# p99 latency by method
histogram_quantile(0.99, sum(rate(scrap_rpc_server_duration_seconds_bucket[1m])) by (le, rpc_method))

# Error rate
sum(rate(scrap_rpc_server_requests_total{rpc_grpc_status_code!="0"}[5m])) / sum(rate(scrap_rpc_server_requests_total[5m]))
```

### Write Path Stages
```promql
# Average stage latency
sum(rate(scrap_write_stage_duration_seconds_sum[1m])) by (scrap_write_stage) / sum(rate(scrap_write_stage_duration_seconds_count[1m])) by (scrap_write_stage)

# p99 stage latency
histogram_quantile(0.99, sum(rate(scrap_write_stage_duration_seconds_bucket[1m])) by (le, scrap_write_stage))
```

### Upload Pipeline
```promql
# Pressure level (0=ok, 1=warn, 2=pressure, 3=critical)
scrap_upload_pressure_level

# Pending bytes and blocks
scrap_upload_pending_bytes
scrap_upload_pending_blocks

# Upload success/failure rate
sum(rate(scrap_upload_total[1m])) by (status)
```

### Raft Health
```promql
# Leader state
scrap_raft_is_leader

# Applied index growth (should increase steadily under write load)
rate(scrap_raft_applied_index[1m])
```

### Process Resources
```promql
# Goroutine count
process_runtime_go_goroutines

# Heap memory
process_runtime_go_mem_heap_alloc_bytes

# GC pause rate
rate(process_runtime_go_gc_pause_seconds_total[1m])
```

## Traces (TraceQL via Tempo)

```traceql
# Slow write operations (>100ms)
{ span.scrap.write.stage = "raft_propose" } | duration > 100ms

# All errors
{ status = error }

# Specific write stage traces
{ name =~ "scrap.write/.*" }

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
