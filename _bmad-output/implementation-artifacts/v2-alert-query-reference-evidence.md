# V2 Alert and Query Reference Evidence

Artifact status: complete for Story 6.3 implementation
Release gate status: CONCERNS

Story: 6.3 - Alert and Query References
Story baseline commit: `25042bca63662449a2a8803818e8fce8bb7222e4`
Story creation commit: `fc21cb8`
Implementation base commit: `fc21cb8`
Branch: `v2`
Generated: 2026-06-12T18:55:19-04:00
Last updated: 2026-06-12T19:00:58-04:00

## Scope

This artifact proves that Story 6.3 created alert/query references tied to V2
release risks without adding new product behavior, telemetry, dashboards,
alert deployment manifests, or closure policy.

Each row records:

- the required release-risk reference,
- the implemented metric/query or explicit gap,
- the operational question the reference answers,
- the operator triad: what happened, how to confirm, and what to do next,
- the runbook link,
- the privacy/cardinality decision, and
- `PASS`, `CONCERNS`, or `FAIL` status.

## Source Inputs

| Input | Path or command | Result |
| --- | --- | --- |
| Release-facing alert/query reference | `docs/observability/v2-alert-query-references.md` | Created. |
| Existing evidence query pack | `deploy/kustomize/components/evidence-stack/queries.md` | Linked and reused for stable Phase 3 queries. |
| Operator runbook index | `docs/runbooks/README.md` | Updated with the alert/query reference link. |
| Story context | `_bmad-output/implementation-artifacts/6-3-alert-and-query-references.md` | Source ACs and task scope. |
| Metric source inventory | `internal/**` telemetry sources | Validated by source scan before completion. |

## Reference Matrix

