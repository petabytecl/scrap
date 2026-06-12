# Epic 5 Story 5.5 Evidence: Admin HTTP Quarantine Operations

Status: PLANNED

Baseline commit: `de5fbbab49117ca2395fd7c0424dc402cbf4eaa3`
Story: `_bmad-output/implementation-artifacts/5-5-admin-http-quarantine-operations.md`

## Scope

Story 5.5 adds admin HTTP operations for active Content Quarantine response.
Closure is limited to:

- Listing active quarantined Documents through admin HTTP.
- Inspecting one active quarantined Document through admin HTTP.
- Confirming quarantine through committed Raft authority.
- Releasing quarantine through committed Raft authority.
- Proving admin authz, rate limits, audit, redaction, and post-release read behavior.

Out of scope for this evidence:

- `scrapctl` quarantine operator UX.
- Scanner engine runtime dependencies.
- Block Quarantine, Deep Scrub repair, `.blk`/`.idx` mutation, or Backend object mutation.
- Epic 5 closure.

## Changed Boundaries

| Boundary | Change |
| --- | --- |
| `proto/scrap/v1/raft.proto` | Planned additive metadata-only confirm/release commands. |
| `gen/go/scrap/v1/raft.pb.go` | Planned regenerated output from proto source. |
| `internal/index` | Planned bounded list/inspect/confirm metadata/release helpers for active Content Quarantine state. |
| `internal/shard` | Planned Raft-authoritative confirm/release lifecycle operations and admin-facing query methods. |
| `internal/admin` | Planned HTTP quarantine endpoints, bounded JSON, authz, rate-limit, and audit integration. |
| `internal/cmd` | Planned admin service wiring and multi-Shard routing adapter updates. |
| BMAD artifacts | Track story evidence and local verification. |

## Public/Admin Contract Summary

Expected admin behavior:

- `GET /admin/quarantine/documents` returns bounded active quarantine metadata, scoped to local Shards unless filtered by Transaction route.
- `GET /admin/quarantine/document` with `transaction_id` and `document_name` query parameters returns one active quarantine record or a bounded not-found/precondition response.
- `POST /admin/quarantine/confirm` records a true-positive lifecycle decision through committed Raft state and keeps reads denied.
- `POST /admin/quarantine/release` removes the active read gate only after committed Raft state converges.
- Admin responses return no Document bytes and no scanner payloads, signatures, YARA/ClamAV rule text, local paths, Backend keys, trace IDs, request IDs, auth claims, or free-form operator notes.

## Verification Plan

| Area | Command / Evidence | Result |
| --- | --- | --- |
| Proto compatibility | `make proto-check` | PENDING |
| Focused quarantine/admin tests | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/shard ./internal/admin ./internal/cmd -run 'Quarantine|Admin|Audit|RateLimit|Authorization|ReadDocument' -count=1` | PENDING |
| Targeted packages | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/store ./internal/server ./internal/shard ./internal/index ./internal/admin ./internal/cmd ./internal/scrapctl -count=1` | PENDING |
| Static diff check | `git diff --check`; `git diff --cached --check` | PENDING |
| E2E gate policy | `scripts/check-e2e-gates.sh` | PENDING |
| Broad local gate | `env GOCACHE=/tmp/scrap-v2-go-build make check` | PENDING |
| Secret shape scan | `rg -n --pcre2 "$secret_shape_pattern" $scan_scope` | PENDING |
| Quarantine-sensitive scan | `rg -n --pcre2 "$quarantine_sensitive_pattern" $scan_scope` | PENDING |

## Authority and Security Evidence

| Scenario | Evidence | Result |
| --- | --- | --- |
| List/inspect returns bounded metadata only | TBD | PENDING |
| Admin authz denies before side effects | TBD | PENDING |
| Admin rate limits apply before side effects | TBD | PENDING |
| Audit events are bounded | TBD | PENDING |
| Confirm converges through Raft | TBD | PENDING |
| Release changes reads only after committed apply | TBD | PENDING |
| Corrupt quarantine state fails closed | TBD | PENDING |

## Redaction Notes

Story 5.5 admin responses may include validated `transaction_id` and
`document_name` because the operator workflow requires exact identity. Logs,
audit, metrics, traces, and evidence must not retain raw Document identity.

Story 5.5 must not expose Document bytes, scanner rule text, raw signature
names, clamd/YARA dependency logs, filesystem paths, Backend keys, trace IDs,
request IDs, gRPC metadata, auth claims, or unbounded operator payloads.

## Final Decision

| Acceptance Criterion | Decision | Evidence |
| --- | --- | --- |
| AC-5.5.1 | PENDING | TBD |
| AC-5.5.2 | PENDING | TBD |
| AC-5.5.3 | PENDING | TBD |
