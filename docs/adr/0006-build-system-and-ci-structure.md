# Build system and CI structure

Status: Accepted

Date: 2026-05-26

## Context

V2 launched with a minimal 6-target Makefile, a multi-stage alpine Dockerfile, a
single raw K8s YAML, and no CI pipeline. This was adequate for Phase 1 spike-store
work but does not support the quality gates, reproducibility, or multi-environment
deployment that Phase 2+ requires.

V1 has a battle-tested build system with 40+ Makefile targets, strict linting,
kustomize manifests, Kind orchestration, and a 6-job CI pipeline. V2 can adopt the
proven patterns while adjusting for its different dependency surface and phasing.

## Decision

**Tool management.** Go-native `tools.go.mod` pins build tool versions (buf,
golangci-lint, govulncheck, gotestsum, kustomize, protoc-gen-go, protoc-gen-go-grpc).
All tools resolve via `go tool <name>` with no external version manager. CI only needs
Go installed.

Considered mise (polyglot tool version manager). Rejected because the entire toolchain
is Go binaries; mise would add an external dependency without benefit. Non-Go tools
(kind, kubectl, docker) are system-level and managed by CI runner images.

**Container image.** `scratch` base with cross-compiled static binary, same as V1.
The Makefile cross-compiles to `bin/scrapd-${GOOS}-${GOARCH}`, then `docker build`
copies the binary into a scratch image with OCI labels. V2 already uses binary-based
healthchecks (`scrapd healthcheck`), so no shell is needed.

Considered keeping alpine (debuggability). Rejected because exec-into-pod debugging is
rare and the attack surface reduction of scratch is worth it for a storage gateway.
Considered distroless (CA certs, timezone data). Rejected because scrapd does not make
outbound TLS connections or use timezone data.

**K8s manifests.** Kustomize with `base/` + `overlays/local-kind/`. The existing raw
YAML migrates into the base. Overlays patch services to NodePort and set image tags.
Additional overlays (local-dev, local-prod-dev) are added as profiles require them.

**Kind cluster.** Multi-node configuration (1 control-plane + 2 workers) so the
3-replica StatefulSet can exercise pod anti-affinity across hosts. NodePort mappings
on the control-plane expose host ports 18090 (client gRPC), 18091 (peer gRPC),
18100 (metrics). The 18xxx range avoids collision with V1's 18080-18083.

**CI pipeline.** GitHub Actions with 5 parallel jobs (static, test+coverage, race,
build, vulnerability) plus a gate job, same structure as V1. Blacksmith runners for
speed. E2E runs on `e2e` label or manual dispatch to avoid slow Kind-in-CI on every
PR. Codecov for coverage, CodeQL for security scanning.

**Linting.** V1's `.golangci.yml` ported wholesale (36+ linters), with depguard
allowlist adjusted for V2's dependency surface (add zap, ulid, etcd; drop templ).
Package boundary enforcement via script: storage core and consensus packages
(`block`, `index`, `store`, `raft`, `shard`) must not import `grpc/status` or
`grpc/codes`.

**Test harness.** Four additions: `test-race` (race detector), `test-cover` (coverage
profile + JUnit XML via gotestsum), `integration` (explicit target for
`test/integration/`), and format checking (`fmt-check`). Composite targets: `static`
(all lint checks), `tests` (all test suites), `check` (static + tests + build) as
the local CI gate.

**Local dev environment.** Orchestration script (`scripts/local-dev-env.sh`) with
`up`, `down`, `status` commands and profile support. Ships with `dev` profile
(3-node Kind, 3 scrapd replicas). `prod-like` profile (5+ nodes) deferred until
needed.
