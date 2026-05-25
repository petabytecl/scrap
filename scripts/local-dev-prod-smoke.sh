#!/usr/bin/env sh
set -eu

namespace=${SCRAP_LOCAL_DEV_NAMESPACE:-scrap-local}
kubectl_bin=${KUBECTL:-kubectl}
go_bin=${GO:-go}
rollout_timeout=${SCRAP_ROLLOUT_TIMEOUT:-180s}
state_dir=${SCRAP_LOCAL_DEV_STATE_DIR:-tmp/local-dev}
smoke_dir="$state_dir/prod-smoke"
public_port=${SCRAP_LOCAL_PROD_SMOKE_PUBLIC_PORT:-18180}
admin_port=${SCRAP_LOCAL_PROD_SMOKE_ADMIN_PORT:-18181}
public_workload=${SCRAP_PUBLIC_WORKLOAD_IDENTITY:-local-public-client}
admin_workload=${SCRAP_WORKLOAD_IDENTITY:-local-operator}
expected_replicas=${SCRAP_LOCAL_PROD_SMOKE_REPLICAS:-3}

case "$expected_replicas" in
	''|*[!0-9]*)
		printf 'SCRAP_LOCAL_PROD_SMOKE_REPLICAS must be a positive integer: %s\n' "$expected_replicas" >&2
		exit 2
		;;
esac
if [ "$expected_replicas" -lt 2 ]; then
	printf 'SCRAP_LOCAL_PROD_SMOKE_REPLICAS must be at least 2 for fail-closed smoke: %s\n' "$expected_replicas" >&2
	exit 2
fi

log() {
	printf '[local-prod-smoke] %s\n' "$*"
}

pid_file_for() {
	printf '%s/%s.pid' "$smoke_dir" "$1"
}

log_file_for() {
	printf '%s/%s.log' "$smoke_dir" "$1"
}

stop_forward() {
	name=$1
	pid_file=$(pid_file_for "$name")
	if [ ! -f "$pid_file" ]; then
		return
	fi
	pid=$(cat "$pid_file")
	if kill -0 "$pid" >/dev/null 2>&1; then
		log "stopping port-forward: $name"
		kill "$pid" >/dev/null 2>&1 || true
		wait "$pid" 2>/dev/null || true
	fi
	rm -f "$pid_file"
}

stop_forwards() {
	stop_forward public-authority
	stop_forward admin-authority
}

start_forward() {
	name=$1
	local_port=$2
	remote_port=$3
	pid_file=$(pid_file_for "$name")
	log_file=$(log_file_for "$name")

	stop_forward "$name"
	log "starting port-forward: $name 127.0.0.1:$local_port -> pod/scrapd-0:$remote_port"
	nohup "$kubectl_bin" -n "$namespace" port-forward pod/scrapd-0 "$local_port:$remote_port" >"$log_file" 2>&1 </dev/null &
	pid=$!
	printf '%s\n' "$pid" > "$pid_file"
	sleep 1
	if ! kill -0 "$pid" >/dev/null 2>&1; then
		printf 'port-forward failed: %s\n' "$name" >&2
		cat "$log_file" >&2 || true
		rm -f "$pid_file"
		exit 1
	fi
}

wait_for_pod_absent() {
	pod=$1
	attempt=1
	while [ "$attempt" -le 60 ]; do
		if ! "$kubectl_bin" -n "$namespace" get pod "$pod" >/dev/null 2>&1; then
			return
		fi
		attempt=$((attempt + 1))
		sleep 1
	done
	printf 'pod still exists after timeout: %s\n' "$pod" >&2
	exit 1
}

wait_for_replicas_ready() {
	replicas=$1
	index=0
	while [ "$index" -lt "$replicas" ]; do
		"$kubectl_bin" -n "$namespace" wait --for=condition=Ready "pod/scrapd-$index" --timeout="$rollout_timeout"
		index=$((index + 1))
	done
}

restore_replicas() {
	stop_forwards
	if [ -n "${initial_replicas:-}" ]; then
		log "restoring scrapd replicas to $initial_replicas"
		"$kubectl_bin" -n "$namespace" scale statefulset/scrapd --replicas="$initial_replicas" >/dev/null
		"$kubectl_bin" -n "$namespace" rollout status statefulset/scrapd --timeout="$rollout_timeout" >/dev/null || true
	fi
}

mkdir -p "$smoke_dir"
initial_replicas=$("$kubectl_bin" -n "$namespace" get statefulset scrapd -o jsonpath='{.spec.replicas}')
trap restore_replicas EXIT INT TERM

log "ensuring $expected_replicas scrapd replicas are ready"
"$kubectl_bin" -n "$namespace" scale statefulset/scrapd --replicas="$expected_replicas" >/dev/null
"$kubectl_bin" -n "$namespace" rollout status statefulset/scrapd --timeout="$rollout_timeout"
wait_for_replicas_ready "$expected_replicas"

start_forward public-authority "$public_port" 18080
start_forward admin-authority "$admin_port" 18081

success_report="$smoke_dir/replicated-ack-success.json"
fail_report="$smoke_dir/replicated-ack-fail-closed.json"

log "running replicated ACK success smoke"
"$go_bin" run ./cmd/scrap-local-prod-smoke \
	--public-addr "127.0.0.1:$public_port" \
	--admin-addr "127.0.0.1:$admin_port" \
	--public-workload-identity "$public_workload" \
	--admin-workload-identity "$admin_workload" \
	--expected-replica-count "$expected_replicas" \
	> "$success_report"
cat "$success_report"

log "scaling one peer down to demonstrate fail-closed ACK"
reduced_replicas=$((expected_replicas - 1))
removed_pod="scrapd-$reduced_replicas"
"$kubectl_bin" -n "$namespace" scale statefulset/scrapd --replicas="$reduced_replicas" >/dev/null
"$kubectl_bin" -n "$namespace" rollout status statefulset/scrapd --timeout="$rollout_timeout"
wait_for_pod_absent "$removed_pod"

log "running fail-closed smoke"
"$go_bin" run ./cmd/scrap-local-prod-smoke \
	--public-addr "127.0.0.1:$public_port" \
	--admin-addr "127.0.0.1:$admin_port" \
	--public-workload-identity "$public_workload" \
	--admin-workload-identity "$admin_workload" \
	--expected-replica-count "$expected_replicas" \
	--expect-failure \
	> "$fail_report"
cat "$fail_report"

log "local production-like replicated ACK smoke passed"
