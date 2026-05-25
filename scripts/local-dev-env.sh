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
NAMESPACE=${SCRAP_LOCAL_DEV_NAMESPACE:-scrap-local}
IMAGE_NAME=${IMAGE_NAME:-localhost/scrapd:local}
ROLLOUT_TIMEOUT=${SCRAP_ROLLOUT_TIMEOUT:-180s}
STATE_DIR=${SCRAP_LOCAL_DEV_STATE_DIR:-tmp/local-dev}
PID_DIR="$STATE_DIR/pids"
LOG_DIR="$STATE_DIR/logs"
KIND_CONFIG=${SCRAP_LOCAL_DEV_KIND_CONFIG:-$STATE_DIR/kind.yaml}

PUBLIC_GRPC_PORT=${SCRAP_PUBLIC_PORT:-18080}
ADMIN_GRPC_PORT=${SCRAP_ADMIN_PORT:-18081}
ADMIN_UI_PORT=${SCRAP_ADMIN_UI_PORT:-18083}
LOCALSTACK_PORT=${LOCALSTACK_PORT:-4566}
OPENBAO_PORT=${OPENBAO_PORT:-8200}
FORWARD_NAMES="public-grpc admin-grpc admin-ui localstack openbao"

case "$PROFILE" in
	dev)
		DEFAULT_CLUSTER=scrap-dev
		DEFAULT_OVERLAY=deploy/kustomize/overlays/local-dev
		DEFAULT_NODE_COUNT=1
		;;
	prod-like)
		DEFAULT_CLUSTER=scrap-prod-dev
		DEFAULT_OVERLAY=deploy/kustomize/overlays/local-prod-dev
		DEFAULT_NODE_COUNT=4
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
  up             Build, create/update kind, deploy scrapd/localstack/openbao, and start port-forwards.
  down           Stop port-forwards and delete the local kind cluster.
  status         Show local Kubernetes resources and port-forward state.
  stop-forwards  Stop only port-forwards started by this script.

Useful overrides:
  SCRAP_LOCAL_DEV_PROFILE=$PROFILE
  KIND_CLUSTER=$KIND_CLUSTER
  IMAGE_NAME=$IMAGE_NAME
  LOCAL_KIND_OVERLAY=$LOCAL_KIND_OVERLAY
  SCRAP_LOCAL_DEV_KIND_NODES=$KIND_NODE_COUNT
  SCRAP_ADMIN_UI_PORT=$ADMIN_UI_PORT
  LOCALSTACK_PORT=$LOCALSTACK_PORT
  OPENBAO_PORT=$OPENBAO_PORT
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
		# KIND may include arguments, matching the Makefile's overridable command style.
		# shellcheck disable=SC2086
		$KIND_CMD "$@"
		return
	fi
	go run "sigs.k8s.io/kind@$KIND_VERSION" "$@"
}

kustomize_build() {
	if [ -n "${KUSTOMIZE:-}" ]; then
		# KUSTOMIZE may include arguments, matching the Makefile's overridable command style.
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

reset_recreated_workloads() {
	if "$KUBECTL" get namespace "$NAMESPACE" >/dev/null 2>&1; then
		log "resetting recreated local dev workloads"
		"$KUBECTL" -n "$NAMESPACE" delete job \
			localstack-s3-bootstrap \
			openbao-transit-bootstrap \
			--ignore-not-found=true >/dev/null
		"$KUBECTL" -n "$NAMESPACE" delete statefulset scrapd \
			--ignore-not-found=true \
			--wait=true >/dev/null
	fi
}

apply_manifests() {
	log "applying local dev manifests profile=$PROFILE overlay=$LOCAL_KIND_OVERLAY"
	kustomize_build | "$KUBECTL" apply -f -
}

wait_for_cluster() {
	log "waiting for scrapd, localstack, and openbao"
	"$KUBECTL" -n "$NAMESPACE" rollout status statefulset/scrapd --timeout="$ROLLOUT_TIMEOUT"
	"$KUBECTL" -n "$NAMESPACE" rollout status deployment/localstack --timeout="$ROLLOUT_TIMEOUT"
	"$KUBECTL" -n "$NAMESPACE" rollout status deployment/openbao --timeout="$ROLLOUT_TIMEOUT"
	"$KUBECTL" -n "$NAMESPACE" wait --for=condition=complete job/localstack-s3-bootstrap --timeout="$ROLLOUT_TIMEOUT"
	"$KUBECTL" -n "$NAMESPACE" wait --for=condition=complete job/openbao-transit-bootstrap --timeout="$ROLLOUT_TIMEOUT"
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

wait_url() {
	name=$1
	url=$2
	attempt=1
	while [ "$attempt" -le 60 ]; do
		if curl -fsS --max-time 2 "$url" >/dev/null 2>&1; then
			log "$name is reachable: $url"
			return
		fi
		attempt=$((attempt + 1))
		sleep 1
	done
	printf '%s did not become reachable: %s\n' "$name" "$url" >&2
	exit 1
}

start_forwards() {
	start_forward public-grpc svc/scrapd-public "$PUBLIC_GRPC_PORT" 18080
	start_forward admin-grpc svc/scrapd-admin "$ADMIN_GRPC_PORT" 18081
	start_forward admin-ui pod/scrapd-0 "$ADMIN_UI_PORT" 18083
	start_forward localstack svc/localstack "$LOCALSTACK_PORT" 4566
	start_forward openbao svc/openbao "$OPENBAO_PORT" 8200
	wait_url "admin UI" "http://127.0.0.1:$ADMIN_UI_PORT/admin/"
	wait_url "LocalStack" "http://127.0.0.1:$LOCALSTACK_PORT/_localstack/health"
	wait_url "OpenBao" "http://127.0.0.1:$OPENBAO_PORT/v1/sys/health?standbyok=true&sealedcode=204&uninitcode=204"
}

print_endpoints() {
	down_command="scripts/local-dev-env.sh down"
	if [ "$PROFILE" = "prod-like" ]; then
		down_command="make local-dev-prod-down"
	fi
	cat <<EOF

Local dev environment is ready.

  Profile:     $PROFILE
  Cluster:     $KIND_CLUSTER
  Admin UI:    http://127.0.0.1:$ADMIN_UI_PORT/admin/
  Public gRPC: 127.0.0.1:$PUBLIC_GRPC_PORT
  Admin gRPC:  127.0.0.1:$ADMIN_GRPC_PORT
  LocalStack:  http://127.0.0.1:$LOCALSTACK_PORT
  OpenBao:     http://127.0.0.1:$OPENBAO_PORT

Port-forward logs:
  $LOG_DIR

Keep this command running while you use the environment.
Press Ctrl-C to stop port-forwards. Delete the cluster with:
  $down_command
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
	require_command curl
	require_command "$KUBECTL"
	ensure_dirs
	build_image
	ensure_cluster
	load_image
	reset_recreated_workloads
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
		"$KUBECTL" -n "$NAMESPACE" get pods,svc,job
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
