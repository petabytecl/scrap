#!/usr/bin/env sh
set -eu

KUSTOMIZE_CMD=${KUSTOMIZE_CMD:-"go tool -modfile=tools.go.mod kustomize"}
LOCAL_KIND_OVERLAY=${LOCAL_KIND_OVERLAY:-deploy/kustomize/overlays/local-kind}

base_render="$(mktemp)"
local_kind_render="$(mktemp)"
statefulset_render="$(mktemp)"
trap 'rm -f "$base_render" "$local_kind_render" "$statefulset_render"' EXIT

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

test -s "$base_render"
test -s "$local_kind_render"

extract_first_kind StatefulSet "$base_render" > "$statefulset_render"
test -s "$statefulset_render"

reject '^[[:space:]]*type:[[:space:]]*NodePort[[:space:]]*$' "$base_render" "NodePort service in base render"
reject '^[[:space:]]*nodePort:' "$base_render" "nodePort field in base render"

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
