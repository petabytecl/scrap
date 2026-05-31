#!/usr/bin/env sh
set -eu

ARTIFACT_DIR=${ARTIFACT_DIR:-artifacts/kind}
KUBECTL=${KUBECTL:-kubectl}
KUBE_CONTEXT=${KUBE_CONTEXT:-${PRODLIKE_E2E_KUBE_CONTEXT:-${PRODLIKE_KUBE_CONTEXT:-}}}

run_kubectl() {
	if [ -n "$KUBE_CONTEXT" ]; then
		"$KUBECTL" --context "$KUBE_CONTEXT" "$@"
		return
	fi
	"$KUBECTL" "$@"
}

capture() {
	out=$1
	shift
	"$@" >"$out" 2>&1 || true
}

capture_kubectl() {
	out=$1
	shift
	capture "$out" run_kubectl "$@"
}

safe_name() {
	printf '%s' "$1" | tr '/: ' '---'
}

mkdir -p "$ARTIFACT_DIR"

capture_kubectl "$ARTIFACT_DIR/version.txt" version
capture_kubectl "$ARTIFACT_DIR/cluster-info.txt" cluster-info
capture_kubectl "$ARTIFACT_DIR/nodes-wide.txt" get nodes -o wide
capture_kubectl "$ARTIFACT_DIR/namespaces.txt" get namespaces

for namespace in kube-system scrap monitoring; do
	if ! run_kubectl get namespace "$namespace" >/dev/null 2>&1; then
		continue
	fi

	ns_dir="$ARTIFACT_DIR/$namespace"
	mkdir -p "$ns_dir/logs" "$ns_dir/describes"

	capture_kubectl "$ns_dir/all.txt" -n "$namespace" get all -o wide
	capture_kubectl "$ns_dir/events.txt" -n "$namespace" get events --sort-by=.lastTimestamp
	capture_kubectl "$ns_dir/networkpolicies.yaml" -n "$namespace" get networkpolicy -o yaml
	capture_kubectl "$ns_dir/ciliumnetworkpolicies.yaml" -n "$namespace" get ciliumnetworkpolicy -o yaml
	capture_kubectl "$ns_dir/configmaps.yaml" -n "$namespace" get configmap -o yaml

	pods=$(run_kubectl -n "$namespace" get pods -o name 2>/dev/null || true)
	for pod in $pods; do
		name=$(safe_name "$pod")
		capture_kubectl "$ns_dir/describes/$name.txt" -n "$namespace" describe "$pod"
		capture_kubectl "$ns_dir/logs/$name.log" -n "$namespace" logs "$pod" --all-containers --tail=-1 --prefix
	done
done

printf '%s\n' "$ARTIFACT_DIR"
