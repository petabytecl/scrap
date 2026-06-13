#!/usr/bin/env sh
set -eu

KIND_CONFIG=${PRODLIKE_KIND_CONFIG:-deploy/kind/cluster-prodlike-cilium.yaml}
STRESS_KIND_CONFIG=${STRESS_KIND_CONFIG:-deploy/kind/cluster-stress.yaml}
PRODLIKE_OVERLAY=${PRODLIKE_OVERLAY:-deploy/kustomize/environments/prodlike}
PRODLIKE_E2E_OVERLAY=${PRODLIKE_E2E_OVERLAY:-deploy/kustomize/environments/prodlike-e2e}
CILIUM_VALUES=${CILIUM_VALUES:-deploy/cilium/prodlike-values.yaml}
CILIUM_CHART_DIR=${CILIUM_CHART_DIR:-deploy/cilium/charts/cilium}
CILIUM_SCRIPT=${CILIUM_SCRIPT:-scripts/prodlike-cilium.sh}
MAKEFILE=${MAKEFILE:-Makefile}

require_file() {
	path=$1
	description=$2

	if [ ! -f "$path" ]; then
		echo "cilium kind check failed: missing $description at $path" >&2
		exit 1
	fi
}

require() {
	pattern=$1
	file=$2
	description=$3

	if ! grep -Eq "$pattern" "$file"; then
		echo "cilium kind check failed: missing $description in $file" >&2
		exit 1
	fi
}

reject() {
	pattern=$1
	file=$2
	description=$3

	if grep -Eq "$pattern" "$file"; then
		echo "cilium kind check failed: found $description in $file" >&2
		exit 1
	fi
}

require_file "$KIND_CONFIG" "prod-like Cilium Kind config"
require_file "$STRESS_KIND_CONFIG" "evidence Cilium Kind config"
require_file "$PRODLIKE_OVERLAY/kustomization.yaml" "prod-like Kustomize overlay"
require_file "$PRODLIKE_OVERLAY/statefulset-prodlike-patch.yaml" "prod-like StatefulSet patch"
require_file "$PRODLIKE_E2E_OVERLAY/kustomization.yaml" "prod-like E2E Kustomize overlay"
require_file "$PRODLIKE_E2E_OVERLAY/statefulset-test-hooks-patch.yaml" "prod-like E2E StatefulSet patch"
require_file "$PRODLIKE_E2E_OVERLAY/statefulset-shard-placement-patch.yaml" "prod-like E2E Shard placement StatefulSet patch"
require_file "$PRODLIKE_E2E_OVERLAY/shard-placement.json" "prod-like E2E Shard placement file"
require_file "$CILIUM_VALUES" "prod-like Cilium Helm values"
require_file "$CILIUM_CHART_DIR/Chart.yaml" "vendored Cilium chart"
require_file "$CILIUM_SCRIPT" "prod-like Cilium helper script"

require 'disableDefaultCNI:[[:space:]]*true' "$KIND_CONFIG" "disabled default Kind CNI"
require 'kubeProxyMode:[[:space:]]*"?none"?' "$KIND_CONFIG" "disabled kube-proxy"
require 'extraPortMappings:' "$KIND_CONFIG" "host port mappings"
require 'hostPort:[[:space:]]*18090' "$KIND_CONFIG" "client host port"
require 'hostPort:[[:space:]]*18100' "$KIND_CONFIG" "admin host port"
require 'hostPort:[[:space:]]*18566' "$KIND_CONFIG" "LocalStack host port"

require 'disableDefaultCNI:[[:space:]]*true' "$STRESS_KIND_CONFIG" "disabled default Kind CNI in evidence config"
require 'kubeProxyMode:[[:space:]]*"?none"?' "$STRESS_KIND_CONFIG" "disabled kube-proxy in evidence config"
require 'hostPort:[[:space:]]*13000' "$STRESS_KIND_CONFIG" "Grafana host port"
require 'hostPort:[[:space:]]*14317' "$STRESS_KIND_CONFIG" "OTLP host port"

