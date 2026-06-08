# Story 2.1: OpenBao Transit Boundary and Test-Only Fake

Status: review

Issue: #405

## Scope

Introduce the storage-facing Transit boundary required by ADR 0020 without
encrypting Document writes or reads yet. Production uses a narrow OpenBao HTTP
adapter. Tests and non-production runtime construction use a deterministic fake
that is explicitly not production capable.

## Acceptance Criteria

- [x] Transit interface supports data-key generation, unwrap, rewrap, readiness,
  outage, auth-denied, missing-key, and minimum-version failure behavior.
- [x] Production config validates Transit address, mount, key, and token-env
  presence without logging secret values or raw provider error bodies.
- [x] Fake Transit tests prove fail-closed behavior without live OpenBao.
- [x] Production crypto behavior remains separated from deterministic test
  behavior through `ProductionCapable`.

## Implementation Notes

- Added `internal/encryption` with provider-neutral Transit errors and classes.
- Added `OpenBaoTransit` for `/datakey/plaintext`, `/decrypt`, `/rewrap`, and
  `/keys` Transit API calls using `net/http`.
- Added `FakeTransit` for deterministic tests with outage, auth-denied,
  missing-key, rotation, rewrap, and minimum-version behavior.
- Wired `internal/cmd` runtime construction so production builds the OpenBao
  boundary and non-production gets the test-only fake.
- Tightened production startup gates for Transit URL and path shape before any
  serving surface can satisfy production readiness.

## Verification

- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/encryption ./internal/cmd ./internal/security`
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./...`
- `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./...`
- `env GOCACHE=/tmp/scrap-v2-go-build go tool -modfile=tools.go.mod golangci-lint run --timeout=5m internal/encryption/... internal/cmd/... internal/security/...`
- `env GOCACHE=/tmp/scrap-v2-go-build GOFLAGS=-buildvcs=false make check`

## Follow-Up

Story 2.2 / issue #406 owns envelope metadata persistence plus encrypted
Document write/read integration. Story 2.3 / issue #407 owns durable rewrap
through Raft metadata and audit evidence.
