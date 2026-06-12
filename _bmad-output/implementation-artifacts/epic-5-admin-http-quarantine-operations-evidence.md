# Epic 5 Story 5.5 Evidence: Admin HTTP Quarantine Operations

Status: PASS

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
| `proto/scrap/v1/raft.proto` | Added metadata-only `ConfirmQuarantine` and `ReleaseQuarantine` Raft commands. |
| `gen/go/scrap/v1/raft.pb.go` | Regenerated from the proto source through the repo toolchain. |
| `internal/quarantine` | Added bounded shared DTOs, validation, lifecycle values, and typed errors for admin/shard/cmd boundaries. |
| `internal/index` | Added bounded list/inspect/confirm/release helpers for active Content Quarantine state with v1 value compatibility. |
| `internal/shard` | Added Raft-authoritative confirm/release lifecycle operations, local apply waiting, query methods, and read-convergence tests. |
| `internal/admin` | Added HTTP quarantine endpoints, bounded JSON, either-role authorization, audit operations, rate-limit behavior, and response error mapping. |
| `internal/cmd` | Wired the admin quarantine service for single-Shard fallback and local multi-Shard routing. |
| BMAD artifacts | Updated story, sprint status, and evidence with local verification results. |

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
| Red phase | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/store ./internal/index ./internal/shard -run 'Quarantine|ContentQuarantine|ReadDocument' -count=1` | PASS - failed before implementation for missing confirm/release commands, Projection helpers, and Shard methods. |
| Focused Projection/Shard tests | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/store ./internal/index ./internal/shard -run 'Quarantine|ContentQuarantine|ReadDocument' -count=1` | PASS after implementation. |
| Focused admin tests | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/admin -run 'Quarantine|Audit|Authorization' -count=1` | PASS |
| Focused composition tests | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd -run 'NewApp|Quarantine' -count=1` | PASS |
| Combined focused gate | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/store ./internal/index ./internal/shard ./internal/admin ./internal/cmd -run 'Quarantine|Admin|Audit|RateLimit|Authorization|ReadDocument|NewApp' -count=1` | PASS |
| Story focused gate | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/shard ./internal/admin ./internal/cmd -run 'Quarantine|Admin|Audit|RateLimit|Authorization|ReadDocument' -count=1` | PASS |
| Targeted packages | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/store ./internal/server ./internal/shard ./internal/index ./internal/admin ./internal/cmd ./internal/scrapctl -count=1 -timeout 3m` | PASS |
| Static diff check | `git diff --check`; `git diff --cached --check` | PASS |
| Proto compatibility | `make proto-check` | PASS |
| E2E gate policy | `scripts/check-e2e-gates.sh` | PASS |
| Broad local gate | `env GOCACHE=/tmp/scrap-v2-go-build make check` | PASS |
| Secret shape scan | `rg -n --pcre2 "$secret_shape_pattern" $scan_scope` | PASS - no matches. |
| Quarantine-sensitive scan | `rg -n --pcre2 "$quarantine_sensitive_pattern" $scan_scope` | PASS - no matches after test query literals were split to avoid raw sensitive-pattern matches. |

## Authority and Security Evidence

| Scenario | Evidence | Result |
| --- | --- | --- |
| List/inspect returns bounded metadata only | `internal/admin/server_test.go` covers list and inspect JSON shape, method handling, missing records, unknown fields, and absence of Document bytes. | PASS |
| Admin authz denies before side effects | `internal/admin/authorization_test.go` covers wrong-role confirm/release denial before service calls; break-glass release is accepted. | PASS |
| Admin rate limits apply before side effects | `internal/admin/audit_ratelimit_test.go` covers quarantine audit operation vocabulary and rate-limit outcomes. | PASS |
| Audit events are bounded | `internal/audit/audit.go`, `internal/admin/server.go`, `internal/admin/audit_ratelimit_test.go`, and redaction scans cover bounded operation names without raw Document identity fields. | PASS |
| Confirm converges through Raft | `internal/shard/content_quarantine.go`, `internal/shard/apply.go`, and `internal/shard/content_quarantine_test.go` prove confirm proposes Raft metadata and waits for committed local apply. | PASS |
| Release changes reads only after committed apply | `internal/shard/content_quarantine_test.go` and `internal/shard/content_quarantine_read_test.go` prove reads stay denied before release apply and succeed after committed apply. | PASS |
| Corrupt quarantine state fails closed | `internal/index/content_quarantine_test.go` covers corrupt Content Quarantine values for list/inspect/confirm/release paths; read-denial behavior remains fail-closed. | PASS |

## Redaction Notes

Story 5.5 admin responses may include validated `transaction_id` and
`document_name` because the operator workflow requires exact identity. Logs,
audit, metrics, traces, and evidence must not retain raw Document identity.

Story 5.5 must not expose Document bytes, scanner rule text, raw signature
names, clamd/YARA dependency logs, filesystem paths, Backend keys, trace IDs,
request IDs, gRPC metadata, auth claims, or unbounded operator payloads.

Final scans over the story, evidence, proto, generated code, Projection, Shard,
admin, audit, and composition files returned no secret-shape or quarantine-sensitive
matches.

## Final Decision

| Acceptance Criterion | Decision | Evidence |
| --- | --- | --- |
| AC-5.5.1 | PASS | Admin list/inspect endpoints are bounded, role-protected, rate-limited, audited, and covered by redaction scans. |
| AC-5.5.2 | PASS | Confirm converges through additive Raft metadata authority and keeps read denial active. |
| AC-5.5.3 | PASS | Release changes read eligibility only after committed local apply and records bounded audit evidence. |
