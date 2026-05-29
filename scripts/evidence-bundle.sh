#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

BUNDLE_DIR="${BUNDLE_DIR:-$REPO_ROOT/evidence}"
GRAFANA_URL="${GRAFANA_URL:-http://127.0.0.1:13000}"
# Query Mimir through Grafana's datasource proxy, addressed by stable uid (grafana-datasources.yaml).
MIMIR_PROXY="${MIMIR_PROXY:-${GRAFANA_URL%/}/api/datasources/proxy/uid/mimir}"
TEMPO_PROXY="${TEMPO_PROXY:-${GRAFANA_URL%/}/api/datasources/proxy/uid/tempo}"
LOKI_PROXY="${LOKI_PROXY:-${GRAFANA_URL%/}/api/datasources/proxy/uid/loki}"
PYROSCOPE_PROXY="${PYROSCOPE_PROXY:-${GRAFANA_URL%/}/api/datasources/proxy/uid/pyroscope}"
STRESS_ADDR="${STRESS_ADDR:-127.0.0.1:18090}"
SCENARIO="${1:-throughput}"
WORKERS="${STRESS_WORKERS:-8}"
DURATION="${STRESS_DURATION:-60s}"
DOC_SIZE="${STRESS_DOC_SIZE:-16384}"
EVIDENCE_SETTLE_SECONDS="${EVIDENCE_SETTLE_SECONDS:-75}"

TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"
GIT_SHA="$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo "unknown")"
BUNDLE_NAME="${SCENARIO}-${TIMESTAMP}-${GIT_SHA}"
BUNDLE_PATH="$BUNDLE_DIR/$BUNDLE_NAME"

