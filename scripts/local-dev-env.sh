#!/usr/bin/env sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
cd "$repo_root"

command_name=${1:-up}

PROFILE=${SCRAP_LOCAL_DEV_PROFILE:-dev}
KIND_VERSION=${KIND_VERSION:-v0.31.0}
KIND_CMD=${KIND:-}
KUBECTL=${KUBECTL:-kubectl}
MAKE_CMD=${MAKE:-make}
TOOLS_MODFILE=${TOOLS_MODFILE:-tools.go.mod}
NAMESPACE=${SCRAP_LOCAL_DEV_NAMESPACE:-scrap}
IMAGE_NAME=${IMAGE_NAME:-localhost/scrapd:local}
ROLLOUT_TIMEOUT=${SCRAP_ROLLOUT_TIMEOUT:-180s}
STATE_DIR=${SCRAP_LOCAL_DEV_STATE_DIR:-tmp/local-dev}
PID_DIR="$STATE_DIR/pids"
LOG_DIR="$STATE_DIR/logs"
KIND_CONFIG=${SCRAP_LOCAL_DEV_KIND_CONFIG:-$STATE_DIR/kind.yaml}

CLIENT_GRPC_PORT=${SCRAP_CLIENT_PORT:-18090}
METRICS_PORT=${SCRAP_METRICS_PORT:-18100}
FORWARD_NAMES="client-grpc metrics"

case "$PROFILE" in
	dev)
		DEFAULT_CLUSTER=scrap-dev
		DEFAULT_OVERLAY=deploy/kustomize/overlays/local-kind
		DEFAULT_NODE_COUNT=4
		;;
	prod-like)
		DEFAULT_CLUSTER=scrap-prod-dev
		DEFAULT_OVERLAY=deploy/kustomize/overlays/local-kind
		DEFAULT_NODE_COUNT=5
		;;
	*)
		printf 'unknown SCRAP_LOCAL_DEV_PROFILE: %s\n' "$PROFILE" >&2
		exit 2
		;;
esac

KIND_CLUSTER=${SCRAP_LOCAL_DEV_CLUSTER:-${KIND_CLUSTER:-$DEFAULT_CLUSTER}}
LOCAL_KIND_OVERLAY=${LOCAL_KIND_OVERLAY:-$DEFAULT_OVERLAY}
KIND_NODE_COUNT=${SCRAP_LOCAL_DEV_KIND_NODES:-$DEFAULT_NODE_COUNT}

usage() {
	cat <<EOF
Usage: scripts/local-dev-env.sh [up|down|status|stop-forwards]

Commands:
  up             Build, create/update kind, deploy scrapd, and start port-forwards.
  down           Stop port-forwards and delete the local kind cluster.
  status         Show local Kubernetes resources and port-forward state.
  stop-forwards  Stop only port-forwards started by this script.

Useful overrides:
  SCRAP_LOCAL_DEV_PROFILE=$PROFILE
  KIND_CLUSTER=$KIND_CLUSTER
  IMAGE_NAME=$IMAGE_NAME
  LOCAL_KIND_OVERLAY=$LOCAL_KIND_OVERLAY
  SCRAP_LOCAL_DEV_KIND_NODES=$KIND_NODE_COUNT
  SCRAP_LOCAL_DEV_DETACH=1
  SKIP_IMAGE_BUILD=1
EOF
}

log() {
	printf '[local-dev] %s\n' "$*"
}

require_command() {
	if ! command -v "$1" >/dev/null 2>&1; then
		printf 'missing required command: %s\n' "$1" >&2
		exit 2
	fi
}

kind_cmd() {
	if [ -n "$KIND_CMD" ]; then
		# shellcheck disable=SC2086
		$KIND_CMD "$@"
		return
	fi
	go run "sigs.k8s.io/kind@$KIND_VERSION" "$@"
}

kustomize_build() {
	if [ -n "${KUSTOMIZE:-}" ]; then
		# shellcheck disable=SC2086
		$KUSTOMIZE build "$LOCAL_KIND_OVERLAY"
		return
	fi
	go tool -modfile="$TOOLS_MODFILE" kustomize build "$LOCAL_KIND_OVERLAY"
}

cluster_exists() {
	kind_cmd get clusters 2>/dev/null | grep -Fx "$KIND_CLUSTER" >/dev/null 2>&1
}

ensure_dirs() {
	mkdir -p "$PID_DIR" "$LOG_DIR"
}

write_default_kind_config() {
	if [ -n "${SCRAP_LOCAL_DEV_KIND_CONFIG:-}" ]; then
		return
	fi
	mkdir -p "$STATE_DIR"
	{
		cat <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: $KIND_CLUSTER
nodes:
  - role: control-plane
    extraPortMappings:
      - containerPort: 30090
        hostPort: $CLIENT_GRPC_PORT
        protocol: TCP
      - containerPort: 30100
        hostPort: $METRICS_PORT
        protocol: TCP
EOF
		node_index=1
		while [ "$node_index" -lt "$KIND_NODE_COUNT" ]; do
			printf '  - role: worker\n'
			node_index=$((node_index + 1))
		done
	} > "$KIND_CONFIG"
}

