---
baseline_commit: 25042bca63662449a2a8803818e8fce8bb7222e4
---

# Story 6.3: Alert and Query References

Status: ready-for-dev

## Story

As a platform operator,
I want alert/query references tied to release risks,
so that S.C.R.A.P. health can be monitored through low-cardinality, redacted
signals.

## Acceptance Criteria

1. **AC-6.3.1 - Required release-risk references are covered.** Given alert/query references are created, when docs are reviewed, then they cover public/peer/admin availability, write ACK latency, read failures, restore failures, Backend upload lag, upload pressure, scrub/quarantine, scanner lag/outage, Transit outage, audit sink failure, rate-limit denials, Shard leader/peer health, and evidence leak-scan status. Evidence links each reference to an operational question.
2. **AC-6.3.1a - High-risk references include the operator triad.** Given an alert/query reference is added, when it is reviewed, then it states what happened, how to confirm it, and what the operator does next. Evidence records that triad for each high-risk reference.
3. **AC-6.3.2 - Telemetry attributes are bounded and redacted.** Given a query uses telemetry attributes, when reviewed, then attributes are bounded and do not include raw Document identifiers, Backend keys, tokens, trace IDs, or request IDs. Evidence records telemetry privacy validation.
4. **AC-6.3.3 - Alerts link to runbooks.** Given alerts reference runbooks, when reviewed, then each high-risk alert links to the relevant operator response path. Evidence records missing links as `FAIL` or `CONCERNS`.
5. **AC-6.3.4 - Missing telemetry is not invented.** Given alert/query references require feature telemetry not yet implemented, when the reference is reviewed, then the gap is marked `FAIL` or `CONCERNS` rather than filled by invented metrics. Evidence proves Epic 6 stayed aggregation-only.

## Tasks / Subtasks

- [ ] Create the durable alert/query reference documentation surface. (AC: 1, 2, 4)
  - [ ] Create `docs/observability/v2-alert-query-references.md` as the release-facing index for Story 6.3.
  - [ ] Link existing evidence-stack queries from `deploy/kustomize/components/evidence-stack/queries.md` instead of duplicating the whole Phase 3 query pack.
  - [ ] Add the Story 6.3 reference from `docs/runbooks/README.md` or the most appropriate operator index so runbook readers can find alert/query guidance.
  - [ ] Keep the docs as references and operator investigation guidance; do not create live notification routing or alert deployment policy in this story.
- [ ] Cover every required release-risk domain with explicit references. (AC: 1, 2)
  - [ ] Public availability: use public Document RPC request/error/latency signals and route to startup/security readiness or multi-Shard routing runbooks as appropriate.
  - [ ] Peer availability and Shard peer health: use peer/authz/routing evidence if implemented; mark missing peer-specific alert telemetry as `CONCERNS` rather than inventing it.
  - [ ] Admin availability: use implemented admin/status/evidence surfaces if available; mark missing admin-specific alert telemetry as `CONCERNS` rather than inventing it.
  - [ ] Write ACK latency: use `scrap.rpc.server.duration`, `scrap.write.stage.duration`, and TraceQL write/apply spans where implemented.
  - [ ] Read failures and restore failures: use bounded RPC status, eviction/restore metrics, and restore runbook links.
  - [ ] Backend upload lag and upload pressure: use `scrap.upload.pending_bytes`, `scrap.upload.pending_blocks`, `scrap.upload.pressure_level`, `scrap.upload.total`, `scrap.upload.verify_total`, `scrap.upload.auth_paused`, and the Backend upload pressure runbook.
  - [ ] Scrub and Block Quarantine: use `scrap.scrub.light.*`, `scrap.scrub.deep.*`, `scrap.eviction.quarantined_blocks`, and the Block Quarantine repair runbook.
  - [ ] Content Scanner and Content Quarantine: use `scrap.avscan.*` where implemented and link Content Quarantine response runbook; mark any missing Content Quarantine metric as `CONCERNS`.
  - [ ] Transit outage: link OpenBao and production security runbooks; mark missing direct Transit outage metric/query as a gap if only rehearsal/status evidence exists.
  - [ ] Audit sink failure: link production security readiness; mark missing direct audit sink failure telemetry as a gap if only startup gate/rehearsal evidence exists.
  - [ ] Rate-limit denials and authorization denials: use `scrap.security.rate_limit.denials` and `scrap.security.authorization.denials`.
  - [ ] Evidence leak-scan status: link Story 6.1/6.2 evidence scan patterns and Story 6.4/6.5 future owners; mark final evidence-gate query references as pending if not implemented yet.
