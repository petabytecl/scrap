# Storage Gateway Release Image And Rollout Policy

Status: planning gate for GitHub issue `#21`
Last updated: 2026-05-23

This document defines the production build, image, artifact, provenance,
dependency-update, Go-upgrade, and rollout policy for S.C.R.A.P. storage-gateway
releases.

The policy separates fast PR feedback from nightly correctness signal,
dedicated-runner evidence, and release evidence. A normal PR should catch
ordinary regressions quickly. Production write ACK mode requires stronger
release evidence before any target deployment is allowed to accept production
traffic.

## Command Entry Points

`Makefile` remains the primary command surface for local development and CI.
GitHub Actions, dedicated runners, release jobs, and operator runbooks should
call Make targets instead of re-encoding command sequences in workflow YAML.

Required targets for ordinary development:

| Target | Required behavior | Normal PR |
| --- | --- | --- |
| `make fmt-check` | Verify Go formatting and import formatting. | Required. |
| `make proto` | Regenerate protobuf and gRPC code. | Not required directly when `proto-check` runs. |
| `make proto-check` | Run `buf lint`, run breaking checks when the base branch has a proto module, regenerate code, and fail on generated-code diffs. | Required. |
| `make lint` | Run fast static checks. Today this is `go vet`; #53 owns the hardened `golangci-lint` baseline. | Required. |
| `make test` | Run ordinary package tests. | Required. |
| `make test-race` | Run race tests. | Required through `make check` until the suite is too large and a scoped race target is introduced. |
| `make build` | Build production binaries and supported local tools. | Required. |
| `make check` | Aggregate normal PR checks. | Required. |

Required targets before production write ACK eligibility:

| Target | Required behavior | Gate |
| --- | --- | --- |
| `make vuln` | Run Go vulnerability scanning for the module graph. #53 owns the first implementation. | Nightly and release-blocking once implemented. |
| `make lint-security` | Run the hardened security/correctness lint profile when separated from fast lint. | PR or nightly according to #53's final runtime cost. |
| `make test-leak` | Run goroutine/resource leak checks for long-lived workers and servers. | Nightly and release evidence. |
| `make test-fuzz` | Run corpus-regression fuzz tests for parsers, codecs, request validation, and format boundaries. | Nightly and release evidence for affected packages. |
| `make test-compat` | Run mixed-version and stored-fixture compatibility tests for protobuf, metadata, block, index, and envelope formats. | PR for schema/format changes; release evidence. |
| `make perf-smoke` | Run stable lightweight performance smoke tests. | PR only when affected and cheap; dedicated evidence for production claims. |
| `make image` | Build the production image from pinned inputs. | Release path. |
| `make image-debug` | Build or document a debug image/workflow separate from production images. | Release path. |
| `make sbom` | Generate SBOMs for binaries and images. | Release-blocking. |
| `make sign` | Sign release artifacts and images. | Release-blocking. |
| `make provenance` | Produce build provenance attestations. | Release-blocking. |
| `make release-check` | Verify all release evidence exists for a release candidate using `RELEASE_EVIDENCE_MANIFEST`. | Release-blocking. |

`make release-check` calls `scrap-release-gate`, which evaluates a JSON release
evidence manifest against the gate catalog in `internal/releasegate`. Each gate
must either point to an automated command or name a manual release artifact and
artifact owner. The report includes the production write ACK readiness gate name
and blocking issues for missing evidence where the gate maps to
`SCRAP_PRODUCTION_WRITE_ACK_READINESS`.

Generated-code checks are release-blocking. A release candidate must prove that
committed generated code matches the checked-in schemas and that the Buf
breaking-change policy ran against the intended base.

## CI And Security Gate Matrix

