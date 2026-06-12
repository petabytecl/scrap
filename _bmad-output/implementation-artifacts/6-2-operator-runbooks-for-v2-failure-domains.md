---
baseline_commit: 12fe6d7e498e406ef0a5aabc7d9125310a2d664a
---

# Story 6.2: Operator Runbooks for V2 Failure Domains

Status: ready-for-dev

## Story

As a platform operator,
I want runbooks for the major V2 failure domains,
so that production incidents can be handled without reconstructing behavior
from source code.

## Acceptance Criteria

1. **AC-6.2.1 - Runbooks cover the required V2 failure domains.** Given V2 runbooks are created, when docs are reviewed, then they cover startup/security readiness, mTLS/cert rotation, OpenBao Transit dependency, Backend upload pressure, restore failures, eviction campaigns, Block Quarantine repair, Content Quarantine response, multi-Shard routing health, and evidence collection. Evidence links each runbook to the owning feature epic or release gate.
2. **AC-6.2.1a - Runbook structure is consistent and reviewable.** Given each runbook is structured, when it is reviewed, then it includes normal path, failure path, rollback or escalation, expected outputs, and evidence collection sections. Evidence records the runbook section checklist.
3. **AC-6.2.2 - Commands are implemented and redacted.** Given a runbook references commands, when examples are reviewed, then commands match implemented `scrapctl`, admin, or make targets and avoid raw secrets. Evidence records command validation and redaction checks.
4. **AC-6.2.3 - Incident steps preserve storage authority boundaries.** Given a runbook contains incident steps, when reviewed for safety, then it does not instruct operators to use Backend inventory, local files, or telemetry as storage authority. Evidence records the authority-boundary review result.
5. **AC-6.2.4 - Critical workflows are independently runnable.** Given cold reads, Content Quarantine, OpenBao fail-closed behavior, and Backend restore are documented, when runbook workflows are reviewed, then each workflow is independently runnable from documented operator commands and expected outputs. Evidence records workflow validation results.

## Tasks / Subtasks

- [ ] Create the V2 operator runbook documentation surface. (AC: 1, 2)
  - [ ] Create `docs/runbooks/README.md` as the index for all Story 6.2 runbooks.
  - [ ] Add `docs/runbooks/v2-startup-security-readiness.md`.
  - [ ] Add `docs/runbooks/v2-mtls-certificate-rotation.md`.
  - [ ] Add `docs/runbooks/v2-openbao-transit-dependency.md`.
  - [ ] Add `docs/runbooks/v2-backend-upload-pressure.md`.
  - [ ] Add `docs/runbooks/v2-restore-failures.md`.
  - [ ] Add `docs/runbooks/v2-eviction-campaigns.md`.
  - [ ] Add `docs/runbooks/v2-block-quarantine-repair.md`.
  - [ ] Add `docs/runbooks/v2-content-quarantine-response.md`.
  - [ ] Add `docs/runbooks/v2-multi-shard-routing-health.md`.
  - [ ] Add `docs/runbooks/v2-evidence-collection.md`.
  - [ ] Keep files focused; do not combine unrelated failure domains into one large document.
- [ ] Apply a common runbook checklist to every runbook. (AC: 1, 2)
  - [ ] Each runbook must include: purpose, owning feature epic or release gate, symptoms, normal path, failure path, rollback or escalation, expected outputs, evidence collection, redaction requirements, authority-boundary note, and references.
  - [ ] Create `_bmad-output/implementation-artifacts/v2-operator-runbook-evidence.md`.
  - [ ] In the evidence artifact, record a row for each runbook showing section coverage, owner, source requirement, command validation result, authority-boundary review result, redaction result, and final `PASS`/`CONCERNS`/`FAIL`.
- [ ] Validate every command example against implemented surfaces. (AC: 3, 5)
  - [ ] Use `go run ./cmd/scrapctl --help` and `internal/scrapctl/run.go` to confirm top-level commands before documenting them.
  - [ ] Use subcommand source files before writing examples: `internal/scrapctl/openbao.go`, `internal/scrapctl/quarantine.go`, `internal/scrapctl/eviction.go`, `internal/scrapctl/evidence.go`, and `internal/scrapctl/fault.go`.
  - [ ] Use only implemented make targets from `Makefile`, especially `production-rehearsal-security`, `production-rehearsal`, `production-rehearsal-down`, `tier2-e2e-up`, `tier3-evidence-up`, `evidence-bundle`, `evidence-up`, `evidence-down`, `local-dev-status`, and the prod-like targets.
  - [ ] If a needed operational command does not exist, mark the runbook row `CONCERNS` or `FAIL` with owner and mitigation. Do not invent commands such as a direct restore CLI if the implemented surface does not provide one.
