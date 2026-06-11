---
baseline_commit: 18a90cd4f14dd6483e081eb5a203b754abfdf86c
---

# Story 2.6: Multi-Shard Evidence Closure

Status: ready-for-dev

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a release owner,
I want multi-Shard behavior proven through failure and restart cases,
so that Epic 2 cannot close from a happy-path startup demo.

## Traceability

- Epic: Epic 2 - Operators Can Run a Shard-Aware Cell.
- Requirements: FR-4 and FR-5.
- Decision gate: DG-2, closed by ADR 0026.
- Prerequisites: Stories 2.1 through 2.5 are done and provide routing validation, multi-Shard composition, public Transaction routing, peer Shard-scope authorization, and Shard-aware diagnostics.
- Non-goal: final V2 release closure remains Epic 6; restore-first cold reads remain Epic 3; real S3/IAM rehearsal remains Story 6.6 and `docs/production-rehearsal.md`.

## Acceptance Criteria

1. **AC-2.6.1 - Restart/rebuild determinism.** Given a two-or-more-Shard Cell, when restart/rebuild evidence is collected, then routing and Shard authority remain deterministic. Evidence links restart/rebuild command output and shows the same Transaction routes to the same owning Shard before and after restart.
2. **AC-2.6.2 - Non-zero Shard Backend evidence.** Given non-zero Shard IDs are used, when Backend upload/restore evidence is sampled, then object identity and diagnostics remain Shard-scoped. Evidence proves Backend keys are observed only as Backend object identity and are not used as public routing authority. If restore-first behavior is not implemented yet, the Epic 2 gate must record that restore evidence as CONCERNS with an Epic 3 reference instead of claiming PASS.
3. **AC-2.6.3 - Epic 2 closure decision.** Given Epic 2 closure is evaluated, when evidence is reviewed, then deterministic routing, invalid startup failure, wrong-Shard denial, diagnostics, restart/rebuild, and redaction evidence are linked. AC records PASS, CONCERNS, or FAIL using V2 release gate language and does not imply final V2 release readiness.

## Tasks / Subtasks

- [ ] Add deployed two-Shard placement for prodlike E2E evidence. (AC: 1, 2, 3)
  - [ ] Add a prodlike-e2e placement ConfigMap or equivalent manifest-owned file using non-zero Shard IDs, preferably Shards `7` and `9` with full slot coverage `0-511` and `512-1023`.
  - [ ] Mount the placement file into `scrapd` and set `SCRAP_SHARD_PLACEMENT_FILE` only in the E2E/prodlike evidence overlay that is allowed to enable `SCRAP_TEST_HOOKS`.
  - [ ] Preserve production overlay safety: do not enable test hooks in `deploy/kustomize/environments/prodlike`, and do not weaken TLS/security/test-hook guard scripts.
  - [ ] Use Kustomize-native ConfigMap generation or a checked-in ConfigMap manifest; do not generate placement files dynamically from tests or infer Shard placement from pod names, Backend keys, local files, peer addresses, or certificates.
- [ ] Extend E2E helpers so evidence can target and observe multiple Shards. (AC: 1, 2)
  - [ ] Add helpers that find Transaction IDs for specific Shards by using the configured route map or an existing routing implementation, not Backend key parsing.
  - [ ] Update Backend object listing helpers so they can list by Cell prefix and assert object Shard prefixes dynamically. Remove hardcoded Shard `0000000000000000` assumptions from multi-Shard evidence paths.
  - [ ] Add assertions that uploaded `.blk` and `.idx` pairs for non-zero Shards follow ADR 0009: `{cell_id}/shards/{shard_id}/{block_id}.blk` and `.idx`, with fixed-width lowercase hex IDs.
  - [ ] Keep S3/LocalStack helper code bounded and local to E2E tests; do not add a new AWS wrapper or dependency.
- [ ] Add multi-Shard restart/rebuild evidence. (AC: 1, 3)
  - [ ] Write Documents whose Transactions route to at least two Shards, then capture Shard diagnostics before restart.
  - [ ] Restart or replace a `scrapd` StatefulSet pod through the existing E2E Kubernetes helpers or Make targets, wait for readiness, then prove `HeadDocument` and `ReadDocument` still work for both routed Transactions.
  - [ ] Capture post-restart diagnostics and assert route ranges, Shard IDs, leader state fields, and health remain bounded and deterministic.
  - [ ] If a true rebuild command is not available for multi-Shard placement yet, record the missing rebuild evidence as CONCERNS and link the exact missing command/follow-up instead of creating a fake passing assertion.