| ID | Required risk | Source metric/query or gap | Operational question | Operator triad | Runbook | Privacy validation | Status | Owner / mitigation |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| AQR-001 | Public availability | `scrap.rpc.server.requests`, `scrap.rpc.server.duration`, `scrap.rpc.server.in_flight`; existing evidence query pack client API queries. | Are public Document RPCs available and within latency/error expectations? | Public RPCs are failing or slow; confirm request/error/p99 queries; then run startup readiness or evidence collection. | `docs/runbooks/v2-startup-security-readiness.md`, `docs/runbooks/v2-evidence-collection.md` | Uses bounded `rpc.method` and `rpc.grpc.status_code`; no Document or request identifiers. | PASS | None. |
| AQR-002 | Peer availability | Gap: no dedicated peer RPC availability metric. Uses Shard leader/apply metrics, security-denial metrics, and `scrapctl peers`. | Are peer paths healthy enough to route and replicate Shard work? | Peer traffic or routing is degraded; confirm leader/apply and peer diagnostics; then follow multi-Shard routing health. | `docs/runbooks/v2-multi-shard-routing-health.md` | Does not publish peer addresses or auth claims. | CONCERNS | Add peer RPC availability telemetry in a future telemetry story if release owners require alert-level proof. |
| AQR-003 | Admin availability | Gap: no dedicated admin HTTP request/error/latency metric. Uses `scrapctl status`, `scrapctl doctor`, and production security readiness evidence. | Can operators reach admin/status evidence safely? | Admin status is unavailable; confirm with CLI and readiness rehearsal; then treat as operator-readiness evidence. | `docs/runbooks/v2-startup-security-readiness.md`, `docs/runbooks/v2-evidence-collection.md` | Avoids admin URLs with credentials and request identifiers. | CONCERNS | Add admin HTTP telemetry if final release requires metrics-backed admin alerting. |
| AQR-004 | Write ACK latency | `scrap.rpc.server.duration`, `scrap.write.stage.duration`, spans `scrap.write/<stage>` and `scrap.apply/commit_document`. | Which write stage is delaying ACK? | Write ACKs are slow; confirm RPC p99, stage p99, and TraceQL write/apply spans; then collect write-path evidence. | `docs/runbooks/v2-evidence-collection.md`, `docs/runbooks/v2-backend-upload-pressure.md` | Uses bounded method/stage labels and no raw Transaction or Document identifiers. | PASS | None. |
| AQR-005 | Read failures | Public RPC status by method plus restore/quarantine references. | Are read/head/find failures expected client outcomes or durability/quarantine issues? | Reads are failing; confirm method/status and traces; then route to restore or quarantine runbooks. | `docs/runbooks/v2-restore-failures.md`, `docs/runbooks/v2-block-quarantine-repair.md`, `docs/runbooks/v2-content-quarantine-response.md` | Uses bounded method/status labels only. | PASS | None. |
| AQR-006 | Restore failures | `scrap.eviction.restore.total`, `scrap.eviction.restore.duration`, `scrap.eviction.restore_failed_blocks`, `scrap.eviction.restore_failures_by_reason`. | Are cold reads blocked by restore failures? | Restore failed; confirm restore metrics and Backend status; then follow restore failure response. | `docs/runbooks/v2-restore-failures.md` | Uses bounded status/reason and does not expose Backend object keys. | PASS | None. |
| AQR-007 | Backend upload lag | `scrap.upload.pending_bytes`, `scrap.upload.pending_blocks`, `scrap.upload.total`, `scrap.upload.duration`, `scrap.upload.verify_total`. | Is the Upload Outbox draining and verifying sealed Blocks? | Upload lag is growing; confirm pending work and outcomes; then inspect Backend dependency and outbox evidence. | `docs/runbooks/v2-backend-upload-pressure.md` | Uses `scrap.shard_id` and bounded `status`; no Backend keys or dependency output. | PASS | None. |
| AQR-008 | Upload pressure | `scrap.upload.pressure_level`, `scrap.upload.pending_bytes`, `scrap.upload.pending_blocks`, `scrap.upload.auth_paused`, RESOURCE_EXHAUSTED RPC status. | Is upload pressure threatening safe write admission? | Pressure is high; confirm pressure level and admission errors; then reduce load or repair Backend dependency. | `docs/runbooks/v2-backend-upload-pressure.md` | Uses bounded Shard/status labels and no object keys. | PASS | None. |
| AQR-009 | Block Quarantine and scrub | `scrap.scrub.deep.*`, `scrap.eviction.quarantined_blocks`. | Did Deep Scrub isolate corrupt Block data? | Scrub found corruption; confirm scrub/quarantine metrics; then repair Block Quarantine before claiming read health. | `docs/runbooks/v2-block-quarantine-repair.md` | Uses bounded Shard labels; no Block payloads or local host paths. | PASS | None. |
| AQR-010 | Scanner lag/outage | `scrap.avscan.runs`, `scrap.avscan.blocks`, `scrap.avscan.failures`, `scrap.avscan.engine_unavailable`, `scrap.avscan.lag_blocks`, `scrap.avscan.in_flight_blocks`. | Is async scanner work keeping up and available? | Scanner is lagging or unavailable; confirm lag/failure metrics; then follow Content Quarantine response if Documents are flagged. | `docs/runbooks/v2-content-quarantine-response.md` | Uses bounded `scrap.shard_id`, `status`, and `reason`; no Document identifiers. | PASS | None. |
| AQR-011 | Content Quarantine state | Gap: no dedicated Content Quarantine inventory metric. Uses scanner metrics and `scrapctl quarantine` evidence. | Are quarantined Documents awaiting operator action? | Content Quarantine needs action; confirm with `scrapctl quarantine` evidence; then inspect, confirm, or release through admin workflow. | `docs/runbooks/v2-content-quarantine-response.md` | CLI examples use placeholders and redacted evidence paths only. | CONCERNS | Add a bounded Content Quarantine count metric if alert-level inventory is required. |
| AQR-012 | Transit outage | Gap: no direct runtime OpenBao Transit outage metric. Uses startup readiness, bootstrap evidence, and production security rehearsal. | Is encryption failing closed because Transit is unavailable? | Transit is unreachable; confirm readiness/rehearsal output; then leave production failed closed and escalate to platform owner. | `docs/runbooks/v2-openbao-transit-dependency.md`, `docs/runbooks/v2-startup-security-readiness.md` | Does not expose OpenBao credential values, key material, or dependency error strings. | CONCERNS | Future security telemetry can add bounded Transit dependency status if accepted. |
| AQR-013 | Audit sink failure | Gap: no direct audit sink failure metric. Uses production security readiness/rehearsal evidence. | Can release evidence prove audit delivery? | Audit evidence is missing; confirm readiness output; then block final release PASS until linked evidence exists. | `docs/runbooks/v2-startup-security-readiness.md` | Avoids auth claims, request metadata, and raw audit payloads. | CONCERNS | Add bounded audit-sink telemetry or final release evidence owner in Story 6.5/6.7. |
| AQR-014 | Rate-limit and authorization denials | `scrap.security.rate_limit.denials`, `scrap.security.authorization.denials`. | Are denials expected policy behavior or a release risk? | Denials increased; confirm by surface/operation/reason; then inspect security readiness and routing health. | `docs/runbooks/v2-startup-security-readiness.md`, `docs/runbooks/v2-multi-shard-routing-health.md` | Uses bounded surface/operation/reason/status; no auth claims. | PASS | None. |
| AQR-015 | Shard leader and peer health | `scrap.raft.is_leader`, `scrap.raft.leader_id`, `scrap.raft.applied_index`, `scrap.raft.commit_index`. | Does each Shard have a leader and bounded apply lag? | Leadership/apply is unhealthy; confirm Raft metrics; then use multi-Shard routing health and avoid local-file authority. | `docs/runbooks/v2-multi-shard-routing-health.md` | Uses bounded Shard/Raft labels only. | PASS | None. |
| AQR-016 | Evidence leak-scan status | Gap: no live evidence leak metric. Uses Story 6.1/6.2/6.3 scans plus future Story 6.4/6.5 gates. | Is release evidence safe to publish and link? | Leak-scan evidence is missing or failed; confirm scan output; then keep release status below PASS until fixed. | `docs/runbooks/v2-evidence-collection.md` | Scan patterns cover secrets, raw IDs, keys, auth claims, host paths, logs, and generated material. | CONCERNS | Story 6.4/6.5 must produce final bundle and Tier evidence leak scans. |