require '../local' "$PRODLIKE_OVERLAY/kustomization.yaml" "prod-like local environment reuse"
require 'networkpolicy-admin\.yaml' "$PRODLIKE_OVERLAY/kustomization.yaml" "prod-like admin NetworkPolicy"
require 'monitoring-namespace\.yaml' "$PRODLIKE_OVERLAY/kustomization.yaml" "prod-like monitoring namespace"
require_file "$PRODLIKE_OVERLAY/networkpolicy-admin.yaml" "prod-like admin NetworkPolicy"
require_file "$PRODLIKE_OVERLAY/monitoring-namespace.yaml" "prod-like monitoring namespace"
require_file "$PRODLIKE_OVERLAY/ciliumnetworkpolicy-admin-host.yaml" "prod-like host admin CiliumNetworkPolicy"
require 'SCRAP_CELL_ID' "$PRODLIKE_OVERLAY/statefulset-prodlike-patch.yaml" "prod-like Cell ID"
require 'SCRAP_ENVIRONMENT' "$PRODLIKE_OVERLAY/statefulset-prodlike-patch.yaml" "prod-like environment"
require 'SCRAP_PPROF_ENABLED' "$PRODLIKE_OVERLAY/statefulset-prodlike-patch.yaml" "prod-like pprof gate"
require 'SCRAP_SCRUB_ENABLED' "$PRODLIKE_OVERLAY/statefulset-prodlike-patch.yaml" "prod-like scrub gate"
require 'SCRAP_LIGHT_SCRUB_INTERVAL' "$PRODLIKE_OVERLAY/statefulset-prodlike-patch.yaml" "prod-like fast light scrub"
reject 'SCRAP_TEST_HOOKS' "$PRODLIKE_OVERLAY/statefulset-prodlike-patch.yaml" "prod-like test hooks"
require '../prodlike' "$PRODLIKE_E2E_OVERLAY/kustomization.yaml" "prod-like E2E environment reuse"
require 'SCRAP_TEST_HOOKS' "$PRODLIKE_E2E_OVERLAY/statefulset-test-hooks-patch.yaml" "prod-like E2E test hooks"
require 'scrap-shard-placement' "$PRODLIKE_E2E_OVERLAY/kustomization.yaml" "prod-like E2E Shard placement generator"
require 'SCRAP_SHARD_PLACEMENT_FILE' "$PRODLIKE_E2E_OVERLAY/statefulset-shard-placement-patch.yaml" "prod-like E2E Shard placement env"

require '^kubeProxyReplacement:[[:space:]]*true' "$CILIUM_VALUES" "Cilium kube-proxy replacement"
require 'mode:[[:space:]]*kubernetes' "$CILIUM_VALUES" "Kubernetes IPAM mode"
require 'enabled:[[:space:]]*true' "$CILIUM_VALUES" "enabled Cilium NodePort support"
reject 'k8sServiceHost:' "$CILIUM_VALUES" "static Kubernetes API host"
require 'version:[[:space:]]*1\.19\.4' "$CILIUM_CHART_DIR/Chart.yaml" "vendored Cilium chart version"

require 'cgroup2fs' "$CILIUM_SCRIPT" "cgroup v2 preflight"
require 'default-cgroupns-mode' "$CILIUM_SCRIPT" "private cgroup namespace guidance"
require 'inspect[[:space:]]+-f' "$CILIUM_SCRIPT" "Kind control-plane IP discovery"
require 'upgrade[[:space:]]+--install[[:space:]]+cilium' "$CILIUM_SCRIPT" "vendored Cilium Helm install"
require 'CILIUM_CHART_DIR' "$CILIUM_SCRIPT" "vendored Cilium chart directory"
require 'kubeProxyReplacement' "$CILIUM_SCRIPT" "kube-proxy replacement doctor check"
require 'kindnet' "$CILIUM_SCRIPT" "Kind default CNI baseline check"
require 'baseline' "$CILIUM_SCRIPT" "stale Kind baseline command"
require 'NetworkPolicy' "$CILIUM_SCRIPT" "NetworkPolicy doctor check"
require 'CiliumNetworkPolicy' "$CILIUM_SCRIPT" "Cilium host admin policy doctor check"
require 'scrap-headless' "$CILIUM_SCRIPT" "headless Service DNS doctor check"
require 'nslookup' "$CILIUM_SCRIPT" "headless Service DNS-only probe"
require '127\.0\.0\.1:18090' "$CILIUM_SCRIPT" "client NodePort doctor check"

require '^HELM[[:space:]]*\?=' "$MAKEFILE" "Helm tool variable"
require '^HELM_VERSION[[:space:]]*\?=' "$MAKEFILE" "Helm version variable"
require '^CILIUM_VERSION[[:space:]]*\?=' "$MAKEFILE" "Cilium version variable"
require '^CILIUM_CHART_DIR[[:space:]]*\?=' "$MAKEFILE" "vendored Cilium chart variable"
require '^PRODLIKE_OVERLAY[[:space:]]*\?=' "$MAKEFILE" "prod-like overlay variable"
require '^PRODLIKE_E2E_OVERLAY[[:space:]]*\?=' "$MAKEFILE" "prod-like E2E overlay variable"
require '^prodlike-kind-ensure:' "$MAKEFILE" "prod-like Kind ensure target"
require '^prodlike-cilium-install:' "$MAKEFILE" "Cilium install target"
require '^prodlike-kind-deploy-e2e:' "$MAKEFILE" "prod-like E2E deploy target"
require '^prodlike-cell-doctor:' "$MAKEFILE" "prod-like Cell doctor target"
require 'baseline' "$MAKEFILE" "stale stress Kind baseline check"