- [ ] For each high-risk reference, include the operator triad. (AC: 2, 4)
  - [ ] State what happened in operator language.
  - [ ] State how to confirm it with PromQL, TraceQL, LogQL, `scrapctl`, make target, or evidence artifact.
  - [ ] State what the operator does next and link the relevant runbook path.
  - [ ] Record references with `PASS`, `CONCERNS`, or `FAIL`, owner, mitigation, and source path in `_bmad-output/implementation-artifacts/v2-alert-query-reference-evidence.md`.
- [ ] Validate telemetry privacy and cardinality before marking references `PASS`. (AC: 3)
  - [ ] Allow only bounded labels/attributes such as `scrap.cell_id`, `scrap.member_slot_id`, `scrap.member_id`, `scrap.shard_id`, `scrap.raft_id`, `rpc.service`, `rpc.method`, `rpc.grpc.status_code`, `scrap.write.stage`, bounded `status`, bounded `reason`, `scrap.surface`, and `scrap.operation`.
  - [ ] Do not add or recommend raw `transaction_id`, `document_name`, idempotency keys, Backend object keys, credential values, trace IDs, request IDs, auth claims, peer addresses, host paths, or dependency error strings as metric labels, log fields, alert labels, or public evidence text.
  - [ ] If TraceQL examples use block correlation, keep them investigation-only with placeholders and do not turn high-cardinality Block IDs into alert labels.
  - [ ] Use annotations/descriptions for changing values; do not put dynamic query values in alert labels.
- [ ] Preserve Epic 6 aggregation-only scope. (AC: 5)
  - [ ] Do not implement new production telemetry, new `scrapctl` commands, new admin endpoints, new dashboards, new alert deployment manifests, new collector components, or closure-policy changes in this story.
  - [ ] Do not edit protobuf contracts, production Go packages, deployment manifests, or ADRs unless a docs-blocking contradiction is found and cannot be recorded honestly.
  - [ ] Mark missing metrics, queries, runbook links, or evidence-gate owners as `CONCERNS` or `FAIL` with mitigation instead of fabricating names.
- [ ] Run verification and update BMAD tracking. (AC: 1-5)
  - [ ] `git diff --check`
  - [ ] `make proto-check`
  - [ ] `scripts/check-e2e-gates.sh`
  - [ ] `env GOCACHE=/tmp/scrap-v2-go-build make check`
  - [ ] Validate every documented metric/query name against existing code, existing evidence-stack query pack, or an explicit gap row.
  - [ ] Run redaction/privacy scans over `docs/observability/`, `docs/runbooks/README.md`, this story, and `_bmad-output/implementation-artifacts/v2-alert-query-reference-evidence.md`.
  - [ ] Update this story's Dev Agent Record and move the story to `review`; leave `done` for BMAD code review.

## Dev Notes

### Source Requirements