## Gap Decisions

The following risks remain `CONCERNS` because the repo does not currently expose
dedicated telemetry for them:

- Peer RPC availability.
- Admin HTTP request/error/latency availability.
- Content Quarantine inventory count.
- Runtime OpenBao Transit outage.
- Audit sink failure.
- Live release evidence leak-scan status.

No metric names were fabricated for these gaps. Operators must use the linked
runbooks and current evidence artifacts until a future story implements bounded
telemetry.

## Verification

| Gate | Command | Result | Notes |
| --- | --- | --- | --- |
| Missing-artifact red phase | `test -f docs/observability/v2-alert-query-references.md`; `test -f _bmad-output/implementation-artifacts/v2-alert-query-reference-evidence.md`; `rg -n "v2-alert-query-references\|alert/query" docs/runbooks/README.md` | PASS | All three checks failed before implementation, proving the story surfaces were absent. |
| Whitespace check | `git diff --check` | PASS | No whitespace errors. |
| Placeholder scan | `rg -n "TO[D]O\|T[B]D\|FI[X]ME\|\{\{|\}\}" docs/observability/v2-alert-query-references.md _bmad-output/implementation-artifacts/v2-alert-query-reference-evidence.md` | PASS | No matches. |
| Reference-row count | `rg -n "AQR-00[1-9]\|AQR-01[0-6]" docs/observability/v2-alert-query-references.md _bmad-output/implementation-artifacts/v2-alert-query-reference-evidence.md` | PASS | All 16 required release-risk references appear in both artifacts. |
| Metric source validation | `rg -n "scrap\.rpc\.server\|scrap\.write\.stage\|scrap\.upload\|scrap\.raft\|scrap\.eviction\|scrap\.scrub\|scrap\.avscan\|scrap\.security\|process\.runtime\.go" internal deploy/kustomize/components/evidence-stack docs/observability _bmad-output/implementation-artifacts/v2-alert-query-reference-evidence.md` | PASS | Implemented metric references resolve to source code or the existing query pack; missing peer/admin/Transit/audit/evidence metrics stay as `CONCERNS` gaps. |
| Runbook/status validation | `rg -n "runbook\|v2-.*\.md\|docs/runbooks" docs/observability docs/runbooks/README.md _bmad-output/implementation-artifacts/v2-alert-query-reference-evidence.md`; `rg -n "PASS\|CONCERNS\|FAIL" _bmad-output/implementation-artifacts/v2-alert-query-reference-evidence.md docs/observability/v2-alert-query-references.md` | PASS | Every high-risk reference has a runbook path or explicit gap status. |
| Secret-shape scan | `rg -n --pcre2 "([a]ccess[_-]?[k]ey\|[p]assword\|[t]oken\|PRIVATE [K]EY\|BEGIN [A-Z ]*KEY)" docs/observability docs/runbooks/README.md _bmad-output/implementation-artifacts/6-3-alert-and-query-references.md _bmad-output/implementation-artifacts/v2-alert-query-reference-evidence.md` | PASS with classified safe matches | Matches are negative guidance about OpenBao credentials/key material and the story's required redaction criteria. No credential values or private-key material appear. |
| Identifier/privacy scan | `rg -n --pcre2 "transaction[_-]?[i]d=\|document[_-]?[n]ame=\|trace[_-]?[i]d=\|request[_-]?[i]d=\|Backend [k]ey\|raw [l]og\|auth [c]laim" docs/observability docs/runbooks/README.md _bmad-output/implementation-artifacts/6-3-alert-and-query-references.md _bmad-output/implementation-artifacts/v2-alert-query-reference-evidence.md` | PASS with classified safe matches | Matches are negative guidance, privacy validation text, or explicit gap decisions. No unredacted identifiers, Backend object references, authorization claims, request identifiers, trace identifiers, or unredacted log output appear. |
| Path-shape scan | `rg -n --pcre2 "/home/c[o]to\|/t[m]p/\|host-[a]bsolute" docs/observability docs/runbooks/README.md _bmad-output/implementation-artifacts/6-3-alert-and-query-references.md _bmad-output/implementation-artifacts/v2-alert-query-reference-evidence.md` | PASS with classified safe matches | Matches are the repo's safe cache command path and negative host-path guidance. No local-machine evidence path is introduced. |
| Proto compatibility | `make proto-check` | PASS | Buf lint/generate completed and `gen/` stayed clean. |
| E2E gate policy | `scripts/check-e2e-gates.sh` | PASS | No output; policy passed. |
| Broad local gate | `env GOCACHE=/tmp/scrap-v2-go-build make check` | PASS | Formatting, package boundaries, proto check, lint, `go test ./...`, race tests, integration-tagged tests, and `scrapd`/`scrapctl` builds passed. Output contained transient Testcontainers IDs and temp paths only. |

## Final Story 6.3 Decision

Story 6.3 satisfies its docs/evidence scope. Required release-risk references
exist, high-risk rows include the operator triad, runbook links are present
where implemented, and missing telemetry is recorded as `CONCERNS` rather than
invented. Final V2 release status remains below PASS until Stories 6.4-6.7 and
real S3/IAM evidence complete.