- [ ] Preserve source-of-truth boundaries in incident steps. (AC: 4)
  - [ ] State that Raft/Shard metadata and committed feature state are authority for storage behavior.
  - [ ] State that Pebble Projection, Confirmed Upload Catalog, Local Block Lifecycle, Backend objects, audit, and OTel evidence have scoped roles and are not interchangeable.
  - [ ] For restore and cold-read instructions, prohibit using Backend inventory, Backend LIST/HEAD, local files, or telemetry as the consistency oracle.
  - [ ] For Block Quarantine, keep it filesystem-level Block isolation and repair; do not conflate it with Content Quarantine.
  - [ ] For Content Quarantine, keep it metadata-level Document gating through committed quarantine state; do not instruct operators to edit Block bytes.
- [ ] Make critical workflows independently runnable from documented commands and expected outputs. (AC: 3, 5)
  - [ ] Cold reads and Backend restore: document how to confirm restore-first behavior through current feature evidence, relevant make/e2e gates, `scrapctl status`, `scrapctl upload-pressure`, `scrapctl evidence bundle`, and `scrapctl fault backend break|restore` where appropriate.
  - [ ] Content Quarantine: document `scrapctl quarantine list|inspect|confirm|release|evidence`, required admin roles, expected denied-read behavior, and expected evidence report fields.
  - [ ] OpenBao fail-closed behavior: document production rehearsal checks, `scrapctl openbao bootstrap`, `make production-rehearsal-security`, and the platform-managed OpenBao boundary.
  - [ ] Backend restore failure handling: document transient Backend failure, missing/corrupt Backend object failure, no partial publish, sanitized public errors, and the owning Epic 3 evidence links.
  - [ ] For any workflow that is currently only proven by package or local evidence, mark the evidence scope explicitly; do not promote it to final release proof.
- [ ] Enforce redaction and evidence hygiene. (AC: 3, 5)
  - [ ] Do not paste credential values, private key material, generated certificate material, Document payloads, unredacted Backend-key material, unredacted Backend object names, unredacted log output, trace IDs, request IDs, auth claims, or dependency output that embeds sensitive paths.
  - [ ] Use placeholder names and bracket-split scan patterns in the evidence artifact where needed so validation commands do not self-match.
  - [ ] Add a false-positive table for any deliberate references that match release-sensitive scan terms.
  - [ ] Keep generated runtime reports under ignored `artifacts/` or `evidence/`; link paths and sanitized summaries only.
- [ ] Keep Story 6.2 inside its release-doc scope. (AC: 1-5)
  - [ ] Do not implement new production behavior, new `scrapctl` commands, new admin endpoints, alert/query references, the release evidence bundle, Tier 2/Tier 3 final evidence, real S3/IAM closure, or final V2 closure policy.
  - [ ] Do not edit `docs/prd-closure-policy.md` unless a contradiction prevents accurate runbook classification; policy updates are Story 6.7 scope.
  - [ ] Do not create an ADR unless this work changes deployment, security, auth, wire, storage, or cross-package ownership contracts.
- [ ] Run verification and update BMAD tracking. (AC: 1-5)
  - [ ] `git diff --check`
  - [ ] `make proto-check`
  - [ ] `scripts/check-e2e-gates.sh`
  - [ ] `env GOCACHE=/tmp/scrap-v2-go-build make check`
  - [ ] Run command-surface validation for all documented commands and record it in `_bmad-output/implementation-artifacts/v2-operator-runbook-evidence.md`.
  - [ ] Run redaction and authority-boundary scans over `docs/runbooks/`, this story, and the evidence artifact.
  - [ ] Update this story's Dev Agent Record and move the story to `review`; leave `done` for BMAD code review.

## Dev Notes

### Source Requirements

- FR-16 requires linked, current, reviewable evidence and operator documentation for every required release claim. Required evidence includes Tier 2 prod-like Cilium, Tier 3 evidence bundle, production security rehearsal, and real S3/IAM production rehearsal when Backend claims depend on S3. [Source: `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md#FR-16: Major-release evidence and documentation closure`]
- Operator runbooks, alert/query references, incident workflows, and evidence instructions are required unless explicitly de-scoped. Issue `#429` remains a final gate after feature scope is complete. [Source: `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md#FR-16: Major-release evidence and documentation closure`]
- DG-5 requires operator runbooks for startup/security readiness, mTLS/certificate rotation, OpenBao Transit dependency, Backend upload pressure, restore failures, eviction campaigns, Block Quarantine repair, Content Quarantine response, multi-Shard routing health, and evidence bundle collection. [Source: `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md#DG-5: Release Documentation and Evidence Standard`]
- DG-5 evidence requires command, commit/ref, environment, expected result, actual result, artifact path, timestamp, and redaction proof. [Source: `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md#DG-5: Release Documentation and Evidence Standard`]
- Normal runbooks belong under `docs/runbooks/`; no ADR is needed unless this story changes deployment, security, auth, wire, storage, or cross-package contracts. [Source: `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md#Implementation Patterns and Consistency Rules`]