- Epic 6 reconciles feature evidence into a V2 release decision using `scrapctl`, OpenTelemetry evidence, runbooks, alert/query references, a release evidence matrix, closure policy updates, and final real S3/IAM production rehearsal. It aggregates and audits evidence; it must not introduce product behavior that belongs in Epics 1 through 5. [Source: `_bmad-output/planning-artifacts/epics.md#Epic 6: Release Owners Can Prove V2 Readiness`]
- Story 6.3 requires alert/query references for public/peer/admin availability, write ACK latency, read failures, restore failures, Backend upload lag, upload pressure, scrub/quarantine, scanner lag/outage, Transit outage, audit sink failure, rate-limit denials, Shard leader/peer health, and evidence leak-scan status. [Source: `_bmad-output/planning-artifacts/epics.md#Story 6.3: Alert and Query References`]
- FR-15 requires OpenTelemetry metrics, logs, traces, and profiles sufficient to prove runtime behavior, production safety, and evidence gates. New metrics use OTel instruments and low-cardinality attributes; logs redact raw Document identifiers, Backend keys, trace IDs, request IDs, secrets, and sensitive dependency text. [Source: `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md#FR-15: OTel evidence plane`]
- FR-16 requires linked, current, reviewable evidence and operator documentation. Operator runbooks, alert/query references, incident workflows, and evidence instructions are required unless explicitly de-scoped. [Source: `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md#FR-16: Major-release evidence and documentation closure`]
- DG-5 requires a release evidence matrix, operator runbooks, alert/query references, closure policy updates, and linked issue `#429` for real S3/IAM. Normal runbooks and docs/evidence closure do not require an ADR unless the work changes deployment, security, auth, or wire/storage contracts. [Source: `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md#DG-5: Release Documentation and Evidence Standard`]
- ADR 0012 makes OpenTelemetry the producer contract and requires low-cardinality metrics for client RPCs, write stages, Raft state, peer replication, upload lag, pressure, scrub work, process/runtime resources, structured logs, traces, and profiles. Raw `transaction_id` and `document_name` are forbidden as metric attributes; logs/traces use hashed identifiers by default. [Source: `docs/adr/0012-otel-evidence-plane.md`]
- ADR 0013 carries trace context through Raft and documents `scrap.apply/*`, `scrap.upload/*`, `scrap.block_id`, hashed `scrap.transaction.hash`, hashed `scrap.document.hash`, and replay suppression. Do not expose raw trace IDs or request IDs in public docs/evidence. [Source: `docs/adr/0013-trace-context-in-raft-log.md`]

### Existing Query and Evidence Surfaces

- Existing evidence-stack queries live in `deploy/kustomize/components/evidence-stack/queries.md`. They already cover dashboards, Kubernetes scrape health, public RPC request rate/error/latency, write-path stage latency, upload pressure and pending bytes/blocks, Raft leader/apply lag, RPC concurrency, disk/Pebble storage, process runtime, TraceQL examples, and Phase 4 readiness gates.
- `scripts/evidence-bundle.sh`, `internal/scrapctl/evidence.go`, and `internal/scrapctl/evidencebundle/*` own existing evidence-bundle collection. Story 6.3 should link to those surfaces only as existing sources; Story 6.4 owns new release bundle behavior.
- `docs/runbooks/` now contains the operator response paths created by Story 6.2. High-risk alert/query references should link to those runbooks rather than duplicating incident steps.

### Existing Metric and Attribute Inventory

Use existing names exactly. If a required risk lacks a metric, mark the row `CONCERNS` or `FAIL`.