| Gate | Normal PR | Nightly | Dedicated runner | Manual release evidence |
| --- | --- | --- | --- | --- |
| Formatting and generated code | `make check` required. | Required. | Required when generated artifacts are part of the suite. | Required. |
| Unit tests and race tests | `make check` required. | Repeated or broader race checks. | Required for long-running harnesses. | Required. |
| Hardened lint | Fast baseline required after #53. Expensive or noisy checks may be nightly until the baseline is clean. | Full lint profile required. | Not normally required unless runner-specific code changes. | Required clean result or approved exception. |
| CodeQL | Go and Actions analysis run on PRs. | Scheduled CodeQL remains enabled. | Not normally required. | Required clean result or approved exception. |
| Dependency review | Required for dependency-changing PRs once configured. | Dependency inventory checked. | Not normally required. | Required for release candidate. |
| `govulncheck` / Go vulnerability scan | Required for dependency-changing PRs once runtime is acceptable; otherwise nightly until #53 proves cost. | Required. | Not normally required. | Required clean result or approved exception. |
| Image vulnerability scan | Not required before images exist. | Optional for development images. | Required for release-candidate images if scanning needs dedicated credentials or runtime. | Required clean result or approved exception. |
| SBOM, signatures, provenance | Not required for every PR. | Optional dry run. | Required when release jobs need trusted builders. | Required. |
| Correctness harness tiers | As defined by the correctness harness policy. | Broader simulator, crash, fault, fuzz, and model suites. | Required for fsync, backend, OpenBao, and performance evidence. | Required summary artifact. |
| Performance and Go upgrade benchmarks | Not required except for Go/runtime/storage dependency changes. | Trend collection when stable. | Authoritative profile on pinned hardware/runtime. | Required for Go/toolchain upgrades and production capacity claims. |

Severity policy:

- reachable critical and high vulnerabilities in production code, runtime
  images, or release tooling block release;
- medium vulnerabilities require owner review before release and may block when
  they affect authentication, authorization, encryption, audit, artifact
  integrity, or remote input handling;
- low vulnerabilities may be tracked to ordinary dependency-update work unless
  they affect a production-readiness claim;
- non-reachable findings, scanner false positives, and base-image noise require
  documented exceptions before release.

Exception records must include owner, affected artifact, scanner, finding ID,
reachability assessment, mitigation, expiry date, and the release or deployment
scope where the exception is valid.

## Production Image Requirements

Production images must be deterministic enough to audit and minimal enough to
operate safely.

Required properties:

- pinned multi-stage build inputs, including base image digests and tool
  versions;
- build from clean source plus generated artifacts verified by `make check`;
- minimal nonroot runtime image with no package manager or shell;
- only the S.C.R.A.P. binary, required runtime assets, CA roots if needed, and
  explicit metadata labels;
- runtime user and filesystem permissions that do not require root;
- read-only root filesystem compatibility unless a later implementation proves a
  writable path is required;
- image labels for git SHA, version, Go version, schema version, format
  versions, build time, dirty-tree flag, and source repository;
- immutable version and git-SHA tags;
- production deployment by digest, not by mutable tag;
- `latest` allowed only for development workflows and never in production
  manifests.

Debugging must not weaken the production image. Use a separate debug image or a
documented ephemeral debug workflow with explicit authorization, auditability,
and time limits. Production images must stay shellless/minimal unless a separate
ADR approves a different operational model.

## Release Artifacts

Every production release candidate must publish or retain:

- source commit SHA and clean-tree proof;
- binary checksums;
- image digest and immutable tags;
- SBOM for binaries and images;
- artifact and image signatures;
- build provenance attestation that identifies builder, source, command, tool
  versions, dependency graph, and generated-code status;
- vulnerability scan results for Go dependencies and images;
- CodeQL result link;
- correctness and release-gate evidence summary;
- exception records with owner and expiry when any gate is not clean.

Release metadata must be durable enough for incident response and rollback
decisions. The release record should say exactly which image digest was
deployed, which config/profile was used, and which production-readiness evidence
was accepted.

## Dependency And Toolchain Updates

Dependency updates must run the normal gates. Updates to storage, consensus,
protobuf/gRPC, backend SDK, OpenBao, crypto, logging/metrics/tracing, build, or
release tooling require extra review because they can change production safety
claims.

Policy:

- routine patch/minor updates may be scheduled, but must keep normal gates green;
- security updates may bypass the ordinary schedule but must still preserve
  generated-code and test gates;
- storage/runtime dependency updates require at least performance smoke evidence
  and targeted correctness checks when the dependency affects IO, consensus,
  serialization, encryption, or backend behavior;
