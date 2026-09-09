# Go 1.27 and dependency refresh

Status: Accepted

Date: 2026-09-09

## Context

S.C.R.A.P. is moving to Go 1.27.1. The previous golangci-lint pin cannot
analyze that toolchain, and vulnerability scanning reports advisories in the
existing dependency graph. The dependency freeze from the initial upgrade
plan has been lifted to refresh application libraries and development tools.

## Decision

Use Go 1.27.1 for application and tool modules. Refresh dependencies to the
latest compatible stable releases on their existing module paths, including
transitive dependencies, and align repository and CI tool pins. Keep exact
resolved versions in the module files and checksums in their sum files.

Preserve S.C.R.A.P.'s public protobuf contracts and existing Block, Frame,
Pebble Projection, and Raft persistence contracts. Regenerate protobuf Go
output with the updated generators. Make only the source adaptations needed
for dependency compatibility and the configured static checks.

Major module-path migrations that change persistence compatibility require a
separate explicit migration decision. A newer dependency version alone is
not permission to change on-disk formats or weaken existing safety gates.

The refresh retains these compatibility boundaries:

- etcd's embedded WAL/snapshot libraries move to the latest 3.6 maintenance
  release, 3.6.14, while Raft remains at 3.6.0. The 3.7 releases replace their
  protobuf APIs with pointer-based standard protobuf messages and require a
  coordinated consensus/peer migration plus old-data compatibility tests.
- Pebble remains on the latest v1 release, 1.1.5, and OpenBao's client remains
  on its existing module path. Their v2 module migrations are separate work.
- Helm moves to 3.21.4 on its existing v3 CLI path. Kind moves to 0.33.0,
  explicitly selecting Kubernetes 1.35.8 by image digest. Kind's new default,
  Kubernetes 1.37, is outside Cilium 1.19.4's tested 1.32–1.35 range.
- Tool dependencies use the newest versions their consumers support:
  golangci-lint 2.13.2 requires the pre-1.0 `gobwas/glob` API and the pre-0.5
  `go-check-sumtype` API; Buf 1.72.0 requires Protovalidate's pre-1.4 CEL API
  and the pre-1.0 `go.lsp.dev` APIs. The old `github.com/google/cel-go` path
  stops at 0.31; 0.32 declares the new `cel.dev/cel-go` module path.

The updated Prometheus exporter normalizes counter names with a single
`_total` suffix. Source OTel instrument names stay unchanged. Current
evidence and operator queries accept both the normalized and legacy doubled
suffixes, preferring current series with PromQL `or` before aggregation.

`tools.go.mod` describes the declared tool dependency graph. Tidy it in an
isolated directory containing that module and its sums, so application
packages do not become accidental dependencies of the tools module.

References: [etcd 3.7 migration](https://etcd.io/docs/v3.7/upgrades/upgrade_3_7/),
[Cilium 1.19.4 compatibility](https://github.com/cilium/cilium/blob/v1.19.4/Documentation/network/kubernetes/compatibility.rst),
[Kind 0.33.0 images](https://github.com/kubernetes-sigs/kind/releases/tag/v0.33.0).

Validate the refresh with package and race tests, container-backed
integration tests, static checks, binary builds, parser fuzzing, benchmark
smoke tests, and vulnerability scans. Record incompatible upstream releases
and remaining advisories explicitly instead of disabling checks.
