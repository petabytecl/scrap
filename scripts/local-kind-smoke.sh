#!/usr/bin/env sh
set -eu

namespace="scrap-local"
admin_addr="${SCRAP_ADMIN_ADDR:-127.0.0.1:18081}"
workload_identity="${SCRAP_WORKLOAD_IDENTITY:-local-operator}"

kubectl_bin="${KUBECTL:-kubectl}"
go_bin="${GO:-go}"

"$kubectl_bin" -n "$namespace" rollout status statefulset/scrapd --timeout="${SCRAP_ROLLOUT_TIMEOUT:-180s}"
"$kubectl_bin" -n "$namespace" rollout status deployment/localstack --timeout="${SCRAP_ROLLOUT_TIMEOUT:-180s}"
"$kubectl_bin" -n "$namespace" rollout status deployment/openbao --timeout="${SCRAP_ROLLOUT_TIMEOUT:-180s}"

"$go_bin" run ./cmd/scrapctl \
	--admin-addr "$admin_addr" \
	--workload-identity "$workload_identity" \
	inspect summary