### Existing Command Surface

Current `scrapctl` top-level commands, validated from `go run ./cmd/scrapctl --help` at story creation baseline, are:

```text
scrapctl <doctor|status|upload-pressure|peers|leader|fault|evidence|eviction|quarantine|openbao>
```

Use these implemented subcommands and flags as the source for examples:

| Surface | Implemented commands to cite | Source |
| --- | --- | --- |
| Common diagnostics | `scrapctl doctor`, `scrapctl status`, `scrapctl upload-pressure`, `scrapctl peers`, `scrapctl leader` | `internal/scrapctl/run.go` |
| Fault exercises | `scrapctl fault <backend|leader|projection|block> <action>` with implemented action pairs | `internal/scrapctl/fault.go` |
| Evidence | `scrapctl evidence bundle [scenario]`, `scrapctl evidence log-probe`, `scrapctl evidence pprof` | `internal/scrapctl/evidence.go` |
| Eviction | `scrapctl eviction plan`, `scrapctl eviction apply`, `scrapctl eviction status` | `internal/scrapctl/eviction.go` |
| Content Quarantine | `scrapctl quarantine list`, `inspect`, `confirm`, `release`, `evidence` | `internal/scrapctl/quarantine.go` |
| OpenBao | `scrapctl openbao bootstrap` | `internal/scrapctl/openbao.go` |

Common CLI flags include `--namespace`, `--cluster`, `--kubectl`, `--context`, `--kubeconfig`, `--docker`, `--admin-url`, `--metrics-url`, `--client-addr`, `--admin-addr`, `--timeout`, `--output text|json`, and mTLS client flags. Validate specific examples against the owning subcommand parser before committing docs. [Source: `internal/scrapctl/run.go`]

### Make Targets to Reuse

- `make production-rehearsal-security` proves local production-mode security and real OpenBao Transit with filesystem Backend only. [Source: `docs/production-rehearsal.md#Targets`; `Makefile`]
- `make production-rehearsal` requires real S3 configuration and adds real S3/IAM Backend proof. Do not let this runbook claim closure without a real non-local S3/IAM report. [Source: `docs/production-rehearsal.md#Targets`; `docs/prd-closure-policy.md#Production rehearsal path`]
- `make production-rehearsal-down` stops leftover rehearsal processes. [Source: `docs/production-rehearsal.md#Targets`]
- `make tier2-e2e-up` is the prod-like Cilium Tier 2 gate; GitHub Actions evidence is required where PRD closure policy demands it. [Source: `docs/prd-closure-policy.md#Cilium-backed Tier 2 guard`; `Makefile`]
- `make tier3-evidence-up STRESS_SCENARIO=throughput` produces Tier 3 evidence locally; GitHub Actions can run `evidence-gate.yml` for reviewable artifacts. [Source: `docs/prd-closure-policy.md#Tier 3 evidence path`; `Makefile`]
- `make evidence-bundle`, `make evidence-up`, and `make evidence-down` support local evidence Cell workflows. [Source: `Makefile`]

### Runbook-Specific Guardrails