usage() {
  cat <<EOF
Usage: $(basename "$0") [scenario]

Scenarios: throughput, mixed, pressure

Environment variables:
  BUNDLE_DIR       Output directory (default: ./evidence)
  GRAFANA_URL      Grafana base URL (default: http://127.0.0.1:13000)
  MIMIR_PROXY      Mimir query base URL (default: \$GRAFANA_URL's datasource
                   proxy for uid=mimir, i.e. \$GRAFANA_URL/api/datasources/proxy/uid/mimir)
  TEMPO_PROXY      Tempo query base URL (default: \$GRAFANA_URL/api/datasources/proxy/uid/tempo)
  LOKI_PROXY       Loki query base URL (default: \$GRAFANA_URL/api/datasources/proxy/uid/loki)
  PYROSCOPE_PROXY  Pyroscope query base URL (default: \$GRAFANA_URL/api/datasources/proxy/uid/pyroscope)
  STRESS_ADDR      Stress target address (default: 127.0.0.1:18090)
  STRESS_WORKERS   Worker count (default: 8)
  STRESS_DURATION  Run duration (default: 60s)
  STRESS_DOC_SIZE  Document size in bytes (default: 16384)
  EVIDENCE_SETTLE_SECONDS
                   Seconds to wait after stress before querying telemetry
                   (default: 75; set to 0 only in tests)
EOF
  exit 1
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
fi

log() { printf '[evidence] %s\n' "$*" >&2; }

if ! [[ "$EVIDENCE_SETTLE_SECONDS" =~ ^[0-9]+$ ]]; then
  log "ERROR: EVIDENCE_SETTLE_SECONDS must be a non-negative integer"
  exit 2
fi

mkdir -p "$BUNDLE_PATH"

log "Bundle: $BUNDLE_NAME"
log "Scenario: $SCENARIO (workers=$WORKERS, duration=$DURATION, doc_size=$DOC_SIZE)"

# --- 1. Capture run configuration ---
cat > "$BUNDLE_PATH/config.json" <<CONF
{
  "scenario": "$SCENARIO",
  "timestamp": "$TIMESTAMP",
  "git_sha": "$(git -C "$REPO_ROOT" rev-parse HEAD 2>/dev/null || echo "unknown")",
  "git_sha_short": "$GIT_SHA",
  "git_dirty": $(git -C "$REPO_ROOT" diff --quiet 2>/dev/null && echo "false" || echo "true"),
  "image": "$(kubectl get statefulset scrapd -n scrap -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null || echo "unknown")",
  "replicas": $(kubectl get statefulset scrapd -n scrap -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "0"),
  "workers": $WORKERS,
  "duration": "$DURATION",
  "doc_size_bytes": $DOC_SIZE,
  "cluster": "$(kubectl config current-context 2>/dev/null || echo "unknown")"
}
CONF
log "Wrote config.json"

# --- 2. Run stress test ---
log "Running stress test..."
RUN_START_EPOCH="$(date -u +%s)"
STRESS_OUTPUT="$BUNDLE_PATH/stress-results.json"
go run "$REPO_ROOT/test/stress/" \
  -addr="$STRESS_ADDR" \
  -scenario="$SCENARIO" \
  -workers="$WORKERS" \
  -duration="$DURATION" \
  -doc-size="$DOC_SIZE" \
  > "$STRESS_OUTPUT" 2> >(tee "$BUNDLE_PATH/stress.log" >&2) || true

if [[ ! -s "$STRESS_OUTPUT" ]]; then
  log "WARNING: stress test did not produce JSON output"
  echo '{"error": "no output"}' > "$STRESS_OUTPUT"
fi
log "Wrote stress-results.json"

RUN_END_EPOCH="$(date -u +%s)"
if ((RUN_END_EPOCH <= RUN_START_EPOCH)); then
  RUN_END_EPOCH=$((RUN_START_EPOCH + 1))
fi

if ((EVIDENCE_SETTLE_SECONDS > 0)); then
  log "Waiting ${EVIDENCE_SETTLE_SECONDS}s for telemetry export..."
  sleep "$EVIDENCE_SETTLE_SECONDS"
fi

RUN_QUERY_EPOCH="$(date -u +%s)"
if ((RUN_QUERY_EPOCH <= RUN_END_EPOCH)); then
  RUN_QUERY_EPOCH=$((RUN_END_EPOCH + 1))
fi

RUN_WINDOW_SECONDS=$((RUN_QUERY_EPOCH - RUN_START_EPOCH))
RUN_PROM_WINDOW="${RUN_WINDOW_SECONDS}s"
RUN_START_NANO=$((RUN_START_EPOCH * 1000000000))
RUN_QUERY_NANO=$((RUN_QUERY_EPOCH * 1000000000))

cat > "$BUNDLE_PATH/run-window.json" <<WINDOW
{
  "start_unix_seconds": $RUN_START_EPOCH,
  "stress_end_unix_seconds": $RUN_END_EPOCH,
  "query_unix_seconds": $RUN_QUERY_EPOCH,
  "query_window_seconds": $RUN_WINDOW_SECONDS,
  "prometheus_range": "$RUN_PROM_WINDOW"
}
WINDOW
log "Wrote run-window.json"

# --- 3. Metric snapshots ---
log "Querying metric snapshots..."
METRICS_DIR="$BUNDLE_PATH/metrics"
mkdir -p "$METRICS_DIR"

# Metric names follow the otelprom/Mimir convention with type+unit suffixes
# (Mimir runs with -distributor.otel-metric-suffixes-enabled=true): counters end
# in _total, duration histograms in _seconds_*, byte gauges in _bytes.
METRIC_QUERY_FAILURES=0
METRIC_EMPTY_REQUIRED=0

# query_metric NAME QUERY [required]
# A "required" metric that returns a valid-but-empty result is treated as a
# capture failure: a reachable Mimir with miswired collectors or wrong metric
# names yields [] with no HTTP error, which must not pass the evidence gate.
query_metric() {
  local name="$1" query="$2" required="${3:-}" resp out="$METRICS_DIR/$1.json"
  if resp=$(curl -G -sf "$MIMIR_PROXY/api/v1/query" \
      --data-urlencode "query=$query" \
      --data-urlencode "time=$RUN_QUERY_EPOCH" 2>/dev/null) \
      && printf '%s' "$resp" | jq -e 'has("data") and (.data | has("result"))' >/dev/null 2>&1; then
    printf '%s' "$resp" | jq '.data.result' > "$out"
    if [[ "$required" == "required" ]] && ! jq -e '[.[]? | ((.value[1]? // "0") | tonumber? // 0)] | any(. > 0)' "$out" >/dev/null; then
      METRIC_EMPTY_REQUIRED=$((METRIC_EMPTY_REQUIRED + 1))
      log "WARNING: required metric returned no positive current-run value: $name"
    fi
  else
    echo '[]' > "$out"
    METRIC_QUERY_FAILURES=$((METRIC_QUERY_FAILURES + 1))
    log "WARNING: metric query failed (no data envelope): $name"
  fi
}

# rpc_requests is required for the current run: any write stress must produce a
# counter increase during this bundle's run window. An instant cumulative query
# can pass on stale series from a reused cluster, so this gate must use increase().
query_metric "rpc_requests" "sum(increase(scrap_rpc_server_requests_total[$RUN_PROM_WINDOW])) by (rpc_method, rpc_grpc_status_code)" required
# 5m rate window: the OTel SDK PeriodicReader exports every 60s, so rate[1m]
# would often see a single sample and return empty for a point-in-time snapshot.
query_metric "rpc_duration_p99" 'histogram_quantile(0.99, sum(rate(scrap_rpc_server_duration_seconds_bucket[5m])) by (le, rpc_method))'
query_metric "write_stage_duration" 'sum(rate(scrap_write_stage_duration_seconds_sum[5m])) by (scrap_write_stage) / sum(rate(scrap_write_stage_duration_seconds_count[5m])) by (scrap_write_stage)'
query_metric "upload_pressure" 'scrap_upload_pressure_level'
query_metric "upload_pending_bytes" 'scrap_upload_pending_bytes'
query_metric "raft_is_leader" 'scrap_raft_is_leader'
query_metric "goroutines" 'process_runtime_go_goroutines'
query_metric "heap_alloc" 'process_runtime_go_mem_heap_alloc_bytes'
log "Wrote metric snapshots ($METRIC_QUERY_FAILURES query failure(s), $METRIC_EMPTY_REQUIRED required-empty)"

# --- 4. Trace, log, and profile evidence ---
log "Capturing trace/log/profile query references..."
cat > "$BUNDLE_PATH/queries.json" <<QUERIES
{
  "traces": {
    "grafana_explore_url": "$GRAFANA_URL/explore?orgId=1&left=%7B%22datasource%22%3A%22tempo%22%2C%22queries%22%3A%5B%7B%22queryType%22%3A%22traceqlSearch%22%7D%5D%7D",
    "gate_query": "service.name=scrapd",
    "slow_writes": "{ span.scrap.write.stage = \"raft_propose\" } | duration > 100ms",
    "errors": "{ status = error }"
  },
  "logs": {
    "grafana_explore_url": "$GRAFANA_URL/explore?orgId=1&left=%7B%22datasource%22%3A%22loki%22%7D",
    "gate_query": "{service_name=\"scrapd\"}"
  },
  "profiles": {
    "grafana_explore_url": "$GRAFANA_URL/explore?orgId=1&left=%7B%22datasource%22%3A%22pyroscope%22%7D",
    "cpu_query": "process_cpu:cpu:nanoseconds:cpu:nanoseconds{service_name=\"scrapd\"}",
    "heap_queries": [
      "memory:inuse_space:bytes:space:bytes{service_name=\"scrapd\"}",
      "memory:alloc_space:bytes:space:bytes{service_name=\"scrapd\"}"
    ]
  }
}
QUERIES
log "Wrote queries.json"

TRACES_DIR="$BUNDLE_PATH/traces"
LOGS_DIR="$BUNDLE_PATH/logs"
PROFILES_DIR="$BUNDLE_PATH/profiles"
mkdir -p "$TRACES_DIR" "$LOGS_DIR" "$PROFILES_DIR"

TRACE_EVIDENCE_OK=false
TRACE_EVIDENCE_REASON="Tempo query failed"
LOG_EVIDENCE_OK=false
LOG_EVIDENCE_REASON="Loki query failed"
CPU_PROFILE_OK=false
CPU_PROFILE_REASON="Pyroscope CPU profile query failed"
HEAP_PROFILE_OK=false
HEAP_PROFILE_REASON="Pyroscope heap profile query failed"

log "Querying trace evidence..."
TRACE_OUT="$TRACES_DIR/scrapd.json"
if trace_resp=$(curl -G -sf "$TEMPO_PROXY/api/search" \
    --data-urlencode "tags=service.name=scrapd" \
    --data-urlencode "start=$RUN_START_EPOCH" \
    --data-urlencode "end=$RUN_QUERY_EPOCH" \
    --data-urlencode "limit=20" 2>/dev/null) \
    && printf '%s' "$trace_resp" | jq -e 'has("traces")' >/dev/null 2>&1; then
  printf '%s' "$trace_resp" | jq . > "$TRACE_OUT"
  trace_count="$(jq '(.traces // []) | length' "$TRACE_OUT")"
  if ((trace_count > 0)); then
    TRACE_EVIDENCE_OK=true
    TRACE_EVIDENCE_REASON="$trace_count trace(s) found for service.name=scrapd"
  else
    TRACE_EVIDENCE_REASON="no Tempo traces found for service.name=scrapd in current run window"
  fi
else
  echo '{"traces":[]}' > "$TRACE_OUT"
fi

log "Querying log evidence..."
LOG_OUT="$LOGS_DIR/scrapd.json"
if log_resp=$(curl -G -sf "$LOKI_PROXY/loki/api/v1/query_range" \
    --data-urlencode 'query={service_name="scrapd"}' \
    --data-urlencode "start=$RUN_START_NANO" \
    --data-urlencode "end=$RUN_QUERY_NANO" \
    --data-urlencode "limit=20" \
    --data-urlencode "direction=forward" 2>/dev/null) \
    && printf '%s' "$log_resp" | jq -e 'has("data") and (.data | has("result"))' >/dev/null 2>&1; then
  printf '%s' "$log_resp" | jq . > "$LOG_OUT"
  log_count="$(jq '[.data.result[]?.values[]?] | length' "$LOG_OUT")"
  if ((log_count > 0)); then
    LOG_EVIDENCE_OK=true
    LOG_EVIDENCE_REASON="$log_count log line(s) found for service_name=scrapd"
  else
    LOG_EVIDENCE_REASON="no Loki log lines found for service_name=scrapd in current run window"
  fi
else
  echo '{"status":"error","data":{"result":[]}}' > "$LOG_OUT"
fi

profile_has_data() {
  jq -e '((.timeline.samples // []) | length > 0) or
         ((.flamebearer.levels // []) | length > 0) or
         ((.groups // {}) | length > 0)' >/dev/null 2>&1
}

query_profile() {
  local name="$1" query="$2" out="$PROFILES_DIR/$1.json" resp
  if resp=$(curl -G -sf "$PYROSCOPE_PROXY/pyroscope/render" \
      --data-urlencode "query=$query" \
      --data-urlencode "from=$RUN_START_EPOCH" \
      --data-urlencode "until=$RUN_QUERY_EPOCH" \
      --data-urlencode "format=json" \
      --data-urlencode "maxNodes=64" 2>/dev/null) \
      && printf '%s' "$resp" | jq . >/dev/null 2>&1; then
    printf '%s' "$resp" | jq . > "$out"
    if profile_has_data < "$out"; then
      return 0
    fi
    return 1
  fi
  echo '{"timeline":{"samples":[]}}' > "$out"
  return 1
}

log "Querying profile evidence..."
CPU_PROFILE_QUERY='process_cpu:cpu:nanoseconds:cpu:nanoseconds{service_name="scrapd"}'
if query_profile "cpu" "$CPU_PROFILE_QUERY"; then
  CPU_PROFILE_OK=true
  CPU_PROFILE_REASON="CPU profile data found for service_name=scrapd"
else
  CPU_PROFILE_REASON="no Pyroscope CPU profile data found for service_name=scrapd in current run window"
fi

for heap_spec in \
  'heap_inuse_space|memory:inuse_space:bytes:space:bytes{service_name="scrapd"}' \
  'heap_alloc_space|memory:alloc_space:bytes:space:bytes{service_name="scrapd"}'; do
  heap_name="${heap_spec%%|*}"
  heap_query="${heap_spec#*|}"
  if query_profile "$heap_name" "$heap_query"; then
    HEAP_PROFILE_OK=true
    HEAP_PROFILE_REASON="heap profile data found via $heap_name for service_name=scrapd"
    break
  fi
  HEAP_PROFILE_REASON="no Pyroscope heap profile data found for service_name=scrapd in current run window"
done

log "Wrote trace/log/profile evidence"

# --- 5. Pass/fail evidence gate checks ---
log "Evaluating evidence gates..."
GATES_FILE="$BUNDLE_PATH/gates.json"

# Scenario-aware gates. Each scenario emits a different schema:
#   throughput -> total_ops / failed_ops
#   mixed      -> nested write.total_ops / write.failed_ops
#   pressure   -> total_writes / other_errors (pressure_rejections are intentional)
# error_rate is derived (no scenario emits it directly); metrics_captured fails
# the bundle when any Mimir snapshot query failed OR a required metric was empty.
jq \
  --argjson metric_failures "$METRIC_QUERY_FAILURES" \
  --argjson metric_empty_required "$METRIC_EMPTY_REQUIRED" \
  --argjson traces_ok "$TRACE_EVIDENCE_OK" \
  --arg trace_reason "$TRACE_EVIDENCE_REASON" \
  --argjson logs_ok "$LOG_EVIDENCE_OK" \
  --arg log_reason "$LOG_EVIDENCE_REASON" \
  --argjson cpu_profile_ok "$CPU_PROFILE_OK" \
  --arg cpu_profile_reason "$CPU_PROFILE_REASON" \
  --argjson heap_profile_ok "$HEAP_PROFILE_OK" \
  --arg heap_profile_reason "$HEAP_PROFILE_REASON" '
  # writes drives the "writes produced" gate; ops/errors drive the error rate.
  # For mixed, ops and errors span write+read+head so failing reads or heads are
  # not masked by healthy writes.
  def writes:
    if .scenario == "throughput" then (.total_ops // 0)
    elif .scenario == "mixed" then (.write.total_ops // 0)
    elif .scenario == "pressure" then (.total_writes // 0)
    else 0 end;
  def ops:
    if .scenario == "throughput" then (.total_ops // 0)
    elif .scenario == "mixed" then ((.write.total_ops // 0) + (.read.total_ops // 0) + (.head.total_ops // 0))
    elif .scenario == "pressure" then (.total_writes // 0)
    else 0 end;
  def errors:
    if .scenario == "throughput" then (.failed_ops // 0)
    elif .scenario == "mixed" then ((.write.failed_ops // 0) + (.read.failed_ops // 0) + (.head.failed_ops // 0))
    elif .scenario == "pressure" then (.other_errors // 0)
    else 0 end;
  (has("error") | not) as $completed |
  writes as $w | ops as $o | errors as $e |
  (if $o > 0 then ($e / $o) else 1 end) as $rate |
  (($metric_failures == 0) and ($metric_empty_required == 0)) as $metrics_ok |
  {
    pass: ($completed and ($w > 0) and ($o > 0) and ($rate < 0.01) and $metrics_ok and
      $traces_ok and $logs_ok and $cpu_profile_ok and $heap_profile_ok),
    scenario: (.scenario // "unknown"),
    checks: [
      {name: "stress_completed", pass: $completed,
       reason: (if $completed then "stress run completed" else "stress run reported error / no JSON" end)},
      {name: "writes_nonzero", pass: ($w > 0), reason: "writes=\($w)"},
      {name: "error_rate_below_1pct", pass: ($o > 0 and $rate < 0.01),
       reason: "error_rate=\((($rate * 10000) | floor) / 10000) errors=\($e) ops=\($o)"},
      {name: "metrics_captured", pass: $metrics_ok,
       reason: "\($metric_failures) query failure(s), \($metric_empty_required) required-empty"},
      {name: "traces_captured", pass: $traces_ok, reason: $trace_reason},
      {name: "logs_captured", pass: $logs_ok, reason: $log_reason},
      {name: "cpu_profile_captured", pass: $cpu_profile_ok, reason: $cpu_profile_reason},
      {name: "heap_profile_captured", pass: $heap_profile_ok, reason: $heap_profile_reason}
    ]
  }
' "$STRESS_OUTPUT" > "$GATES_FILE" 2>/dev/null \
  || printf '{"pass":false,"scenario":"unknown","checks":[{"name":"gates_evaluated","pass":false,"reason":"failed to parse stress output"}]}\n' > "$GATES_FILE"
log "Wrote gates.json"

# --- 6. Summary ---
PASS=$(jq -r '.pass' "$GATES_FILE")
if [[ "$PASS" == "true" ]]; then
  log "PASS - Evidence bundle: $BUNDLE_PATH"
else
  log "FAIL - Evidence bundle: $BUNDLE_PATH"
fi

log "Bundle contents:"
find "$BUNDLE_PATH" -type f | sort | while read -r f; do
  printf '  %s (%s)\n' "${f#"$BUNDLE_PATH/"}" "$(wc -c < "$f" | tr -d ' ') bytes" >&2
done

echo "$BUNDLE_PATH"
