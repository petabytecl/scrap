#!/usr/bin/env sh
set -eu

KUSTOMIZE_CMD=${KUSTOMIZE_CMD:-"go run sigs.k8s.io/kustomize/kustomize/v5@v5.7.1"}
LOCAL_KIND_OVERLAY=${LOCAL_KIND_OVERLAY:-deploy/kustomize/overlays/local-kind}
LOCAL_PROD_DEV_OVERLAY=${LOCAL_PROD_DEV_OVERLAY:-deploy/kustomize/overlays/local-prod-dev}

base_render="$(mktemp)"
local_kind_render="$(mktemp)"
local_prod_dev_render="$(mktemp)"
network_policy_render="$(mktemp)"
statefulset_render="$(mktemp)"
local_prod_dev_statefulset_render="$(mktemp)"
trap 'rm -f "$base_render" "$local_kind_render" "$local_prod_dev_render" "$network_policy_render" "$statefulset_render" "$local_prod_dev_statefulset_render"' EXIT

render() {
	# KUSTOMIZE_CMD intentionally supports commands with arguments, as defined by Makefile.
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
render "$LOCAL_PROD_DEV_OVERLAY" > "$local_prod_dev_render"

test -s "$base_render"
test -s "$local_kind_render"
test -s "$local_prod_dev_render"

extract_first_kind NetworkPolicy "$base_render" > "$network_policy_render"
test -s "$network_policy_render"

extract_first_kind StatefulSet "$base_render" > "$statefulset_render"
test -s "$statefulset_render"
extract_first_kind StatefulSet "$local_prod_dev_render" > "$local_prod_dev_statefulset_render"
test -s "$local_prod_dev_statefulset_render"

reject '^[[:space:]]*type:[[:space:]]*NodePort[[:space:]]*$' "$base_render" "NodePort service in base render"
reject '^[[:space:]]*nodePort:' "$base_render" "nodePort field in base render"

require '^kind:[[:space:]]*NetworkPolicy[[:space:]]*$' "$network_policy_render" "NetworkPolicy in base render"
require 'name:[[:space:]]*scrapd[[:space:]]*$' "$network_policy_render" "scrapd NetworkPolicy"
require 'app\.kubernetes\.io/component:[[:space:]]*scrapd[[:space:]]*$' "$network_policy_render" "scrapd pod selector"
require 'scrap\.petabyte\.cl/allowed-public-client:[[:space:]]*"true"[[:space:]]*$' "$network_policy_render" "public client ingress selector"
require 'scrap\.petabyte\.cl/allowed-public-client-namespace:[[:space:]]*"true"[[:space:]]*$' "$network_policy_render" "public client namespace ingress selector"
require 'scrap\.petabyte\.cl/allowed-admin-client:[[:space:]]*"true"[[:space:]]*$' "$network_policy_render" "admin client ingress selector"
require 'kubernetes\.io/metadata\.name:[[:space:]]*scrap-ops[[:space:]]*$' "$network_policy_render" "scrap-ops namespace selector"
require 'app\.kubernetes\.io/component:[[:space:]]*scrapd[[:space:]]*$' "$network_policy_render" "scrapd peer admin ingress selector"
require 'port:[[:space:]]*18080[[:space:]]*$' "$network_policy_render" "public gRPC ingress port"
require 'port:[[:space:]]*18081[[:space:]]*$' "$network_policy_render" "admin gRPC ingress port"
require 'port:[[:space:]]*18082[[:space:]]*$' "$network_policy_render" "metrics ingress port"
require 'admin\.peer_replication\.prepare' "$base_render" "peer replication authorization capability"
require 'healthcheck' "$base_render" "in-container healthcheck probe"
require 'scrap\.admin\.v1-readiness' "$base_render" "readiness healthcheck service"
require 'cpu:[[:space:]]*"?250m"?[[:space:]]*$' "$statefulset_render" "scrapd CPU request"
require 'memory:[[:space:]]*"?256Mi"?[[:space:]]*$' "$statefulset_render" "scrapd memory request"
require 'cpu:[[:space:]]*"?1000m"?[[:space:]]*$' "$statefulset_render" "scrapd CPU limit"
require 'memory:[[:space:]]*"?512Mi"?[[:space:]]*$' "$statefulset_render" "scrapd memory limit"

require '^[[:space:]]*nodePort:[[:space:]]*30080[[:space:]]*$' "$local_kind_render" "local-kind public NodePort"
require '^[[:space:]]*nodePort:[[:space:]]*30081[[:space:]]*$' "$local_kind_render" "local-kind admin NodePort"

require '^[[:space:]]*replicas:[[:space:]]*3[[:space:]]*$' "$local_prod_dev_statefulset_render" "local production-like StatefulSet replicas"
require 'name:[[:space:]]*SCRAP_MEMBER_ID[[:space:]]*$' "$local_prod_dev_statefulset_render" "runtime member id downward API environment"
require 'name:[[:space:]]*SCRAP_MEMBER_SLOT_ID[[:space:]]*$' "$local_prod_dev_statefulset_render" "runtime member slot downward API environment"
require 'fieldPath:[[:space:]]*metadata\.name[[:space:]]*$' "$local_prod_dev_statefulset_render" "runtime identity field path"
require '^[[:space:]]*-[[:space:]]*--cell-id=scrap-local-prod-like[[:space:]]*$' "$local_prod_dev_statefulset_render" "local production-like cell identity"
require '^[[:space:]]*-[[:space:]]*--member-id=\$\(SCRAP_MEMBER_ID\)[[:space:]]*$' "$local_prod_dev_statefulset_render" "local production-like member identity"
require '^[[:space:]]*-[[:space:]]*--member-slot-id=\$\(SCRAP_MEMBER_SLOT_ID\)[[:space:]]*$' "$local_prod_dev_statefulset_render" "local production-like member slot identity"
require '^[[:space:]]*-[[:space:]]*--peer-admin-workload-identity=local-operator[[:space:]]*$' "$local_prod_dev_statefulset_render" "local production-like peer admin workload identity"
require '^[[:space:]]*-[[:space:]]*--metadata-authority-member-id=scrapd-0[[:space:]]*$' "$local_prod_dev_statefulset_render" "local production-like metadata authority member"
require 'scrapd-0\.scrapd\.scrap-local\.svc\.cluster\.local:18081,scrapd-1\.scrapd\.scrap-local\.svc\.cluster\.local:18081,scrapd-2\.scrapd\.scrap-local\.svc\.cluster\.local:18081' "$local_prod_dev_statefulset_render" "local production-like peer discovery addresses"
