---
baseline_commit: 29ec066ae79f0d7692d82f4737d07432f5eb0e79
---

# Story 5.6: `scrapctl` Quarantine Operator Workflow

Status: review

## Story

As a security operator,
I want `scrapctl` commands for quarantine response,
so that operators can inspect, confirm, release, and collect evidence without raw API calls.

## Acceptance Criteria

1. **AC-5.6.1 - CLI list and inspect are glossary-correct and redacted by default.** Given quarantine state exists, when `scrapctl` lists or inspects quarantine, then output uses exact glossary terms and redacts raw identifiers by default. Evidence includes CLI output and redaction proof.
2. **AC-5.6.2 - CLI confirm and release route through admin HTTP authority.** Given an operator confirms or releases quarantine, when `scrapctl` invokes admin HTTP, then the command reports the committed outcome or a typed failure. Evidence proves CLI operations route through admin HTTP and Raft-owned authority.
3. **AC-5.6.3 - CLI evidence output is complete and leak-checked.** Given evidence output is requested, when `scrapctl` renders quarantine evidence, then it includes command, result, artifact path, and redaction proof. Evidence records stdout, stderr, and report leak checks.

## Tasks / Subtasks

- [x] Create Story 5.6 evidence artifact before code changes. (AC: 1-3)
  - [x] Use `_bmad-output/implementation-artifacts/epic-5-scrapctl-quarantine-operator-workflow-evidence.md`.
  - [x] Record baseline commit `c8ac14e8a803ff08c4deb4af7596d1e91ead97d5`, changed boundaries, CLI command contract, admin HTTP routing proof, redaction checks, and final `PASS`/`CONCERNS`/`FAIL` rows.
  - [x] Keep closure scoped to Story 5.6. Do not claim scanner runtime closure or Epic 5 closure; Story 5.7 owns content-safety closure evidence.

- [x] Add `scrapctl quarantine` command group using existing CLI patterns. (AC: 1-3)
  - [x] Register `quarantine` in `scrapctlUsage` and `runBuiltInCommand` in `internal/scrapctl/run.go`.
  - [x] Add a focused `internal/scrapctl/quarantine.go`; add `internal/scrapctl/quarantine_test.go`.
  - [x] Implement subcommands:
    - `scrapctl quarantine list`
    - `scrapctl quarantine inspect`
    - `scrapctl quarantine confirm`
    - `scrapctl quarantine release`
    - `scrapctl quarantine evidence`
  - [x] Reuse `parseCommon`, `newFlagSet`, `commandContext`, `withHTTPClientTLS`, `writeJSON`, and existing text-output style. Do not introduce Cobra, pflag, or another CLI framework.

- [x] Implement admin HTTP client calls only. (AC: 2)
  - [x] `list` sends `GET /admin/quarantine/documents` with optional `transaction_id` and `limit` query values.
  - [x] `inspect` sends `GET /admin/quarantine/document` with required `transaction_id` and `document_name` query values.
  - [x] `confirm` sends `POST /admin/quarantine/confirm` with JSON body `{ "transaction_id": "...", "document_name": "..." }`.
  - [x] `release` sends `POST /admin/quarantine/release` with the same JSON body.
  - [x] Route all calls through `--admin-url` and existing TLS flags. Do not import `internal/shard`, `internal/index`, or scanner packages from `internal/scrapctl`.
  - [x] Decode `internal/quarantine` DTOs (`Record`, `Result`, `ListFilter`, `Identity`) or local wire-compatible DTOs. Keep authority in admin HTTP, Shard, Raft, and Projection.

- [x] Add default-redacted operator output. (AC: 1-3)
  - [x] Default text and JSON output must not print raw `transaction_id` or `document_name`; render stable redacted identifiers instead, such as bounded digests and explicit labels `Transaction` and `Document`.
  - [x] Preserve operator usefulness by printing bounded fields: `Shard`, `Block`, lifecycle, scan type, reason, detected/confirmed timestamps, changed flag, and typed status/reason.
  - [x] If a raw-output escape hatch is added, make it opt-in, clearly named, excluded from evidence defaults, and covered by tests. Prefer no raw-output option unless implementation proves it is needed.
  - [x] Never render Document bytes, scanner signatures, YARA rule text, raw dependency logs, trace IDs, request IDs, auth claims, operator notes, local file paths, Backend object identifiers, or credential material.

