#!/usr/bin/env bash
set -euo pipefail

go_cmd="${GO:-go}"
failures=0

# Package-boundary rules apply to production package imports. Tests may import
# transport adapters to assert mapping behavior at the boundary.
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
	./internal/blockstore
	./internal/storageformat
	./internal/metastore
	./internal/raftmeta
	./internal/published
)

for package in "${storage_core_packages[@]}"; do
	check_no_direct_import "$package" google.golang.org/grpc/status "gRPC status mapping belongs at transport boundaries"
	check_no_direct_import "$package" google.golang.org/grpc/codes "gRPC status mapping belongs at transport boundaries"
done

check_no_direct_import ./internal/metastore github.com/petabytecl/scrap/internal/blockstore \
	"metastore owns logical index metadata, not physical block IO records"
check_no_direct_import ./internal/localstorage github.com/petabytecl/scrap/internal/api \
	"workflow packages must not depend on transport adapters"
check_no_direct_import ./internal/localstorage google.golang.org/grpc/status \
	"workflow packages return typed internal errors"
check_no_direct_import ./internal/localstorage google.golang.org/grpc/codes \
	"workflow packages return typed internal errors"

exit "$failures"
