# Prod-like Kind Cell CNI and verification gates

Status: Accepted

Date: 2026-05-30

## Context

SCRAP uses Kind for local and CI-adjacent production-like validation. The current
local deployment path is Kubernetes-shaped, but it still leaves several
confidence gaps:

- Kind's default networking does not enforce the same NetworkPolicy behavior
  expected from production.
- Production uses Cilium with kube-proxy replacement, while local Kind currently
  relies on the default Kind network path.
- E2E and stress setup logic is spread across Make targets, shell scripts, and
  Go tests that invoke `kubectl` directly.
- Normal CI skips the Kind E2E job unless it is manually requested, so a green
  push does not prove the prod-like Cell is healthy.

Docker Compose was considered as a simpler local test harness, but it would not
exercise StatefulSet identity, PVC behavior, headless Service peer discovery,
NetworkPolicy, NodePort behavior, or Kubernetes resource attributes. Those are
part of the production risk surface for S.C.R.A.P.

## Decision

Prod-like and evidence Kind Cells use Cilium as the CNI with
`kubeProxyReplacement=true`. They must not silently fall back to Kind's default
CNI or kube-proxy.

The Kind cluster configuration for those Cells disables the default Kind CNI.
Cilium is installed before S.C.R.A.P. workloads are deployed. The Cilium
installation must expose the same service datapath assumptions as production:
Service routing, NodePort behavior, DNS, and NetworkPolicy enforcement are all
part of the local prod-like contract.

A new operator tool, `scrapctl`, owns local Cell diagnostics and evidence
orchestration. It has two command classes:

- Read-only commands that are safe in any Cell, such as status, doctor, leader,
  peer, upload-pressure, and evidence inspection.
- Fault and test commands that are allowed only in non-production local,
  prod-like, or evidence Cells, such as backend outage injection, leader pod
  deletion, projection-divergence injection, Block corruption, pprof capture,
  and evidence runs.

Fault and test commands must refuse to run unless the target Cell declares
itself non-production/evidence, the needed test or admin hooks are enabled, the
namespace/context is explicit, and destructive actions include an explicit
confirmation value such as the target `cell_id`.

Verification is split into three tiers:

- Tier 1: the commit gate. Static checks, lint, proto generation check, unit and
  integration tests, race tests, build, and vulnerability scanning.
- Tier 2: the prod-like E2E gate. A Cilium-backed Kind Cell passes `scrapctl`
  doctor checks, basic Document API checks, leader failover, Backend upload
  happy path, Backend outage and recovery, upload pressure, fast scrub tests,
  and NetworkPolicy allow/deny checks for admin and pprof access.
- Tier 3: the evidence gate. The evidence Cell runs throughput, mixed
  read/write/head, and upload-pressure stress scenarios and produces a current
  run evidence bundle containing metric, log, trace, and profile proof.

Existing completed PRDs for the OTel evidence plane and architecture
remediation must not be closed as final bookkeeping until Tier 2 is working on
the new Cilium-backed prod-like Kind path.

## Consequences

- Local prod-like validation better matches the production CNI and service
  datapath.
- Kind setup becomes heavier because Cilium must be installed and verified
  before workloads are deployed.
- `scrapctl doctor` becomes a required preflight for prod-like and evidence
  Cells, including host/runtime checks such as cgroup v2, Docker cgroup
  namespace support, Kind node cgroup namespace isolation, Cilium readiness,
  kube-proxy replacement status, NodePort reachability, CoreDNS, headless
  Service peer DNS, and NetworkPolicy behavior.
- E2E and stress tests target an existing Cell. Cell setup and test execution
  are separate concerns, even when a CI workflow composes them.
- Kustomize remains the deployment renderer, but `deploy/` should be reorganized
  around bases, reusable components, and named environments instead of scattered
  workflow-specific overlays.
- Docker Compose is not added as a parallel harness for the current SCRAP
  confidence work.