- [x] Report committed outcomes and typed failures. (AC: 2)
  - [x] A successful confirm/release must display the `quarantine.Result` status, reason, changed flag, lifecycle, and bounded Document identity proof returned by admin HTTP.
  - [x] A failed HTTP response must surface a typed bounded failure using the response `reason` when present; sanitize fallback body text before returning an error.
  - [x] Map expected admin reasons without leaking implementation detail: `invalid_request`, `not_found`, `permission_denied`, `rate_limited`, `method_not_allowed`, `not_leader`, `unavailable`, `failed_precondition`, `data_loss`, `audit_failed`, and `internal_error`.
  - [x] For failed `quarantine.Result` responses, return a non-nil command error after writing bounded output, matching the eviction apply pattern.

- [x] Implement quarantine evidence rendering. (AC: 3)
  - [x] Add `--evidence-path` to `scrapctl quarantine evidence`.
  - [x] Evidence report must include command, sanitized args, admin URL label, result summary, artifact path, changed boundaries, stdout/stderr redaction checks, report redaction check, and route proof that operations used admin HTTP endpoints.
  - [x] Write JSON evidence with explicit file permissions, create parent directories safely, and sync file/directory following the OpenBao evidence pattern.
  - [x] Evidence defaults must use redacted output and pass leak checks over stdout, stderr, and report content.

- [x] Add tests before implementation and keep them narrow. (AC: 1-3)
  - [x] Add tests that prove `list` and `inspect` call the exact admin paths/methods and do not leak raw identifiers in text or JSON output.
  - [x] Add tests that prove `confirm` and `release` POST the expected JSON identity body, route through admin HTTP, and report committed lifecycle outcomes.
  - [x] Add tests that prove typed failures return non-zero command errors without leaking raw response bodies or dependency strings.
  - [x] Add tests for evidence report creation, file mode, sanitized args, stdout/stderr/report redaction checks, and route proof.
  - [x] Add parse/validation tests for missing identity, invalid limit, unsupported subcommand, unsupported output, and missing evidence path.

## Dev Notes

### Current State and Reuse Points

- `cmd/scrapctl/main.go` is intentionally thin and delegates to `scrapctl.Run`; do not move CLI logic into `cmd/`.
- `internal/scrapctl/run.go` owns command routing, common flags, default admin URL, default timeout, TLS flags, and the `Deps` injection surface used by tests.
- `internal/scrapctl/eviction.go` is the closest admin HTTP command pattern: it parses subcommands with stdlib `flag`, calls admin HTTP through `withHTTPClientTLS`, decodes JSON, sanitizes operator-safe output, and returns command errors for failed outcomes.
- `internal/scrapctl/openbao_report.go` is the closest evidence-report pattern: it renders text/JSON, writes evidence files with explicit permissions, records redaction checks, and verifies forbidden values before writing successful output.
- `internal/scrapctl/output.go` contains existing text sanitization helpers. Extend or add quarantine-specific redaction locally; do not weaken generic diagnostic redaction.
- `internal/quarantine/types.go` defines the bounded admin wire shape and constants: `Record`, `Result`, `ListFilter`, `Identity`, `DefaultListLimit`, `MaxListLimit`, status, lifecycle, and reason strings.
- `internal/admin/quarantine.go` owns the HTTP endpoints. It validates duplicate query params, strict JSON bodies, auth/rate-limit/audit, JSON-only errors, and maps store errors to bounded quarantine reasons.
- Existing tests use `roundTripFunc` HTTP clients rather than real listeners for CLI HTTP calls. Continue that pattern unless `httptest.Server` is needed to prove URL handling.

### Authority and Boundary Rules

- `scrapctl` is an operator surface, not authority. It must not decide quarantine state from local files, scanner memory, Projection internals, Shard imports, Backend object existence, or Block Quarantine state.
- Confirm and release are authoritative only after admin HTTP returns the Shard/Raft-backed result from Story 5.5. The CLI must report that result; it must not synthesize success.
- Keep Content Quarantine separate from Block Quarantine and Deep Scrub repair. The admin `/healthz` field `quarantined_blocks` is Block Quarantine health, not Content Quarantine count.
- Do not add a new admin gRPC surface. ADR 0025 amends ADR 0008: V2 quarantine management uses admin HTTP plus `scrapctl`.
- Do not change proto, Raft, Shard, Projection, or admin-handler contracts for this story unless a failing test proves the Story 5.5 contract cannot support CLI acceptance criteria.

### Output Contract

- Text output should use exact glossary terms: `Content Quarantine`, `Document`, `Transaction`, `Shard`, `Block`, and `Raft`.
- Default output must redact raw Document identity values. A digest is acceptable only if bounded, deterministic for one run, and not reversible from output alone.
- JSON output is still operator output and must follow the same default redaction rule. Do not expose raw fields in JSON just because the admin API returns them.
- HTTP request bodies can contain raw identity input because the admin API requires it; evidence and command output must not echo those raw values by default.