| Domain | Implemented signals to reuse | Source |
| --- | --- | --- |
| Public RPC availability, error, latency, in-flight load | `scrap.rpc.server.requests`, `scrap.rpc.server.duration`, `scrap.rpc.server.in_flight`; attributes `rpc.service`, `rpc.method`, `rpc.grpc.status_code` | `internal/server/telemetry.go`; `deploy/kustomize/components/evidence-stack/queries.md` |
| Write ACK path | `scrap.write.stage.duration`; spans `scrap.write/<stage>` and `scrap.apply/commit_document` | `internal/shard/write_telemetry.go`; `docs/adr/0013-trace-context-in-raft-log.md` |
| Upload lag and pressure | `scrap.upload.pending_bytes`, `scrap.upload.pending_blocks`, `scrap.upload.total`, `scrap.upload.duration`, `scrap.upload.verify_total`, `scrap.upload.pressure_level`, `scrap.upload.concurrency`, `scrap.upload.auth_paused` | `internal/shard/upload_metrics_otel.go` |
| Raft leader and apply lag | `scrap.raft.is_leader`, `scrap.raft.leader_id`, `scrap.raft.applied_index`, `scrap.raft.commit_index` | `internal/telemetry/raft.go` |
| Local storage and Projection disk | `scrap.disk.used_bytes`, `scrap.disk.free_bytes`, `scrap.pebble.disk_bytes` | `internal/telemetry/disk.go` |
| Eviction and restore | `scrap.eviction.plans`, `scrap.eviction.skips`, candidate/eligible/selected gauges, `scrap.eviction.apply.*`, `scrap.eviction.restore.*`, `scrap.eviction.restore_failed_blocks`, `scrap.eviction.restore_failures_by_reason`, `scrap.eviction.quarantined_blocks` | `internal/shard/eviction_metrics_otel.go` |
| Light and Deep Scrub | `scrap.scrub.light.runs`, `scrap.scrub.light.duration`, `scrap.scrub.deep.runs`, `scrap.scrub.deep.frames_verified`, `scrap.scrub.deep.corruptions`, `scrap.scrub.deep.quarantines`, `scrap.scrub.deep.blocks_quarantined`, `scrap.scrub.deep.progress_ratio`, `scrap.scrub.deep.bad_disk_suspected`, `scrap.scrub.deep.pauses`, `scrap.scrub.deep.duration`, `scrap.scrub.deep.repairs`, `scrap.scrub.deep.skips` | `internal/scrub/metrics_otel.go` |
| Content Scanner | `scrap.avscan.runs`, `scrap.avscan.run.duration`, `scrap.avscan.blocks`, `scrap.avscan.failures`, `scrap.avscan.engine_unavailable`, `scrap.avscan.lag_blocks`, `scrap.avscan.in_flight_blocks`, `scrap.avscan.duplicate_schedules` | `internal/avscan/metrics_otel.go` |
| Rate-limit and authorization denials | `scrap.security.rate_limit.denials`, `scrap.security.authorization.denials`; attributes `scrap.surface`, `scrap.operation`, `scrap.reason`, `scrap.authorization_status` | `internal/security/ratelimit.go`; `internal/security/authorization_metrics.go` |
| Runtime resources | `process.runtime.go.goroutines`, `process.runtime.go.mem.heap_alloc`, `process.runtime.go.mem.heap_sys`, `process.runtime.go.gc.count`, `process.runtime.go.gc.pause_total` | `internal/telemetry/runtime.go` |
| Resource/log identity | `service.instance.id`, `scrap.cell_id`, `scrap.member_slot_id`, `scrap.member_id`, `scrap.shard_id`, `scrap.raft_id` | `internal/cmd/telemetry.go` |

### Expected Durable Artifacts

- `docs/observability/v2-alert-query-references.md` - release-facing reference docs for alert/query coverage, triads, runbook links, and gap rows.
- `_bmad-output/implementation-artifacts/v2-alert-query-reference-evidence.md` - Story 6.3 evidence artifact with one row per required release-risk reference, source query/metric, operational question, triad status, runbook link, privacy validation, status, owner, and mitigation.
- `docs/runbooks/README.md` - update only if needed to link the alert/query reference docs from the operator entry point.
- `deploy/kustomize/components/evidence-stack/queries.md` - update only if the implementation adds or corrects stable evidence-stack query references. Preserve existing Phase 3 query pack content and rate-window rationale.

### Previous Story Intelligence

- Story 6.2 created the runbooks that Story 6.3 must link to: startup/security readiness, mTLS certificate rotation, OpenBao Transit dependency, Backend upload pressure, restore failures, eviction campaigns, Block Quarantine repair, Content Quarantine response, multi-Shard routing health, and evidence collection. [Source: `_bmad-output/implementation-artifacts/6-2-operator-runbooks-for-v2-failure-domains.md`; `docs/runbooks/README.md`]
- Story 6.2 review fixes matter here: command/evidence examples must match implemented parsers, safe repo-relative artifact paths must be distinguished from forbidden host paths/raw contents, evidence collection must not overclaim final release readiness, and missing links/gaps should be explicit. [Source: `_bmad-output/implementation-artifacts/6-2-operator-runbooks-for-v2-failure-domains.md#Review Findings`]
- Story 6.1 matrix still marks Story 6.3 as `FAIL` because alert/query references do not exist yet. Completing this story should update only the Story 6.3 row or a dedicated 6.3 evidence artifact; final V2 release remains blocked by Stories 6.4-6.7 and issue `#429`. [Source: `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md#Story Matrix`]