- [ ] Add non-zero Shard Backend upload evidence. (AC: 2, 3)
  - [ ] Force at least one sealed Block upload on a non-zero Shard and observe the uploaded `.blk` and `.idx` pair in LocalStack through the existing AWS SDK Go v2 paginator pattern.
  - [ ] Prove public reads and heads route by Transaction before and after Backend object observation; do not route public API calls by parsing Backend keys.
  - [ ] Capture diagnostics showing the same Shard ID and route range for the Transaction while treating Backend object key evidence as cold-durability evidence only.
  - [ ] Do not add restore-first cold read implementation in this story. If restore evidence is sampled only as "not yet implemented", record that as CONCERNS for AC-2.6.2 and reference Epic 3 Story 3.4/3.7.
- [ ] Build an Epic 2 evidence closure artifact. (AC: 3)
  - [ ] Add a small checked-in evidence summary artifact under `_bmad-output/implementation-artifacts/` or a repo-owned script/test output path that links all Epic 2 evidence commands and story artifacts.
  - [ ] Include rows for deterministic routing, invalid startup failure, wrong-Shard peer denial before side effects, Shard-aware diagnostics, restart/rebuild behavior, non-zero Shard Backend upload/restore state, and redaction proof.
  - [ ] Each row must have AC ID, source story, command, artifact or test path, commit/ref, result `PASS`, `CONCERNS`, or `FAIL`, and concise next action for non-PASS rows.
  - [ ] Make the artifact explicit that Epic 2 closure is feature-scope evidence, not final V2 release readiness or PRD closure.
- [ ] Add redaction and authority-boundary checks. (AC: 1, 2, 3)
  - [ ] Leak-scan E2E output, diagnostics, and closure artifacts for raw `transaction_id`, `document_name`, idempotency keys, Backend object keys where not intentionally bounded, local filesystem paths, sensitive peer addresses, cert/key material, Transit tokens, request IDs, trace IDs, and raw dependency errors.
  - [ ] Ensure permitted evidence fields are bounded Shard IDs, route ranges, low-cardinality states, command names, test paths, commit refs, and non-secret object-key shape samples.
  - [ ] Add tests or scans proving Backend keys are not consumed by public routing code or used as Shard authority.
- [ ] Keep scope narrow and preserve current contracts. (AC: 1-3)
  - [ ] Do not change `proto/`, `gen/`, public `DocumentService`, peer `PeerService` wire shape, Block/Frame layout, Backend object format, Raft command shape, tenant identity, slot-transfer/rebalancing, OpenBao/Transit behavior, Content Quarantine, or final release matrix semantics.
  - [ ] Do not move routing ownership out of `internal/routing` or composition ownership out of `internal/cmd`.
  - [ ] Do not make Backend inventory, object existence, local files, pod names, peer addresses, certificates, metrics, admin output, or evidence artifacts storage authority.
  - [ ] Do not silently mark Epic 2 done if any P0 evidence row is missing, stale, local-only when policy requires CI, or redaction-unsafe.
- [ ] Add focused verification. (AC: 1-3)
  - [ ] Add unit or integration tests for any new E2E helper logic that can be tested without Kubernetes.
  - [ ] Run targeted tests first, then `make manifests-check`, `make package-boundaries`, and `make check`.
  - [ ] For runtime evidence, run `make tier2-e2e-up` or the narrowest documented prodlike E2E target that exercises the two-Shard overlay and Backend upload evidence.
  - [ ] Record exact commands, results, and artifact paths in this story before review.

## Dev Notes

### Current State

