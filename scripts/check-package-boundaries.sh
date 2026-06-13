#!/usr/bin/env bash
set -euo pipefail

go_cmd="${GO:-go}"
failures=0

check_no_direct_import() {
	local package="$1"
	local forbidden="$2"
	local reason="$3"
	local imports

	if ! imports="$("$go_cmd" list -f '{{range .Imports}}{{println .}}{{end}}' "$package")"; then
		echo "package boundary check failed: go list ${package}" >&2
		failures=$((failures + 1))
		return
	fi

	if grep -Fx "$forbidden" <<<"$imports" >/dev/null; then
		echo "package boundary violation: ${package} imports ${forbidden} (${reason})" >&2
		failures=$((failures + 1))
	fi
}

storage_core_packages=(
	./internal/block
	./internal/index
	./internal/routing
	./internal/store
)

consensus_packages=(
	./internal/raft
	./internal/shard
)

for package in "${storage_core_packages[@]}" "${consensus_packages[@]}"; do
	check_no_direct_import "$package" google.golang.org/grpc/status "gRPC status mapping belongs at transport boundaries"
	check_no_direct_import "$package" google.golang.org/grpc/codes "gRPC status mapping belongs at transport boundaries"
done

exit "$failures"
