# V2 Operator Runbook Evidence

Status: complete-with-release-scope-concerns
Generated: 2026-06-12T18:25:26-04:00
Baseline commit: cb5bfbf075e2b8de22217098ce7c7844677b09a3
Branch: v2

## Scope

Story 6.2 creates the V2 operator runbook surface and validates that the docs
use implemented commands, preserve authority boundaries, and keep public
evidence redacted. It does not implement new `scrapctl` commands, admin
endpoints, alert/query references, release evidence bundle behavior, Tier 2 or
Tier 3 final evidence, real S3/IAM closure, or final V2 closure policy.

Overall V2 release readiness remains blocked by later Epic 6 stories and issue
`#429`. This artifact is a Story 6.2 runbook decision, not a final V2 release
decision.

## Source Inputs

| Source | Use |
| --- | --- |
| `_bmad-output/planning-artifacts/epics.md` | Story 6.2 ACs and required runbook domains |
| `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` | FR-16 evidence and documentation closure |
| `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` | DG-5 runbook scope and docs placement |
| `CONTEXT.md` | glossary and authority boundaries |
| `docs/prd-closure-policy.md` | Tier 2, Tier 3, production rehearsal, and public evidence rules |
| `docs/production-rehearsal.md` | production rehearsal targets and certificate rotation |
| `docs/openbao-deployment-contract.md` | platform-managed OpenBao boundary |
| `docs/adr/0025-content-quarantine-admin-surface.md` | Content Quarantine admin and `scrapctl` surface |
| `docs/adr/0026-multi-shard-v2-release-boundary.md` | multi-Shard release boundary |
| `docs/adr/0027-phase-5-restore-first-cold-reads.md` | restore-first cold-read boundary |
| `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md` | release evidence row schema and remaining gaps |

## Runbook Checklist

All runbooks contain purpose, owning source, symptoms, normal path, failure
path, rollback or escalation, expected outputs, evidence collection, redaction
requirements, authority-boundary note, and references.

| Runbook | Owner/source | Command validation | Authority review | Redaction review | Workflow validation | Status |
| --- | --- | --- | --- | --- | --- | --- |
| `docs/runbooks/v2-startup-security-readiness.md` | Epic 4, FR-9, FR-16 | `scrapctl doctor`, `scrapctl status`, `make production-rehearsal-security` are implemented | Does not treat readiness as storage authority | Placeholder-only examples | Runnable from `scrapctl` and make targets | PASS |
| `docs/runbooks/v2-mtls-certificate-rotation.md` | Epic 4, production rehearsal docs | `kubectl rollout status`, `scrapctl status`, `make production-rehearsal-security` are implemented surfaces | Certificate validity is not Shard/storage authority | No credential values or cert material | Restart/roll rotation only; hot reload out of scope | PASS |
| `docs/runbooks/v2-openbao-transit-dependency.md` | Epic 4, FR-10, FR-14 | `scrapctl openbao bootstrap`, `make production-rehearsal-security` are implemented | OpenBao owns dependency proof, not storage authority | Credential placeholders only | Local/prod-like bootstrap and fail-closed production dependency are documented | PASS |
| `docs/runbooks/v2-backend-upload-pressure.md` | Epic 3, FR-6 | `scrapctl upload-pressure`, `status`, `evidence bundle`, `fault backend break|restore` are implemented | Does not use Backend listings as upload authority | No object names or dependency output | Pressure diagnosis and controlled non-production fault exercise documented | PASS |
| `docs/runbooks/v2-restore-failures.md` | Epic 3, FR-7, FR-8, ADR 0027 | `scrapctl status`, `upload-pressure`, `evidence bundle`, `fault backend break|restore`, `make e2e-up` are implemented surfaces | Restore follows committed metadata and full-Block verification | No Document or Backend object names | Cold-read and Backend restore workflow documented with scoped proof caveat | PASS |
| `docs/runbooks/v2-eviction-campaigns.md` | Epic 3, FR-7 | `scrapctl eviction plan|apply|status` are implemented | Local Block Lifecycle remains scoped filesystem evidence | Plan placeholders only | Plan, apply, status, and post-eviction restore escalation documented | PASS |
| `docs/runbooks/v2-block-quarantine-repair.md` | Epics 1-2, FR-3 | `scrapctl status`, `evidence bundle`, `fault block corrupt` are implemented surfaces | Block Quarantine remains filesystem-level isolation, not Content Quarantine | No file paths or object names | Repair response is automatic/peer-transfer oriented with escalation if not converging | PASS |
| `docs/runbooks/v2-content-quarantine-response.md` | Epic 5, FR-11, FR-12, ADR 0025 | `scrapctl quarantine list|inspect|confirm|release|evidence` are implemented | Content Quarantine is metadata-level Document gate | Redacted identity placeholders | Admin and `scrapctl` workflow independently runnable | PASS |
| `docs/runbooks/v2-multi-shard-routing-health.md` | Epic 2, FR-5, ADR 0026 | `scrapctl status`, `peers`, `leader`, `doctor`, `make tier2-e2e-up` are implemented surfaces | Shard ownership is not inferred from local/network/cert presence | No sensitive peer addresses | Multi-Shard health and non-zero Shard proof path documented | PASS |
| `docs/runbooks/v2-evidence-collection.md` | Epic 6, FR-15, FR-16 | `scrapctl evidence bundle`, `make tier2-e2e-up`, `make tier3-evidence-up`, production rehearsal targets are implemented | Evidence is explicitly not storage authority | Public tracker redaction rules included | Release evidence collection runnable while final closure remains out of scope | PASS |