- Startup/security readiness: document fail-closed startup checks, production/test hook boundaries, admin/peer/public mTLS readiness, audit/rate-limit policy readiness, and production rehearsal evidence. Use `docs/production-rehearsal.md` and Story 4.7 evidence as source material.
- mTLS/certificate rotation: document restart or rollout-based rotation only. Hot certificate reload is out of V2; do not imply live reload. [Source: `docs/production-rehearsal.md#Certificate Rotation`]
- OpenBao Transit dependency: SCRAP owns the client contract only. Production OpenBao lifecycle, credential custody, unseal policy, HA topology, and disaster recovery are platform-owned unless a future ADR changes ownership. [Source: `docs/openbao-deployment-contract.md#Ownership`]
- Backend upload pressure: use `scrapctl upload-pressure`, upload/outbox evidence, and Epic 3 closure artifacts. Do not treat Backend inventory as admission or consistency authority.
- Restore failures and cold reads: restore uses committed metadata and full-Block verification before serving bytes. Direct Backend streaming and Backend inventory as read authority are out of V2. [Source: `docs/adr/0027-phase-5-restore-first-cold-reads.md`; `_bmad-output/implementation-artifacts/epic-3-backend-durability-cold-read-closure-evidence.md`]
- Eviction campaigns: use `scrapctl eviction plan|apply|status`; `apply` requires explicit confirmation. Keep Local Block Lifecycle scoped to per-Member filesystem evidence and do not turn it into Document visibility authority. [Source: `internal/scrapctl/eviction.go`; `CONTEXT.md`]
- Block Quarantine repair: keep Block Quarantine distinct from Content Quarantine. Block Quarantine isolates corrupt Block files and repairs from peer `TransferBlock`; all Documents in that Block are unreadable until repair completes. [Source: `CONTEXT.md#Language`]
- Content Quarantine response: use admin HTTP plus `scrapctl quarantine` flows. Reads must fail closed with no bytes while `HeadDocument` and `FindDocuments` expose bounded scan status. [Source: `docs/adr/0025-content-quarantine-admin-surface.md`; `_bmad-output/implementation-artifacts/5-6-scrapctl-quarantine-operator-workflow.md`]
- Multi-Shard routing health: document deterministic routing, per-Shard status, wrong-Shard denial, non-zero Shard evidence, and Shard-scoped peer authorization. Do not infer Shard ownership from local files, hostnames, Backend objects, cached peer addresses, network addresses, or certificate presence. [Source: `docs/adr/0026-multi-shard-v2-release-boundary.md`; `CONTEXT.md`]
- Evidence collection: reuse `scrapctl evidence bundle`, Tier 2/Tier 3 targets, production rehearsal targets, and Story 6.1 evidence matrix semantics. This story creates runbook instructions, not the Story 6.4 release evidence bundle feature.

### Previous Story Intelligence

- Story 6.1 created `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md` and intentionally marks the overall V2 release gate `FAIL` because Epic 6 documentation/evidence gates are incomplete and issue `#429` remains open. Story 6.2 should improve the runbook gap but must not claim final release closure. [Source: `_bmad-output/implementation-artifacts/6-1-v2-release-evidence-matrix.md`]
- Story 6.1 review fixes made evidence attribution stricter: rows need owner, evidence command, artifact path, environment, timestamp, commit/ref, expected result, actual result, redaction proof, freshness decision, status, and mitigation. Use the same evidence discipline for runbook coverage. [Source: `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md`]
- Prior closure stories mark local or package-scope proof honestly. If a runbook workflow can only cite local/package evidence today, record it as local/package-scoped instead of `PASS` for final release. [Source: `_bmad-output/implementation-artifacts/epic-3-backend-durability-cold-read-closure-evidence.md`; `_bmad-output/implementation-artifacts/epic-4-production-security-rehearsal-closure-evidence.md`; `_bmad-output/implementation-artifacts/epic-5-content-safety-closure-evidence.md`]

### External Research

- GitHub code/repo search for reusable operator runbook examples returned no useful project-specific template at story creation time.
- Google SRE incident guidance reinforces keeping incident roles, working notes, escalation, mitigation, evidence preservation, and post-incident follow-up explicit, but SCRAP's ACs and repo policies are authoritative for the runbook shape. [Source: `https://sre.google/sre-book/managing-incidents/`; `https://sre.google/workbook/incident-response/`; `https://sre.google/resources/practices-and-processes/incident-management-guide/`]

### Redaction and Security Notes

- Public docs and evidence must not include credential values, private key material, generated certificate material, Document payloads, unredacted Backend-key material, unredacted Backend object names, validation secrets, unredacted log output, trace IDs, request IDs, auth claims, or sensitive dependency output.
- Evidence examples may name environment variable names such as `SCRAP_TRANSIT_TOKEN_ENV`, but must not include actual credential values.
- Prefer placeholders that cannot be mistaken for real material, for example `<redacted-openbao-credential-env>`, `<redacted-bucket-name>`, and `<redacted-document-name>`.
- If a command needs local artifact paths, cite the repo-relative artifact path and state whether it is ignored by Git. Do not paste ignored runtime contents.

### Testing Requirements

Run these gates before moving Story 6.2 to review:

```bash
git diff --check
make proto-check
scripts/check-e2e-gates.sh
env GOCACHE=/tmp/scrap-v2-go-build make check
```

Run command-surface validation and record results in `_bmad-output/implementation-artifacts/v2-operator-runbook-evidence.md`:

```bash
go run ./cmd/scrapctl --help
rg -n "usage:|case \"" internal/scrapctl
rg -n "production-rehearsal|tier2-e2e-up|tier3-evidence-up|evidence-bundle|local-dev-status" Makefile docs scripts
```

