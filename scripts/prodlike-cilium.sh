#!/usr/bin/env sh
set -eu

command_name=${1:-doctor}

CLUSTER=${PRODLIKE_KIND_CLUSTER:-scrap-prodlike}
NAMESPACE=${SCRAP_PRODLIKE_NAMESPACE:-scrap}
CILIUM_VERSION=${CILIUM_VERSION:-1.19.4}
CILIUM_CHART_DIR=${CILIUM_CHART_DIR:-deploy/cilium/charts/cilium}
CILIUM_VALUES=${CILIUM_VALUES:-deploy/cilium/prodlike-values.yaml}
DOCKER=${DOCKER:-docker}
HELM=${HELM:-helm}
KUBECTL=${KUBECTL:-kubectl}

log() {
	printf '[prodlike-cilium] %s\n' "$*"
}

die() {
	printf '[prodlike-cilium] %s\n' "$*" >&2
	exit 1
}

require_command() {
	command_name=${1%% *}
	if ! command -v "$command_name" >/dev/null 2>&1; then
		die "missing required command: $command_name"
	fi
}

run_helm() {
	# HELM intentionally supports commands with arguments, matching Makefile tool vars.
	# shellcheck disable=SC2086
	$HELM "$@"
}

kernel_at_least_5_14() {
	version=$(uname -r | sed 's/[^0-9.].*$//')
	major=${version%%.*}
	rest=${version#*.}
	minor=${rest%%.*}
	[ "${major:-0}" -gt 5 ] || { [ "${major:-0}" -eq 5 ] && [ "${minor:-0}" -ge 14 ]; }
}

preflight_host() {
	require_command "$DOCKER"

	cgroup_type=$(stat -fc %T /sys/fs/cgroup)
	if [ "$cgroup_type" != "cgroup2fs" ]; then
		die "cgroup v2 is required for Cilium Socket LB on Kind; /sys/fs/cgroup is $cgroup_type"
	fi

	cgroup_version=$("$DOCKER" info --format '{{.CgroupVersion}}')
	if [ "$cgroup_version" != "2" ]; then
		die "Docker cgroup v2 is required, got $cgroup_version"
	fi

	security_options=$("$DOCKER" info --format '{{range .SecurityOptions}}{{println .}}{{end}}')
	if ! printf '%s\n' "$security_options" | grep -Fx 'name=cgroupns' >/dev/null 2>&1; then
		die "Docker must use private cgroup namespaces; set dockerd --default-cgroupns-mode=private"
	fi

	if ! kernel_at_least_5_14; then
		die "kernel 5.14+ is required unless cgroup v1 net_cls/net_prio are disabled"
	fi
}

kind_node_names() {
	"$DOCKER" ps \
		--filter "label=io.x-k8s.kind.cluster=$CLUSTER" \
		--format '{{.Names}}'
}

preflight_kind_nodes() {
	host_cgroup=$(ls -al /proc/self/ns/cgroup)
	nodes=$(kind_node_names)
	if [ -z "$nodes" ]; then
		die "no Kind nodes found for cluster $CLUSTER"
	fi

	for node in $nodes; do
		node_cgroup=$("$DOCKER" exec "$node" ls -al /proc/self/ns/cgroup)
		if [ "$node_cgroup" = "$host_cgroup" ]; then
			die "Kind node $node shares the host cgroup namespace; recreate with private cgroup namespaces"
		fi
	done
}

check_kind_cni_baseline() {
	require_command "$KUBECTL"
	if "$KUBECTL" -n kube-system get daemonset kube-proxy >/dev/null 2>&1; then
		die "kube-proxy daemonset exists before Cilium install; delete and recreate $CLUSTER with kubeProxyMode=none"
	fi
	if "$KUBECTL" -n kube-system get daemonset kindnet >/dev/null 2>&1; then
		die "kindnet daemonset exists before Cilium install; delete and recreate $CLUSTER with disableDefaultCNI=true"
	fi
}

control_plane_ip() {
	"$DOCKER" inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$CLUSTER-control-plane"
}

install_cilium() {
	require_command "$HELM"
	require_command "$KUBECTL"
	preflight_host
	preflight_kind_nodes
	check_kind_cni_baseline
	[ -d "$CILIUM_CHART_DIR" ] || die "vendored Cilium chart not found: $CILIUM_CHART_DIR"

	api_host=$(control_plane_ip)
	if [ -z "$api_host" ]; then
		die "could not discover $CLUSTER-control-plane IP"
	fi

	log "installing vendored Cilium $CILIUM_VERSION into $CLUSTER with kubeProxyReplacement=true"
	run_helm upgrade --install cilium "$CILIUM_CHART_DIR" \
		--namespace kube-system \
		--values "$CILIUM_VALUES" \
		--set k8sServiceHost="$api_host" \
		--set k8sServicePort=6443
}

wait_cilium() {
	require_command "$KUBECTL"
	"$KUBECTL" -n kube-system rollout status daemonset/cilium --timeout=180s
	"$KUBECTL" -n kube-system rollout status deployment/cilium-operator --timeout=180s
	"$KUBECTL" -n kube-system rollout status deployment/coredns --timeout=180s
}

cilium_status() {
	"$KUBECTL" -n kube-system exec ds/cilium -- cilium-dbg status --verbose
}

check_cilium_status() {
	status=$(cilium_status)
	printf '%s\n' "$status" | grep -Eq 'KubeProxyReplacement:[[:space:]]+True' ||
		die "Cilium did not report KubeProxyReplacement: True"
	printf '%s\n' "$status" | grep -Eq 'NodePort:[[:space:]]+Enabled' ||
		die "Cilium did not report NodePort: Enabled"
	if "$KUBECTL" -n kube-system get daemonset kube-proxy >/dev/null 2>&1; then
		die "kube-proxy daemonset exists; prod-like Cell must use Cilium replacement only"
	fi
	if "$KUBECTL" -n kube-system get daemonset kindnet >/dev/null 2>&1; then
		die "kindnet daemonset exists; recreate the Kind cluster with disableDefaultCNI=true"
	fi
}

wait_tcp() {
	host=$1
	port=$2
	label=$3

	deadline=$(($(date +%s) + 30))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		if command -v nc >/dev/null 2>&1 && nc -z "$host" "$port" >/dev/null 2>&1; then
			return
		fi
		if command -v python3 >/dev/null 2>&1 && python3 - "$host" "$port" <<'PY' >/dev/null 2>&1
import socket
import sys
with socket.create_connection((sys.argv[1], int(sys.argv[2])), timeout=1):
    pass
PY
		then
			return
		fi
		sleep 1
	done
	die "$label is not reachable at $host:$port"
}

check_nodeports() {
	wait_tcp 127.0.0.1 18090 "client NodePort 127.0.0.1:18090"
	wait_tcp 127.0.0.1 18100 "admin NodePort 127.0.0.1:18100"
}

run_probe() {
	namespace=$1
	name=$2
	labels=$3
	url=$4

	"$KUBECTL" -n "$namespace" run "$name" \
		--restart=Never \
		--rm \
		--attach \
		--quiet \
		--image=busybox:1.36 \
			--labels "$labels" \
			--command -- sh -c "wget -qO- -T 3 '$url' >/dev/null"
}

check_headless_dns() {
	name="scrap-dns-check-$$"
	target="scrapd-0.scrap-headless.$NAMESPACE.svc.cluster.local"

	"$KUBECTL" -n "$NAMESPACE" run "$name" \
		--restart=Never \
		--rm \
		--attach \
		--quiet \
		--image=busybox:1.36 \
		--labels "app=scrap-netcheck" \
		--command -- sh -c "nslookup '$target' >/dev/null" >/dev/null 2>&1 ||
		die "headless Service DNS for scrapd-0.scrap-headless.$NAMESPACE.svc.cluster.local failed"
}

check_network_policy() {
	"$KUBECTL" -n "$NAMESPACE" get networkpolicy scrapd-admin-restrict >/dev/null 2>&1 ||
		die "NetworkPolicy scrapd-admin-restrict is required before policy checks"
	"$KUBECTL" -n "$NAMESPACE" get ciliumnetworkpolicy scrapd-admin-host-allow >/dev/null 2>&1 ||
		die "CiliumNetworkPolicy scrapd-admin-host-allow is required before host admin checks"
	"$KUBECTL" get namespace monitoring >/dev/null 2>&1 ||
		die "monitoring namespace is required before policy checks"

	allowed="scrap-admin-allow-$$"
	run_probe monitoring "$allowed" "app=otel-collector" \
		"http://scrapd-0.scrap-headless.$NAMESPACE.svc.cluster.local:9100/healthz" >/dev/null ||
		die "NetworkPolicy allow check from monitoring/otel-collector failed"

	denied="scrap-admin-deny-$$"
	if run_probe "$NAMESPACE" "$denied" "app=scrap-netcheck" \
		"http://scrapd-0.scrap-headless.$NAMESPACE.svc.cluster.local:9100/healthz" >/dev/null 2>&1; then
		die "NetworkPolicy deny check unexpectedly reached admin port"
	fi
}

doctor() {
	preflight_host
	preflight_kind_nodes
	wait_cilium
	check_cilium_status
	check_nodeports
	check_headless_dns
	check_network_policy
	log "prod-like Cilium Cell checks passed"
}

case "$command_name" in
	preflight)
		preflight_host
		;;
	baseline)
		check_kind_cni_baseline
		;;
	install)
		install_cilium
		;;
	wait)
		wait_cilium
		;;
	status)
		cilium_status
		;;
	doctor)
		doctor
		;;
	*)
		die "usage: $0 [preflight|baseline|install|wait|status|doctor]"
		;;
esac
