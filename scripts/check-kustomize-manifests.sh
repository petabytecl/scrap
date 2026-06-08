#!/usr/bin/env sh
set -eu

KUSTOMIZE_CMD=${KUSTOMIZE_CMD:-"go tool -modfile=tools.go.mod kustomize"}
LOCAL_KIND_OVERLAY=${LOCAL_KIND_OVERLAY:-deploy/kustomize/environments/local}
PRODLIKE_OVERLAY=${PRODLIKE_OVERLAY:-deploy/kustomize/environments/prodlike}
EVIDENCE_OVERLAY=${EVIDENCE_OVERLAY:-deploy/kustomize/environments/evidence}
EVIDENCE_STACK=${EVIDENCE_STACK:-deploy/kustomize/components/evidence-stack}

base_render="$(mktemp)"
local_kind_render="$(mktemp)"
prodlike_render="$(mktemp)"
evidence_render="$(mktemp)"
evidence_stack_render="$(mktemp)"
statefulset_render="$(mktemp)"
trap 'rm -f "$base_render" "$local_kind_render" "$prodlike_render" "$evidence_render" "$evidence_stack_render" "$statefulset_render"' EXIT

render() {
	# KUSTOMIZE_CMD intentionally supports commands with arguments.
	# shellcheck disable=SC2086
	$KUSTOMIZE_CMD build "$1"
}

require() {
	pattern=$1
	file=$2
	description=$3

	if ! grep -Eq "$pattern" "$file"; then
		echo "manifest check failed: missing $description" >&2
		exit 1
	fi
}

reject() {
	pattern=$1
	file=$2
	description=$3

	if grep -Eq "$pattern" "$file"; then
		echo "manifest check failed: found $description" >&2
		exit 1
	fi
}

extract_first_kind() {
	kind=$1
	file=$2

	awk -v target_kind="$kind" '
		function matches_target_kind(value) {
			return value ~ "(^|\n)kind:[[:space:]]*" target_kind "[[:space:]]*(\n|$)"
		}
		/^---$/ {
			if (matches_target_kind(doc)) {
				printf "%s", doc
				found = 1
				exit
			}
			doc = ""
			next
		}
		{
			doc = doc $0 ORS
		}
		END {
			if (!found && matches_target_kind(doc)) {
				printf "%s", doc
			}
		}
	' "$file"
}

render deploy/kustomize/base > "$base_render"
render "$LOCAL_KIND_OVERLAY" > "$local_kind_render"
render "$PRODLIKE_OVERLAY" > "$prodlike_render"
render "$EVIDENCE_OVERLAY" > "$evidence_render"
render "$EVIDENCE_STACK" > "$evidence_stack_render"

test -s "$base_render"
test -s "$local_kind_render"
test -s "$prodlike_render"
test -s "$evidence_render"
test -s "$evidence_stack_render"

extract_first_kind StatefulSet "$base_render" > "$statefulset_render"
test -s "$statefulset_render"

require '../../components/localstack' deploy/kustomize/environments/local/kustomization.yaml "explicit local LocalStack component"
require '../../components/nodeports' deploy/kustomize/environments/local/kustomization.yaml "explicit local NodePort component"
require '../../components/stress-tuning' deploy/kustomize/environments/evidence/kustomization.yaml "explicit evidence stress tuning component"

reject '^[[:space:]]*type:[[:space:]]*NodePort[[:space:]]*$' "$base_render" "NodePort service in base render"
reject '^[[:space:]]*nodePort:' "$base_render" "nodePort field in base render"
reject 'name:[[:space:]]*SCRAP_SECURITY_MODE' "$base_render" "security mode in base render"

require 'healthcheck' "$base_render" "in-container healthcheck probe"
require 'scrap\.v1-readiness' "$base_render" "readiness healthcheck service"

require 'readOnlyRootFilesystem:[[:space:]]*true' "$statefulset_render" "read-only root filesystem"
require 'runAsNonRoot:[[:space:]]*true' "$statefulset_render" "non-root user"
require 'allowPrivilegeEscalation:[[:space:]]*false' "$statefulset_render" "no privilege escalation"

require 'cpu:[[:space:]]*"?100m"?' "$statefulset_render" "scrapd CPU request"
require 'memory:[[:space:]]*"?128Mi"?' "$statefulset_render" "scrapd memory request"
require 'cpu:[[:space:]]*"?500m"?' "$statefulset_render" "scrapd CPU limit"
require 'memory:[[:space:]]*"?512Mi"?' "$statefulset_render" "scrapd memory limit"

require '^[[:space:]]*nodePort:[[:space:]]*30090[[:space:]]*$' "$local_kind_render" "local-kind client NodePort"
require '^[[:space:]]*nodePort:[[:space:]]*30100[[:space:]]*$' "$local_kind_render" "local-kind metrics NodePort"
require 'name:[[:space:]]*SCRAP_SECURITY_MODE' "$local_kind_render" "explicit local security mode"
require 'value:[[:space:]]*development' "$local_kind_render" "local development security mode"
require 'kind:[[:space:]]*Service' "$evidence_render" "evidence S.C.R.A.P. workload"
require 'localstack' "$evidence_render" "evidence LocalStack workload"
require 'SCRAP_ENVIRONMENT' "$evidence_render" "evidence environment marker"
require 'name:[[:space:]]*SCRAP_SECURITY_MODE' "$evidence_render" "explicit evidence security mode"
require 'value:[[:space:]]*development' "$evidence_render" "evidence development security mode"
require 'SCRAP_CELL_ID' "$prodlike_render" "prod-like Cell ID marker"
require 'name:[[:space:]]*SCRAP_SECURITY_MODE' "$prodlike_render" "explicit prod-like security mode"
require 'value:[[:space:]]*development' "$prodlike_render" "prod-like development security mode"
require 'NetworkPolicy' "$prodlike_render" "prod-like NetworkPolicy"
reject 'name:[[:space:]]*otel-collector' "$evidence_render" "evidence stack in S.C.R.A.P. workload render"
require 'name:[[:space:]]*otel-collector' "$evidence_stack_render" "evidence stack collector"
require 'name:[[:space:]]*kube-state-metrics' "$evidence_stack_render" "evidence stack kube-state-metrics"
require 'registry\.k8s\.io/kube-state-metrics/kube-state-metrics:v2\.19\.0' "$evidence_stack_render" "pinned kube-state-metrics image"
require 'nodes/proxy' "$evidence_stack_render" "collector node proxy scrape RBAC"
require 'nodes/metrics' "$evidence_stack_render" "collector node metrics scrape RBAC"
require 'job_name:[[:space:]]*kube-state-metrics' "$evidence_stack_render" "kube-state-metrics scrape job"
require 'job_name:[[:space:]]*kubernetes-cadvisor' "$evidence_stack_render" "cAdvisor scrape job"
require 'kubernetes-cluster\.json:' "$evidence_stack_render" "Kubernetes cluster dashboard"
require 'kubernetes-workloads\.json:' "$evidence_stack_render" "Kubernetes workloads dashboard"
reject '^[[:space:]]*-[[:space:]]*secrets[[:space:]]*$' "$evidence_stack_render" "kube-state-metrics secrets collector RBAC"
reject '^kind:[[:space:]]*StatefulSet[[:space:]]*$' "$evidence_stack_render" "S.C.R.A.P. workload in evidence stack render"
