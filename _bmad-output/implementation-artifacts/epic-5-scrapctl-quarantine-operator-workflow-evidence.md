# Epic 5 Story 5.6 Evidence: `scrapctl` Quarantine Operator Workflow

Status: pending

## Scope

Story 5.6 proves the operator CLI can list, inspect, confirm, release, and render evidence for Content Quarantine through the existing admin HTTP surface. This artifact does not close scanner runtime behavior or Epic 5; Story 5.7 owns content-safety closure evidence.

## Baseline

- Baseline commit: `c8ac14e8a803ff08c4deb4af7596d1e91ead97d5`
- Previous story evidence: `_bmad-output/implementation-artifacts/epic-5-admin-http-quarantine-operations-evidence.md`
- Admin HTTP endpoints:
  - `GET /admin/quarantine/documents`
  - `GET /admin/quarantine/document`
  - `POST /admin/quarantine/confirm`
  - `POST /admin/quarantine/release`

## Changed Boundaries

| Boundary | Change | Evidence |
| --- | --- | --- |
| `internal/scrapctl` | Pending Story 5.6 CLI command and evidence implementation. | Pending |
| `cmd/scrapctl` | Expected to remain a thin entrypoint. | Pending |
| `internal/admin` | Should remain unchanged unless a failing CLI test exposes a contract gap. | Pending |
| `internal/shard` / `internal/index` / Raft | Should remain unchanged; authority was implemented in Story 5.5. | Pending |

## Acceptance Evidence

| AC | Evidence | Result |
| --- | --- | --- |
| AC-5.6.1 | CLI list/inspect output and redaction proof. | Pending |
| AC-5.6.2 | CLI confirm/release admin HTTP route proof and typed outcome/failure proof. | Pending |
| AC-5.6.3 | Evidence report path, stdout/stderr/report leak checks, and artifact proof. | Pending |

## Verification Log

Pending implementation.

## Redaction Checks

Pending implementation.

## Final Gate

Pending implementation.