### Command Shape

Use existing common flags on every subcommand unless a narrower parser proves simpler:

```text
scrapctl quarantine list --admin-url=http://127.0.0.1:18100 [--transaction-id=<id>] [--limit=50] [--output=text|json]
scrapctl quarantine inspect --admin-url=http://127.0.0.1:18100 --transaction-id=<id> --document-name=<name> [--output=text|json]
scrapctl quarantine confirm --admin-url=http://127.0.0.1:18100 --transaction-id=<id> --document-name=<name> [--output=text|json]
scrapctl quarantine release --admin-url=http://127.0.0.1:18100 --transaction-id=<id> --document-name=<name> [--output=text|json]
scrapctl quarantine evidence --admin-url=http://127.0.0.1:18100 --evidence-path=<path> [--output=text|json]
```

If implementation needs `--confirm` for dangerous CLI actions, add it consistently to confirm and release and test that missing confirmation fails before HTTP side effects. Do not add free-form operator notes.

### Previous Story Intelligence

- Story 5.5 created and reviewed the admin endpoints this story must consume. Review fixes in `c8ac14e` matter for this story: JSON-only auth/rate-limit/method errors, strict JSON decision bodies, duplicate-query rejection, bounded route/unavailable mapping, idempotent confirm, release lifecycle reporting, and Shard 0 JSON metadata.
- Story 5.5 evidence proved admin authorization, rate limits, audit, Raft convergence, replay/reopen behavior, and post-release read behavior. Story 5.6 should reference that authority evidence but still prove CLI routing and redacted operator output.
- Recent quarantine commits:
  - `c8ac14e fix: address quarantine admin review findings`
  - `42eca44 feat: add admin quarantine operations`
  - `a20b9a1 docs: create story 5.5 quarantine admin`
  - `de5fbba fix: address quarantined read review findings`
  - `414eef7 feat: deny quarantined document reads`

### External Research

- GitHub code search for public Go quarantine/admin/evidence CLIs did not reveal a reusable implementation that fits this repo's authority and redaction model.
- Exa research reinforced general Go CLI guidance already matched locally: use contextual HTTP requests, support JSON output, test HTTP clients with local fakes or `httptest`, and avoid unnecessary dependencies when stdlib covers flags, HTTP, JSON, and file I/O.
- `go list -m` shows Cobra and pflag are already transitive dependencies, but `cmd/scrapctl` currently uses stdlib `flag`. Do not adopt Cobra/pflag for this story.

### Project Structure Notes

- Expected touched files:
  - `internal/scrapctl/run.go`
  - `internal/scrapctl/quarantine.go`
  - `internal/scrapctl/quarantine_test.go`
  - `internal/scrapctl/output.go` only if shared redaction helpers need small extension
  - `cmd/scrapctl/main_test.go` only if command usage or top-level routing coverage requires it
  - `_bmad-output/implementation-artifacts/5-6-scrapctl-quarantine-operator-workflow.md`
  - `_bmad-output/implementation-artifacts/epic-5-scrapctl-quarantine-operator-workflow-evidence.md`
- Avoid unrelated refactors in `internal/scrapctl/evidencebundle`, `internal/admin`, `internal/shard`, `internal/index`, `proto/`, and generated files.
- No UX artifact was present under `_bmad-output/planning-artifacts`; CLI operator UX is governed by the PRD, architecture, ADRs, and existing `scrapctl` style.

### Testing

Run focused tests first:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl ./cmd/scrapctl -run 'Quarantine|Evidence|Redact|Usage' -count=1
```

Then run the adjacent admin/CLI package gate:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/admin ./internal/quarantine ./internal/scrapctl ./cmd/scrapctl -run 'Quarantine|Admin|Audit|Authorization|RateLimit|Evidence|Redact' -count=1
```

Before review, run:

```bash
git diff --check
make proto-check
scripts/check-e2e-gates.sh
env GOCACHE=/tmp/scrap-v2-go-build make check
```

Run leak scans over story, evidence, and touched CLI/quarantine code:

```bash
credential_scan_pattern='([A]KIA|[A]SIA|[B]EGIN (RSA|EC|OPENSSH|PRIVATE) [K]EY|[p]assword|[p]asswd|[s]ecret|[t]oken|api[_-]?[k]ey|client[_-]?[s]ecret)'
scan_scope='_bmad-output/implementation-artifacts/5-6-scrapctl-quarantine-operator-workflow.md _bmad-output/implementation-artifacts/epic-5-scrapctl-quarantine-operator-workflow-evidence.md internal/scrapctl internal/quarantine'
rg -n --pcre2 "$credential_scan_pattern" $scan_scope

quarantine_sensitive_pattern='([t]ransaction_id=|[d]ocument_name=|[i]dempotency[_-]?[k]ey=|Backend [k]ey:|trace[_-]?[i]d=|request[_-]?[i]d=|[s]ignature=|[r]ule=|clamd_[e]rror=|yara_[e]rror=|[f]ile[_-]?[p]ath=|operator_[n]ote=)'
rg -n --pcre2 "$quarantine_sensitive_pattern" $scan_scope
```

## References

- `CONTEXT.md` - glossary, immutable Document model, Content Quarantine versus Block Quarantine vocabulary.
- `_bmad-output/project-context.md` - repo package boundaries, testing rules, evidence and redaction rules.
- `_bmad-output/planning-artifacts/epics.md` - Epic 5 and Story 5.6 acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-12 Content Quarantine read gate and admin operations.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - DG-1 scanner/quarantine architecture, boundary map, and `internal/scrapctl` ownership.
- `docs/adr/0008-async-content-scanning-architecture.md` - Content Quarantine store, read behavior, and original admin operations.
- `docs/adr/0025-content-quarantine-admin-surface.md` - accepted admin HTTP plus `scrapctl` decision and security requirements.
- `_bmad-output/implementation-artifacts/5-5-admin-http-quarantine-operations.md` - previous story implementation record and review fixes.
- `_bmad-output/implementation-artifacts/epic-5-admin-http-quarantine-operations-evidence.md` - admin HTTP/Raft authority evidence to reference from CLI evidence.
- `internal/scrapctl/eviction.go` and `internal/scrapctl/eviction_test.go` - admin HTTP CLI pattern and sensitive-output tests.
- `internal/scrapctl/openbao_report.go` and `internal/scrapctl/openbao_bootstrap_test.go` - evidence report and redaction-check pattern.
- `internal/admin/quarantine.go` and `internal/admin/server_test.go` - quarantine endpoint contract and bounded error mapping.
- `internal/quarantine/types.go` - DTOs and bounded reason/lifecycle constants.

## Dev Agent Record

### Agent Model Used

GPT-5 Codex

### Debug Log References

- 2026-06-12T16:33:23-04:00 - Story created from sprint backlog after Story 5.5 commit `c8ac14e8a803ff08c4deb4af7596d1e91ead97d5`.
- 2026-06-12T16:37:15-04:00 - Story moved to in-progress with baseline commit `29ec066ae79f0d7692d82f4737d07432f5eb0e79`.
- 2026-06-12T16:40:00-04:00 - RED phase confirmed: focused quarantine tests failed because `scrapctl quarantine` was not registered.
- 2026-06-12T16:46:57-04:00 - Final `make check` passed after formatter/lint fixes.

### Implementation Plan

- Add a focused `internal/scrapctl/quarantine.go` using existing stdlib `flag`, admin HTTP, TLS, JSON, text-output, and evidence-report patterns.
- Keep `scrapctl` as an operator client only; do not import Shard, Projection, Raft, scanner, or admin internals.
- Redact admin-returned Document identity fields before any text, JSON, or evidence output.
- Prove list/inspect/confirm/release/evidence behavior with local HTTP round-tripper tests.

### Completion Notes List

- Implemented `scrapctl quarantine list|inspect|confirm|release|evidence`.
- CLI calls the Story 5.5 admin HTTP endpoints and decodes `internal/quarantine` DTOs without adding a new dependency or authority path.
- Default text, JSON, error, and evidence output use bounded redacted Document identity values.
- Evidence command writes a JSON report with artifact path, route proof, sanitized args, changed boundaries, and stdout/stderr/report redaction checks.
- Full local verification passed, including `make check`.

### File List

- `_bmad-output/implementation-artifacts/5-6-scrapctl-quarantine-operator-workflow.md`
- `_bmad-output/implementation-artifacts/epic-5-scrapctl-quarantine-operator-workflow-evidence.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `cmd/scrapctl/main_test.go`
- `internal/scrapctl/doctor_test.go`
- `internal/scrapctl/run.go`
- `internal/scrapctl/quarantine.go`
- `internal/scrapctl/quarantine_test.go`

### Change Log

- 2026-06-12 - Added `scrapctl quarantine` operator workflow with redacted output, admin HTTP routing, evidence report generation, tests, and BMAD evidence updates.
