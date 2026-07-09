# Build system and CI structure

Status: Accepted

Date: 2026-05-26

Superseded by: ADR-0015 for the prod-like Kind Cell and verification gates.

## Context

SCRAP launched with a minimal 6-target Makefile, a multi-stage alpine Dockerfile, a
single raw K8s YAML, and no CI pipeline. This was adequate for Phase 1 spike-store
work but does not support the quality gates, reproducibility, or multi-environment
deployment that Phase 2+ requires.

V1 has a battle-tested build system with 40+ Makefile targets, strict linting,
kustomize manifests, Kind orchestration, and a 6-job CI pipeline. SCRAP can adopt the
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
copies the binary into a scratch image with OCI labels. SCRAP already uses binary-based
healthchecks (`scrapd healthcheck`), so no shell is needed.

The image must include a maintained CA trust bundle (or the deployment must mount
an explicit validated trust store) so production HTTPS to S3 and OpenBao works
without operator `SSL_CERT_FILE` hacks (finding `H-17`). Docker builds use a
deny-by-default `.dockerignore` so credentials and ignored artifacts are not
sent to the daemon (finding `H-19`).

Release evidence gates bind to the exact candidate SHA with freshness checks;
stale `commit_ref` values fail closed (finding `H-19`). `make static` and
`make vuln` are non-waivable release blockers.

Considered keeping alpine (debuggability). Rejected because exec-into-pod debugging is
rare and the attack surface reduction of scratch is worth it for a storage gateway.
Distroless remains optional only if it is the vehicle for shipping CA roots.

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
allowlist adjusted for SCRAP's dependency surface (add zap, ulid, etcd; drop templ).
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
(3-node Kind, 3 scrapd replicas). The original `prod-like` profile deferral is
superseded by ADR-0015: prod-like and evidence Kind Cells now use Cilium-backed
named environments and separate Cell setup from E2E/evidence execution.
