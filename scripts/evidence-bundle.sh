#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

BUNDLE_DIR="${BUNDLE_DIR:-$REPO_ROOT/evidence}"
GRAFANA_URL="${GRAFANA_URL:-http://127.0.0.1:13000}"
# Query Mimir through Grafana's datasource proxy, addressed by stable uid (grafana-datasources.yaml).
MIMIR_PROXY="${MIMIR_PROXY:-${GRAFANA_URL%/}/api/datasources/proxy/uid/mimir}"
STRESS_ADDR="${STRESS_ADDR:-127.0.0.1:18090}"
SCENARIO="${1:-throughput}"
WORKERS="${STRESS_WORKERS:-8}"
DURATION="${STRESS_DURATION:-60s}"
DOC_SIZE="${STRESS_DOC_SIZE:-16384}"

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
  STRESS_ADDR      Stress target address (default: 127.0.0.1:18090)
  STRESS_WORKERS   Worker count (default: 8)
  STRESS_DURATION  Run duration (default: 60s)
  STRESS_DOC_SIZE  Document size in bytes (default: 16384)
EOF
  exit 1
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
fi

log() { printf '[evidence] %s\n' "$*" >&2; }

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

# --- 3. Metric snapshots ---
log "Querying metric snapshots..."
METRICS_DIR="$BUNDLE_PATH/metrics"
mkdir -p "$METRICS_DIR"

# Metric names follow the otelprom/Mimir convention with type+unit suffixes
# (Mimir runs with -distributor.otel-metric-suffixes-enabled=true): counters end
# in _total, duration histograms in _seconds_*, byte gauges in _bytes.
METRIC_QUERY_FAILURES=0

query_metric() {
  local name="$1" query="$2" resp out="$METRICS_DIR/$1.json"
  if resp=$(curl -sf "$MIMIR_PROXY/api/v1/query?query=$(printf '%s' "$query" | jq -sRr @uri)" 2>/dev/null) \
      && printf '%s' "$resp" | jq -e 'has("data") and (.data | has("result"))' >/dev/null 2>&1; then
    printf '%s' "$resp" | jq '.data.result' > "$out"
  else
    echo '[]' > "$out"
    METRIC_QUERY_FAILURES=$((METRIC_QUERY_FAILURES + 1))
    log "WARNING: metric query failed (no data envelope): $name"
  fi
}

query_metric "rpc_requests" 'sum(scrap_rpc_server_requests_total) by (rpc_method, rpc_grpc_status_code)'
query_metric "rpc_duration_p99" 'histogram_quantile(0.99, sum(rate(scrap_rpc_server_duration_seconds_bucket[1m])) by (le, rpc_method))'
query_metric "write_stage_duration" 'sum(rate(scrap_write_stage_duration_seconds_sum[1m])) by (scrap_write_stage) / sum(rate(scrap_write_stage_duration_seconds_count[1m])) by (scrap_write_stage)'
query_metric "upload_pressure" 'scrap_upload_pressure_level'
query_metric "upload_pending_bytes" 'scrap_upload_pending_bytes'
query_metric "raft_is_leader" 'scrap_raft_is_leader'
query_metric "goroutines" 'process_runtime_go_goroutines'
query_metric "heap_alloc" 'process_runtime_go_mem_heap_alloc_bytes'
log "Wrote metric snapshots ($METRIC_QUERY_FAILURES query failure(s))"

# --- 4. Trace and profile references ---
log "Capturing trace/profile query references..."
cat > "$BUNDLE_PATH/queries.json" <<QUERIES
{
  "traces": {
    "grafana_explore_url": "$GRAFANA_URL/explore?orgId=1&left=%7B%22datasource%22%3A%22tempo%22%2C%22queries%22%3A%5B%7B%22queryType%22%3A%22traceqlSearch%22%7D%5D%7D",
    "slow_writes": "{ span.scrap.write.stage = \"raft_propose\" } | duration > 100ms",
    "errors": "{ status = error }"
  },
  "profiles": {
    "grafana_explore_url": "$GRAFANA_URL/explore?orgId=1&left=%7B%22datasource%22%3A%22pyroscope%22%7D",
    "cpu_query": "process_cpu{service_name=\"scrapd\"}",
    "heap_query": "memory{service_name=\"scrapd\"}"
  }
}
QUERIES
log "Wrote queries.json"

# --- 5. Pass/fail evidence gate checks ---
log "Evaluating evidence gates..."
GATES_FILE="$BUNDLE_PATH/gates.json"

# Scenario-aware gates. Each scenario emits a different schema:
#   throughput -> total_ops / failed_ops
#   mixed      -> nested write.total_ops / write.failed_ops
#   pressure   -> total_writes / other_errors (pressure_rejections are intentional)
# error_rate is derived (no scenario emits it directly); metrics_captured fails
# the bundle when any Mimir snapshot query failed.
jq --argjson metric_failures "$METRIC_QUERY_FAILURES" '
  def writes:
    if .scenario == "throughput" then (.total_ops // 0)
    elif .scenario == "mixed" then (.write.total_ops // 0)
    elif .scenario == "pressure" then (.total_writes // 0)
    else 0 end;
  def errors:
    if .scenario == "throughput" then (.failed_ops // 0)
    elif .scenario == "mixed" then (.write.failed_ops // 0)
    elif .scenario == "pressure" then (.other_errors // 0)
    else 0 end;
  (has("error") | not) as $completed |
  writes as $w | errors as $e |
  (if $w > 0 then ($e / $w) else 1 end) as $rate |
  ($metric_failures == 0) as $metrics_ok |
  {
    pass: ($completed and ($w > 0) and ($rate < 0.01) and $metrics_ok),
    scenario: (.scenario // "unknown"),
    checks: [
      {name: "stress_completed", pass: $completed,
       reason: (if $completed then "stress run completed" else "stress run reported error / no JSON" end)},
      {name: "writes_nonzero", pass: ($w > 0), reason: "writes=\($w)"},
      {name: "error_rate_below_1pct", pass: ($w > 0 and $rate < 0.01),
       reason: "error_rate=\((($rate * 10000) | floor) / 10000) errors=\($e) writes=\($w)"},
      {name: "metrics_captured", pass: $metrics_ok,
       reason: "\($metric_failures) metric query failure(s)"}
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