- Story 2.1 established `internal/routing` as the owner of slot count, Transaction hash lookup, route-map summaries, placement validation, and bounded route telemetry.
- Story 2.2 added `startupTopology`, `shardSet`, per-Shard local data directories, bounded startup status, and production startup failure for invalid or single-Shard production placement.
- Story 2.3 added public routing through `internal/cmd/public_store_router.go`; public handlers route by Transaction and fail closed when routes are unavailable.
- Story 2.4 added placement-derived peer Shard authorization. Wrong-Shard peer RPCs are denied before Raft, replication, rebuild, or Block-transfer side effects, with bounded audit/log/metric evidence.
- Story 2.5 added Shard-aware admin `/healthz` diagnostics and `scrapctl status` rendering. Diagnostics are read-only and include bounded Cell, Member, Shard, route, leader, peer, upload, eviction, and restore fields.
- `deploy/kustomize/environments/prodlike-e2e` currently inherits prodlike and only patches test hooks. It does not yet mount a Shard placement file, so deployed prodlike E2E still lacks explicit two-Shard placement evidence.
- `test/e2e/upload_e2e_test.go` currently lists Backend objects under `e2eCellID() + "/shards/0000000000000000/"`. That is incompatible with Story 2.6's non-zero Shard evidence and must be generalized.
- Existing E2E helpers can write/read/head Documents, find a leader pod for a Transaction, delete a pod and wait for readiness, run `scrapctl fault backend`, port-forward LocalStack, and list S3 objects with the AWS SDK Go v2 paginator.
- `docs/prd-closure-policy.md` requires linked CI/Tier evidence for PRD closure. Story 2.6 is not PRD closure, but the evidence rows must still name commands, commit/ref, result, and artifact path.

### Implementation Guidance

- Prefer a small prodlike-e2e placement manifest:
  - `slot_count`: `1024`
  - `shards`: `[7, 9]`
  - `local_shards`: `[7, 9]`
  - ranges: `0-511 -> 7` and `512-1023 -> 9`
- Mount placement read-only into the `scrapd` container, set `SCRAP_SHARD_PLACEMENT_FILE` to that mount path, and let existing startup validation enforce full slot coverage and local Shard membership.
- For E2E Transaction selection, reuse `internal/routing` from tests if package boundaries allow it. If an E2E test package cannot import the internal package cleanly, create a test-only helper command or use admin route diagnostics as evidence, but never parse Backend object keys to choose public routes.
- Backend evidence should observe object identity after upload, then independently prove public `HeadDocument`/`ReadDocument` still route by Transaction. The causal authority remains routing and Shard metadata, not S3 key prefixes.
- Treat restore carefully. Epic 3 owns restore-first cold reads; Story 2.6 may only link current restore status/diagnostics and record CONCERNS if restore is missing. Do not implement or simulate a cold-read restore to satisfy Epic 2.
- Closure artifact language must use `PASS`, `CONCERNS`, or `FAIL`. Use `PASS` only when current command output proves the criterion. Use `CONCERNS` for explicitly deferred Epic 3 restore or missing rebuild command when the core Epic 2 behavior is otherwise proven. Use `FAIL` when a required Epic 2 safety behavior is absent or redaction fails.

### Project Structure Notes

Likely update:

- `deploy/kustomize/environments/prodlike-e2e/kustomization.yaml` - include placement ConfigMap generation or resource.
- `deploy/kustomize/environments/prodlike-e2e/*placement*.json` or `*placement*.yaml` - two-Shard placement fixture for deployed E2E.
- `deploy/kustomize/environments/prodlike-e2e/statefulset-*.yaml` - mount placement and set `SCRAP_SHARD_PLACEMENT_FILE`.
- `test/e2e/upload_e2e_test.go` - remove single-Shard object-prefix assumption and assert non-zero Shard object identity.
- `test/e2e/e2e_test.go` or a focused `test/e2e/multishard_evidence_e2e_test.go` - restart/rebuild determinism and public read/head proof for at least two Shards.
- `test/e2e/scrapctl_e2e_test.go` or a focused admin helper file - capture Shard diagnostics through existing `scrapctl`/admin paths.
- `_bmad-output/implementation-artifacts/epic-2-multi-shard-evidence.md` or similar - closure artifact with evidence rows and gate decision.
- `_bmad-output/implementation-artifacts/2-6-multi-shard-evidence-closure.md` and `sprint-status.yaml` - story status/evidence updates during dev.

Likely avoid:

- `proto/`, `gen/`, public or peer API contracts.
- `internal/backend/*` unless a test-only helper needs existing object-key parsing behavior; ADR 0009 format should already be sufficient.
- `internal/shard/*` unless diagnostics reveal a missing read-only accessor. Do not add new authority behavior.
- `internal/routing/*` unless a small exported helper is required for test route selection and is justified by existing routing ownership.
- `docs/adr/` unless implementation changes a durable storage, wire, dependency, security, or package-boundary decision.

### Testing Notes

- Start with the E2E/helper red tests that fail on the hardcoded Shard `0` prefix and missing prodlike-e2e placement.
- Keep unit tests local for object-key prefix filtering and closure artifact rendering if those are implemented as helpers.
- For Kubernetes evidence, prefer existing `make tier2-e2e-up` because it creates the prodlike Kind/Cilium Cell, deploys the prodlike-e2e overlay, confirms LocalStack, rolls the StatefulSet, and runs `go test ./test/e2e/...` with E2E TLS/admin settings.
- `make manifests-check` must prove the overlay builds and the placement mount/env are present.
- `make check` remains the broad local gate before review. If Tier 2 cannot run in the current machine state, record it as skipped with the blocker and do not mark AC-2.6.1/2.6.2 PASS.

### Previous Story Intelligence

- Story 2.1 and 2.2 already proved invalid placement failure and multi-Shard startup in unit/app tests. Story 2.6 should link those results and add deployed restart evidence rather than duplicating all validation logic.
- Story 2.3 already proved public routing by Transaction through two Shards and route-failure redaction. Story 2.6 should exercise that behavior in a deployed Cell and after restart.
- Story 2.4 already proved wrong-Shard peer denial before side effects and redacted denial evidence. Story 2.6 should link those exact tests/artifacts in the closure table.
- Story 2.5 already proved read-only diagnostics, redacted admin/CLI output, production fail-closed diagnostics, and leader-state clarity. Story 2.6 should consume diagnostics as evidence and avoid changing the admin contract unless a read-only field is missing.
- Recent relevant commits: `18a90cd fix: address story 2.5 review findings`, `ec687a8 feat: add shard-aware diagnostics`, `994c8fb docs: create story 2.5 shard diagnostics`, `1abc429 fix: address story 2.4 review findings`, and `7a3a28d test: cover peer shard authorization evidence`.

### Technical Research Notes

- GitHub repo search for reusable Go multi-Shard storage-gateway patterns did not identify an implementation to adopt.
- GitHub code search for `SCRAP_SHARD_PLACEMENT_FILE` and placement ConfigMap patterns returned no reusable prior art; implement this using local Kustomize overlay patterns.
- Kubernetes docs confirm Kustomize `configMapGenerator` is an appropriate native way to generate a ConfigMap from files or literals. Source: https://kubernetes.io/docs/tasks/manage-kubernetes-objects/kustomization/
- Kubernetes `kubectl rollout restart` and `rollout status` docs confirm the existing Makefile restart/status pattern is appropriate for StatefulSet restart evidence. Sources: https://kubernetes.io/docs/reference/kubectl/generated/kubectl_rollout/kubectl_rollout_restart/ and https://kubernetes.io/docs/reference/kubectl/generated/kubectl_rollout/kubectl_rollout_status/
- AWS SDK for Go v2 docs confirm operation paginators expose `HasMorePages` and `NextPage(context.Context)`, matching the existing LocalStack object-list helper style. Source: https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/using.html
- No new Go dependency, assertion library, mocking framework, CLI framework, or Kubernetes client is expected.

### References