ensure_cluster() {
	if cluster_exists; then
		log "kind cluster already exists: $KIND_CLUSTER"
	else
		write_default_kind_config
		log "creating kind cluster: $KIND_CLUSTER profile=$PROFILE nodes=$KIND_NODE_COUNT"
		kind_cmd create cluster --name "$KIND_CLUSTER" --config "$KIND_CONFIG"
	fi
	kind_cmd export kubeconfig --name "$KIND_CLUSTER" >/dev/null
}

build_image() {
	if [ "${SKIP_IMAGE_BUILD:-0}" = "1" ]; then
		log "skipping image build because SKIP_IMAGE_BUILD=1"
		return
	fi
	log "building local scrapd image: $IMAGE_NAME"
	"$MAKE_CMD" image IMAGE_NAME="$IMAGE_NAME"
}

load_image() {
	log "loading image into kind: $IMAGE_NAME"
	kind_cmd load docker-image "$IMAGE_NAME" --name "$KIND_CLUSTER"
}

apply_manifests() {
	log "applying local dev manifests profile=$PROFILE overlay=$LOCAL_KIND_OVERLAY"
	kustomize_build | "$KUBECTL" apply -f -
}

wait_for_cluster() {
	log "waiting for scrapd pods"
	"$KUBECTL" -n "$NAMESPACE" rollout status statefulset/scrapd --timeout="$ROLLOUT_TIMEOUT"
}

pid_file_for() {
	printf '%s/%s.pid' "$PID_DIR" "$1"
}

log_file_for() {
	printf '%s/%s.log' "$LOG_DIR" "$1"
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

start_forward() {
	name=$1
	resource=$2
	local_port=$3
	remote_port=$4
	pid_file=$(pid_file_for "$name")
	log_file=$(log_file_for "$name")

	stop_forward "$name"
	log "starting port-forward: $name 127.0.0.1:$local_port -> $resource:$remote_port"
	nohup "$KUBECTL" -n "$NAMESPACE" port-forward "$resource" "$local_port:$remote_port" >"$log_file" 2>&1 </dev/null &
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

start_forwards() {
	start_forward client-grpc svc/scrap "$CLIENT_GRPC_PORT" 9090
	start_forward metrics svc/scrap "$METRICS_PORT" 9100
}

print_endpoints() {
	cat <<EOF

Local dev environment is ready.

  Profile:      $PROFILE
  Cluster:      $KIND_CLUSTER
  Client gRPC:  127.0.0.1:$CLIENT_GRPC_PORT
  Metrics:      http://127.0.0.1:$METRICS_PORT/metrics

Port-forward logs:
  $LOG_DIR

Keep this command running while you use the environment.
Press Ctrl-C to stop port-forwards. Delete the cluster with:
  make local-dev-down
EOF
}

monitor_forwards() {
	if [ "${SCRAP_LOCAL_DEV_DETACH:-0}" = "1" ]; then
		return
	fi
	log "port-forwards are running in the foreground"
	trap 'stop_forwards; exit 0' INT TERM
	while :; do
		for name in $FORWARD_NAMES; do
			pid_file=$(pid_file_for "$name")
			if [ ! -f "$pid_file" ] || ! kill -0 "$(cat "$pid_file")" >/dev/null 2>&1; then
				printf 'port-forward stopped unexpectedly: %s\n' "$name" >&2
				cat "$(log_file_for "$name")" >&2 || true
				exit 1
			fi
		done
		sleep 5
	done
}

up() {
	require_command docker
	require_command go
	require_command "$KUBECTL"
	ensure_dirs
	build_image
	ensure_cluster
	load_image
	apply_manifests
	wait_for_cluster
	start_forwards
	print_endpoints
	monitor_forwards
}

stop_forwards() {
	ensure_dirs
	for name in $FORWARD_NAMES; do
		stop_forward "$name"
	done
}

down() {
	ensure_dirs
	stop_forwards
	if cluster_exists; then
		log "deleting kind cluster: $KIND_CLUSTER"
		kind_cmd delete cluster --name "$KIND_CLUSTER"
	else
		log "kind cluster does not exist: $KIND_CLUSTER"
	fi
}

status() {
	ensure_dirs
	if cluster_exists; then
		kind_cmd export kubeconfig --name "$KIND_CLUSTER" >/dev/null
		"$KUBECTL" -n "$NAMESPACE" get pods,svc
	else
		log "kind cluster does not exist: $KIND_CLUSTER"
	fi
	for name in $FORWARD_NAMES; do
		pid_file=$(pid_file_for "$name")
		if [ -f "$pid_file" ] && kill -0 "$(cat "$pid_file")" >/dev/null 2>&1; then
			log "port-forward running: $name pid=$(cat "$pid_file")"
		else
			log "port-forward stopped: $name"
		fi
	done
}

case "$command_name" in
	up)
		up
		;;
	down)
		down
		;;
	status)
		status
		;;
	stop-forwards)
		stop_forwards
		;;
	-h|--help|help)
		usage
		;;
	*)
		usage >&2
		exit 2
		;;
esac