Run release-sensitive scans over the story, runbooks, and evidence artifact. Keep patterns in variables with bracket-splitting so copied commands do not self-match:

```bash
secret_shape='([a]ccess[_-]?[k]ey|[p]assword|[t]oken|PRIVATE [K]EY|BEGIN [A-Z ]*KEY)'
authority_shape='Backend [i]nventory|ListObjects|ListObject|HEAD as [a]uthority|local files as [a]uthority|telemetry as [a]uthority'
identity_shape='transaction[_-]?id=|document[_-]?name=|trace[_-]?id=|request[_-]?id=|Backend [k]ey|raw [l]og'
rg -n --pcre2 "$secret_shape" docs/runbooks _bmad-output/implementation-artifacts/6-2-operator-runbooks-for-v2-failure-domains.md _bmad-output/implementation-artifacts/v2-operator-runbook-evidence.md
rg -n --pcre2 "$authority_shape" docs/runbooks _bmad-output/implementation-artifacts/6-2-operator-runbooks-for-v2-failure-domains.md _bmad-output/implementation-artifacts/v2-operator-runbook-evidence.md
rg -n --pcre2 "$identity_shape" docs/runbooks _bmad-output/implementation-artifacts/6-2-operator-runbooks-for-v2-failure-domains.md _bmad-output/implementation-artifacts/v2-operator-runbook-evidence.md
```

Classify every match as either a required warning, a safe placeholder, or a bug to fix before review.

### Project Structure Notes

- Story file: `_bmad-output/implementation-artifacts/6-2-operator-runbooks-for-v2-failure-domains.md`.
- Expected durable docs: `docs/runbooks/*.md`.
- Expected evidence artifact: `_bmad-output/implementation-artifacts/v2-operator-runbook-evidence.md`.
- Sprint tracker: `_bmad-output/implementation-artifacts/sprint-status.yaml`.
- Keep production Go packages, protobuf contracts, generated files, deployment manifests, and release closure policy untouched unless command validation finds a doc-blocking contradiction that cannot be documented honestly.

### References

- `CONTEXT.md` - glossary and authority boundaries for Document, Transaction, Block, Frame, Shard, Cell, Member, Backend, Pebble Projection, Local Block Lifecycle, Block Quarantine, and Content Quarantine.
- `_bmad-output/planning-artifacts/epics.md` - Epic 6 and Story 6.2 source requirements.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-16 and release evidence requirements.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - DG-5 runbook scope, architecture structure, and docs placement.
- `docs/prd-closure-policy.md` - Tier 2, Tier 3, production rehearsal, real S3/IAM, and public evidence rules.
- `docs/production-rehearsal.md` - production rehearsal targets, report fields, certificate rotation, and production/non-production scope.
- `docs/openbao-deployment-contract.md` - platform-managed OpenBao ownership and SCRAP client contract.
- `docs/v2-scope-reconciliation.md` - final release gate order and issue `#429` context.
- `docs/adr/0025-content-quarantine-admin-surface.md` - Content Quarantine operator/admin surface.
- `docs/adr/0026-multi-shard-v2-release-boundary.md` - multi-Shard release boundary.
- `docs/adr/0027-phase-5-restore-first-cold-reads.md` - restore-first cold-read boundary.
- `_bmad-output/implementation-artifacts/6-1-v2-release-evidence-matrix.md` - previous Story 6.1 record and review findings.
- `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md` - release evidence row schema and current gap classification.
- `internal/scrapctl/run.go` - top-level `scrapctl` command routing and common flags.
- `internal/scrapctl/openbao.go` - `scrapctl openbao bootstrap`.
- `internal/scrapctl/quarantine.go` - `scrapctl quarantine` commands.
- `internal/scrapctl/eviction.go` - `scrapctl eviction` commands.
- `internal/scrapctl/evidence.go` - `scrapctl evidence` commands.
- `internal/scrapctl/fault.go` - fault exercise command surface.
- `internal/admin/server.go` - implemented admin HTTP routes for eviction, rewrap, and quarantine.
- `Makefile` - production rehearsal, Tier 2, Tier 3, evidence, prod-like, and local-dev targets.

## Dev Agent Record

### Agent Model Used

### Debug Log References

- 2026-06-12T18:20:00-04:00 - Story context created from Epic 6, FR-16, DG-5, Story 6.1 review lessons, closure policy, production rehearsal docs, OpenBao deployment contract, implemented `scrapctl` command surfaces, Makefile gates, repo context, and quick external runbook research.

### Completion Notes List

### File List

## Change Log

- 2026-06-12 - Created Story 6.2 context for V2 operator runbooks.