- `_bmad-output/planning-artifacts/epics.md` - Epic 2 overview and Story 2.6 acceptance criteria.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - V2 architecture, package ownership, evidence, and release-boundary rules.
- `_bmad-output/project-context.md` - package boundaries, testing rules, redaction rules, and commit safety.
- `CONTEXT.md` - glossary definitions for Cell, Member, Shard, Transaction, Document, Block, Backend, and routing identity.
- `docs/adr/0026-multi-shard-v2-release-boundary.md` - accepted multi-Shard release boundary and evidence requirements.
- `docs/adr/0009-backend-object-key-format.md` - Backend key format with Cell, Shard, and Block identity.
- `docs/adr/0024-production-topology-and-peer-scope-policy.md` - peer identity and Shard-scope authorization policy.
- `docs/prd-closure-policy.md` - current evidence-link and PRD closure rules.
- `docs/production-rehearsal.md` - final real S3/IAM rehearsal scope that Story 2.6 must not claim.
- `_bmad-output/implementation-artifacts/2-1-shard-routing-boundary-and-placement-validation.md` - routing validation and redaction evidence.
- `_bmad-output/implementation-artifacts/2-2-multi-shard-cell-startup-composition.md` - multi-Shard composition, startup status, and invalid placement evidence.
- `_bmad-output/implementation-artifacts/2-3-public-api-routes-by-transaction.md` - public API Transaction routing evidence.
- `_bmad-output/implementation-artifacts/2-4-peer-rpc-shard-scope-authorization.md` - wrong-Shard denial and redaction evidence.
- `_bmad-output/implementation-artifacts/2-5-shard-aware-admin-and-scrapctl-diagnostics.md` - Shard diagnostics and production fail-closed evidence.
- `internal/cmd/routing_config.go`, `internal/cmd/routing_config_test.go` - placement file loading and validation.
- `internal/cmd/public_store_router.go`, `internal/cmd/public_store_router_test.go` - public routing by Transaction.
- `internal/cmd/shard_set.go`, `internal/cmd/shard_diagnostics.go` - multi-Shard composition and diagnostics source.
- `internal/peer/authorization_test.go`, `internal/peer/audit_ratelimit_test.go` - wrong-Shard denial and redaction tests.
- `deploy/kustomize/environments/prodlike-e2e/` - target overlay for two-Shard runtime evidence.
- `test/e2e/e2e_test.go`, `test/e2e/upload_e2e_test.go`, `test/e2e/scrapctl_e2e_test.go` - current E2E write/read/restart/upload/scrapctl helpers.
- `Makefile` - `prodlike-kind-deploy-e2e`, `tier2-e2e`, `tier2-e2e-up`, `manifests-check`, `package-boundaries`, and `check` gates.

## Dev Agent Record

### Agent Model Used

GPT-5 Codex

### Debug Log References

- CREATE-STORY: `git status --short --branch` confirmed a clean `v2...origin/v2` branch at baseline `18a90cd4f14dd6483e081eb5a203b754abfdf86c`.
- CREATE-STORY: `python3 _bmad/scripts/resolve_customization.py --skill .agents/skills/bmad-create-story --key workflow` resolved no prepend/append hooks and loaded `_bmad-output/project-context.md` as persistent context.
- RESEARCH: `gh search repos "Go multi shard routing storage gateway" --limit 5` returned no reusable implementation candidates.
- RESEARCH: `gh search code "NewListObjectsV2Paginator language:Go" --limit 5` returned generic S3 paginator examples; the local helper already uses the official AWS SDK Go v2 paginator pattern.
- RESEARCH: `gh search code "kustomize configMapGenerator placement.json SCRAP_SHARD_PLACEMENT_FILE" --limit 5` returned no reusable prior art.
- RESEARCH: Official Kubernetes docs reviewed for Kustomize ConfigMap generation and rollout restart/status; official AWS SDK Go v2 docs reviewed for S3 paginators.

### Completion Notes List

- Ultimate context engine analysis completed - comprehensive developer guide created.
- Created Story 2.6 as the Epic 2 closure story and set status to ready-for-dev.
- Scoped the story to multi-Shard deployed evidence, restart/rebuild determinism, non-zero Shard Backend upload evidence, and Epic 2 PASS/CONCERNS/FAIL closure language.
- Explicitly prevented false closure of Epic 3 restore-first cold reads, Epic 6 final V2 release readiness, and real S3/IAM production rehearsal.
- Identified the current deployed-evidence gap: prodlike-e2e lacks a mounted placement file and upload E2E helpers hardcode Shard `0`.

### File List

- `_bmad-output/implementation-artifacts/2-6-multi-shard-evidence-closure.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

## Change Log

- 2026-06-11: Created Story 2.6 Multi-Shard Evidence Closure context and moved status to ready-for-dev.
