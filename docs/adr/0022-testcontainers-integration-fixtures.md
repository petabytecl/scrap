# Testcontainers integration fixtures

Status: Accepted

Date: 2026-06-10

## Context

S.C.R.A.P. integration tests need real external dependencies for Backend and
Transit boundary behavior. LocalStack validates S3-compatible Backend behavior.
OpenBao validates the Transit adapter against a real Transit secrets engine.

Before this decision, the LocalStack package test depended on manually supplied
environment variables and an already-running service. That made integration
coverage easy to skip and hard to reproduce in CI. Kind remains the correct
tooling for deployed Cell behavior, Cilium networking, Kubernetes manifests, and
Tier 2/Tier 3 E2E evidence, but it is too heavy for package-level service
boundary tests.

## Decision

Use `github.com/testcontainers/testcontainers-go` for integration-test fixtures
under `test/integration`. The fixtures start typed, test-scoped containers for
external services and return explicit endpoint/config values to tests.

LocalStack and OpenBao use repo-owned module-style wrappers under
`test/integration/testinfra`. The wrappers follow the Testcontainers module
shape: typed container structs, `Run(ctx, img, opts...)`, explicit image/port/env
configuration, HTTP readiness checks, endpoint helpers, and test-owned cleanup.

LocalStack is opinionated for S3 only and stays pinned to the same image family
as the Kubernetes LocalStack deployment. OpenBao runs in dev mode, then
bootstraps the Transit mount and test key through the official OpenBao Go API
client.

Integration tests that require containers use the `integration` build tag and
run through `make integration`. Default `go test ./...` continues to compile the
fixtures and run non-container tests, but it does not start external services.

Kind, Cilium, and Kubernetes deployment infrastructure remain E2E-only. Do not
use Testcontainers to replace prod-like Kind evidence gates, and do not use Kind
for the focused LocalStack/OpenBao integration boundary.