## Command Validation

| Command/source | Validation result | Evidence |
| --- | --- | --- |
| `go run ./cmd/scrapctl --help` | PASS | Reports `scrapctl <doctor|status|upload-pressure|peers|leader|fault|evidence|eviction|quarantine|openbao>` |
| `internal/scrapctl/run.go` | PASS | Top-level routing includes all documented `scrapctl` commands |
| `internal/scrapctl/openbao.go` | PASS | Implements `openbao bootstrap` and redacted evidence output |
| `internal/scrapctl/quarantine.go` | PASS | Implements `list`, `inspect`, `confirm`, `release`, `evidence` |
| `internal/scrapctl/eviction.go` | PASS | Implements `plan`, `apply`, `status` |
| `internal/scrapctl/evidence.go` | PASS | Implements `bundle`, `log-probe`, `pprof` |
| `internal/scrapctl/fault.go` | PASS | Implements `backend break|restore`, `leader delete`, `projection inject`, `block corrupt` |
| `Makefile` | PASS | Contains production rehearsal, Tier 2, Tier 3, evidence, prod-like, and local-dev targets referenced by runbooks |

## Authority-Boundary Review

| Boundary | Decision |
| --- | --- |
| Backend object listings | Runbooks warn against using listings or object existence checks as consistency authority |
| Local member files | Runbooks warn against manual local file edits or local file state as Document visibility authority |
| Telemetry/evidence | Runbooks state evidence proves observation only and does not override committed Shard state |
| Block Quarantine vs Content Quarantine | Runbooks keep filesystem-level Block isolation separate from metadata-level Document gating |
| OpenBao | Runbooks keep production OpenBao lifecycle platform-owned and SCRAP-owned scope limited to client contract and fail-closed dependency behavior |

## Redaction Review

| Surface | Result |
| --- | --- |
| Runbook command examples | PASS: placeholder values only |
| Evidence artifact | PASS: no credential values, private key material, generated cert material, Document payloads, Backend object names, unredacted dependency output, request IDs, trace IDs, or auth claims |
| Runtime artifacts | PASS: docs instruct operators to keep runtime reports under ignored `artifacts/` or `evidence/` paths and attach sanitized summaries |

False-positive scan classification:

| Location | Pattern class | Classification |
| --- | --- | --- |
| `docs/runbooks/v2-openbao-transit-dependency.md` | credential-shaped word | Safe implemented flag name `--[t]oken-env`; no credential value present |
| `_bmad-output/implementation-artifacts/6-2-operator-runbooks-for-v2-failure-domains.md` | authority-boundary wording | Required AC/source prose and bracket-split scan pattern; no instruction uses those surfaces as authority |

## Current Verification

| Command | Environment | Result | Notes |
| --- | --- | --- | --- |
| `go run ./cmd/scrapctl --help` | local checkout, branch `v2`, baseline `cb5bfbf` | PASS | Validated top-level command list before writing runbooks |
| `rg -n "usage:|case \"" internal/scrapctl` | local checkout | PASS | Validated documented subcommands against source |
| `rg -n "production-rehearsal|tier2-e2e-up|tier3-evidence-up|evidence-bundle|local-dev-status" Makefile docs scripts` | local checkout | PASS | Validated referenced make targets and docs |
| `git diff --check` | local checkout | PASS | No whitespace errors |
| `make proto-check` | local checkout | PASS | Buf lint/generate left `gen/` clean |
| `scripts/check-e2e-gates.sh` | local checkout | PASS | E2E gate wiring checks passed |
| `env GOCACHE=/tmp/scrap-v2-go-build make check` | local checkout | PASS | Format, package-boundary, lint, all Go tests, race tests, integration-tagged tests, and builds passed |
| Release-sensitive scans | local checkout | PASS with classified safe matches | Safe matches: implemented `--[t]oken-env` flag name and required authority-boundary AC/source prose |

## Remaining Release Scope

- Story 6.3 owns alert and query references.
- Story 6.4 owns the release evidence bundle behavior.
- Story 6.5 owns Tier 2 and Tier 3 release evidence gates.
- Story 6.6 and issue `#429` own real S3/IAM production rehearsal closure.
- Story 6.7 owns final V2 closure policy and final gate decision.