### External Research

- Grafana alerting docs define labels as alert-instance identity/routing data and annotations as responder information. Built-in annotations include `summary`, `description`, `runbook_url`, `__dashboardUid__`, and `__panelId__`; use annotations for changing query values rather than dynamic labels. [Source: `https://grafana.com/docs/grafana/latest/alerting/fundamentals/annotation-label/labels-and-label-matchers/`; `https://grafana.com/docs/grafana/latest/alerting/alerting-rules/templates/`]
- Prometheus alerting rules use `expr`, optional `for`, `labels`, and `annotations`; annotations are the right place for descriptions and runbook links. Recording rules can precompute expensive expressions, and ratio aggregation should aggregate numerator and denominator separately. [Source: `https://prometheus.io/docs/prometheus/latest/configuration/alerting_rules/`; `https://prometheus.io/docs/prometheus/latest/configuration/recording_rules/`; `https://prometheus.io/docs/practices/rules/`]
- Tempo TraceQL supports scoped fields such as `span:status`, `span:duration`, `span:name`, and `trace:duration`; scoped attribute filters are preferred for performance. Use TraceQL for investigation references, not as alert labels containing trace IDs. [Source: `https://grafana.com/docs/tempo/latest/traceql/construct-traceql-queries/`; `https://grafana.com/docs/grafana/latest/datasources/tempo/query-editor/traceql-query-examples/`]
- Loki LogQL metric queries support `rate`, `count_over_time`, `absent_over_time`, parsers, and bounded stream selectors. Keep selectors bounded and avoid parsing sensitive payload fields into labels. [Source: `https://grafana.com/docs/loki/latest/query/query_reference/`; `https://grafana.com/docs/loki/latest/query/metric_queries/`; `https://grafana.com/docs/loki/latest/query/log_queries/`]
- GitHub search found generic examples of p99 `histogram_quantile` alert/query patterns but no SCRAP-specific reusable alert/query pack. Reuse the local evidence-stack query pack and implemented OTel metric inventory instead of importing external templates.

### Redaction and Security Notes

- Public docs and evidence must not include credential values, private key material, generated certificate material, Document payloads, raw Document identifiers, Backend object keys, tokens, trace IDs, request IDs, auth claims, host-absolute paths, or unredacted dependency/log output.
- Do not add raw `transaction_id`, `document_name`, idempotency keys, Backend object keys, trace IDs, request IDs, peer addresses, or auth claims as metric labels, alert labels, log fields, or public artifact rows.
- Prefer placeholders that cannot be mistaken for real material: `<redacted-transaction>`, `<redacted-document>`, `<admin-url>`, `<cell-id>`, `<shard-id>`, `<runbook-path>`.
- Keep validation scan patterns bracket-split in the story/evidence artifact so the scan command does not self-match.

### Testing Requirements

Run these gates before moving Story 6.3 to review:

```bash
git diff --check
make proto-check
scripts/check-e2e-gates.sh
env GOCACHE=/tmp/scrap-v2-go-build make check
```

Run source validation for documented query/metric references and record results in `_bmad-output/implementation-artifacts/v2-alert-query-reference-evidence.md`:

```bash
rg -n "scrap\\.rpc\\.server|scrap\\.write\\.stage|scrap\\.upload|scrap\\.raft|scrap\\.eviction|scrap\\.scrub|scrap\\.avscan|scrap\\.security|process\\.runtime\\.go" internal deploy/kustomize/components/evidence-stack docs/observability _bmad-output/implementation-artifacts/v2-alert-query-reference-evidence.md
rg -n "runbook|v2-.*\\.md|docs/runbooks" docs/observability docs/runbooks/README.md _bmad-output/implementation-artifacts/v2-alert-query-reference-evidence.md
rg -n "PASS|CONCERNS|FAIL" _bmad-output/implementation-artifacts/v2-alert-query-reference-evidence.md
```