- dependency exceptions must be time-bound and linked to an owner;
- new production dependencies are reviewed against the package architecture and
  ADR coverage checklist before adoption.

Go toolchain upgrades are deliberate release-policy events, not incidental CI
changes. Upgrade PRs must record:

- old and new Go version;
- reason for upgrade;
- `make check`, race, compatibility, and vulnerability results;
- performance-smoke or benchmark comparison for ACK latency, read latency,
  allocation rate, heap profile, GC pause, and GC CPU where relevant;
- known runtime behavior changes;
- rollback plan if the upgraded runtime regresses storage behavior.

Do not promote a Go/toolchain upgrade into a production release until the
benchmark and correctness evidence is reviewed.

## Security Finding Triage

Security lint and vulnerability findings are production-readiness inputs, not
drive-by cleanup. The first hardened baseline is implemented by issue `#53`.

Triage records for `golangci-lint`, `gosec`, `govulncheck`, CodeQL, dependency
review, image scans, SBOM analysis, or provenance checks must include:

- scanner name and version;
- finding ID, package, file, dependency, image layer, or artifact reference;
- owner and decision date;
- severity and reachability assessment;
- production impact, including whether the finding affects authentication,
  authorization, encryption, audit, release tooling, remote input, or
  acknowledged-write safety;
- fix plan or exception justification;
- expiry date for exceptions;
- linked follow-up issue when the finding is not fixed in the current PR.

Critical and high reachable findings block release. They also block normal PRs
when the affected code or dependency is introduced by that PR. Medium findings
require explicit owner review before release. Low findings may be batched unless
they undermine a production-readiness claim.

Suppressions must be local and explained. Blanket `nolint`, scanner-wide
exclusions, or vulnerability ignore files are not acceptable unless they point
to a triage record with owner and expiry.

## Rollout Policy

Production rollout is canary, then shard-safe progressive rollout. Plain
Kubernetes rolling updates are not sufficient for S.C.R.A.P. because pod
readiness does not prove shard quorum health, byte-serving eligibility, local
durability, or format compatibility.

Rollout preconditions:

- target image digest is approved by release evidence;
- production config profile validates before traffic shift;
- feature gates for block, index, envelope, metadata, and admin API behavior are
  compatible with the mixed-version window;
- OpenBao, backend, and local disk readiness are healthy;
- dashboards and alerts for write admission, disk runway, backend lag, repair
  lag, restore lag, corruption incidents, Raft health, and OpenBao health are
  live for the target deployment and satisfy the
  [Storage Gateway Dashboard And Alert Contract](storage-gateway-dashboard-alert-contract.md);
- operator runbooks satisfy
  [Storage Gateway Operator Runbooks](storage-gateway-operator-runbooks.md);
- rollback or roll-forward path is documented for the release candidate.

Shard-safe rollout requirements:

- canary one member or one bounded shard cohort before broad rollout;
- never take down enough members to risk quorum for a shard;
- respect PodDisruptionBudgets and storage-node failure domains;
- mark a replacement member byte-serving eligible only after local bytes are
  verified and metadata freshness is proven;
- fence stale leaders before they can acknowledge writes or serve fresh reads;
- pause rollout when repair lag, backend lag, disk runway, OpenBao health, or
  corruption incidents breach release thresholds;
- do not enable format writers until all required readers are compatible and the
  consensus-owned feature gate is committed;
- record rollout step, image digest, config generation, shard/member cohort,
  health result, and operator approval.

Rollback policy:

- rollback by digest to a known compatible release when the previous binary can
  read all active formats and metadata states;
- roll forward instead of rolling back when a writer has committed a format,
  schema, or metadata feature that older binaries cannot safely read;
- keep failed release evidence and rollout logs for diagnosis;
- do not clear a failed release gate by rerunning without preserving the failed
  evidence unless the failure is proven environmental.

## Issue Relationships

#21 defines policy. #53 implements the first hardened lint and vulnerability
gate baseline. #45 automates production release-gate aggregation after #21,
#44, #46, #47, #48, and #53 provide the evidence contracts it needs.