Run release-sensitive scans over the story, new docs, updated runbook index, and evidence artifact. Keep patterns bracket-split:

```bash
secret_shape='([a]ccess[_-]?[k]ey|[p]assword|[t]oken|PRIVATE [K]EY|BEGIN [A-Z ]*KEY)'
identity_shape='transaction[_-]?[i]d=|document[_-]?[n]ame=|trace[_-]?[i]d=|request[_-]?[i]d=|Backend [k]ey|raw [l]og|auth [c]laim'
path_shape='/home/c[o]to|/t[m]p/|host-absolute'
rg -n --pcre2 "$secret_shape" docs/observability docs/runbooks/README.md _bmad-output/implementation-artifacts/6-3-alert-and-query-references.md _bmad-output/implementation-artifacts/v2-alert-query-reference-evidence.md
rg -n --pcre2 "$identity_shape" docs/observability docs/runbooks/README.md _bmad-output/implementation-artifacts/6-3-alert-and-query-references.md _bmad-output/implementation-artifacts/v2-alert-query-reference-evidence.md
rg -n --pcre2 "$path_shape" docs/observability docs/runbooks/README.md _bmad-output/implementation-artifacts/6-3-alert-and-query-references.md _bmad-output/implementation-artifacts/v2-alert-query-reference-evidence.md
```

Classify every match as required negative guidance, a safe placeholder, or a bug to fix before review.

### Project Structure Notes

- This is a docs/evidence story. Keep production Go packages, protobuf contracts, generated files, deployment manifests, and ADRs untouched unless a contradiction blocks honest documentation.
- Prefer small focused docs. If `docs/observability/v2-alert-query-references.md` grows too large, split by domain under `docs/observability/` and keep the top-level file as an index.
- Do not move `deploy/kustomize/components/evidence-stack/queries.md`; it is tied to the evidence-stack deployment component. The release-facing docs can link to it or add stable references to it.

### References

- `CONTEXT.md` - glossary and authority boundaries for Document, Transaction, Block, Frame, Shard, Cell, Member, Backend, Pebble Projection, Local Block Lifecycle, Block Quarantine, and Content Quarantine.
- `_bmad-output/planning-artifacts/epics.md` - Epic 6 and Story 6.3 source requirements.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-15 and FR-16.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - DG-5 release documentation/evidence standard.
- `docs/adr/0012-otel-evidence-plane.md` - OTel evidence plane and privacy requirements.
- `docs/adr/0013-trace-context-in-raft-log.md` - TraceQL and trace context conventions.
- `deploy/kustomize/components/evidence-stack/queries.md` - existing evidence query pack.
- `docs/runbooks/README.md` - Story 6.2 operator runbook index.
- `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md` - current release matrix and Story 6.3 gap row.
- `_bmad-output/implementation-artifacts/6-2-operator-runbooks-for-v2-failure-domains.md` - previous story intelligence and review corrections.
- `internal/server/telemetry.go`, `internal/shard/write_telemetry.go`, `internal/shard/upload_metrics_otel.go`, `internal/shard/eviction_metrics_otel.go`, `internal/scrub/metrics_otel.go`, `internal/avscan/metrics_otel.go`, `internal/security/ratelimit.go`, `internal/security/authorization_metrics.go`, `internal/telemetry/raft.go`, `internal/telemetry/disk.go`, `internal/telemetry/runtime.go`, `internal/cmd/telemetry.go` - implemented telemetry inventory.

## Dev Agent Record

### Agent Model Used

### Debug Log References

- 2026-06-12T18:49:33-04:00 - Story context created from Epic 6, FR-15, FR-16, DG-5, ADR 0012, ADR 0013, existing evidence-stack queries, implemented OTel metric inventory, Story 6.2 runbook/review lessons, and current sprint state.

### Completion Notes List

- Ultimate context engine analysis completed - comprehensive developer guide created.

### File List

## Change Log

- 2026-06-12 - Created Story 6.3 context for alert and query references.
