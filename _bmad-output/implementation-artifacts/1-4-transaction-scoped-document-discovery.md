---
baseline_commit: d970de3d0bbec6b6ec260d94e3722774bc3995e4
created_at: 2026-06-11T10:33:20-04:00
---

# Story 1.4: Transaction-Scoped Document Discovery

Status: done

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a billing service engineer,
I want to find Documents for a Transaction without storage internals leaking,
so that reconciliation can use public metadata safely.

## Traceability

- Epic: Epic 1 - Billing ETL Can Trust Immutable Document Writes and Reads.
- Requirements: FR-1.
- Acceptance IDs: AC-1.4.1, AC-1.4.2, AC-1.4.3, AC-1.4.4.
- Governing sources: `_bmad-output/planning-artifacts/epics.md`, `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md`, `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md`, `CONTEXT.md`, ADR 0014, ADR 0016, ADR 0026.
- GitHub issue: not assigned in the current epic artifact. Before implementation PR, link either a tracker issue or this BMAD story artifact.

## Acceptance Criteria

1. **AC-1.4.1 - Transaction scope and public metadata.** Given multiple Documents exist for a Transaction and other Documents exist outside it, when `FindDocuments` is called, then the response contains only Documents from the requested Transaction, in write order, with public metadata fields only: `name`, `content_type`, `size`, `sha256_checksum`, and `created_at`. Evidence proves no Backend object keys, Block IDs, Shard IDs, local file paths, or raw storage layout details are exposed.
2. **AC-1.4.2 - Empty discovery is stable and local-authority based.** Given no visible Documents exist for a Transaction, when `FindDocuments` is called, then the response is successful with an empty list, not `NOT_FOUND`, and the result does not imply Backend inventory state. Evidence proves Backend list, HEAD, or inventory output is not used as public API authority.
3. **AC-1.4.3 - Redacted low-cardinality evidence.** Given find paths run through the public gRPC server, when logs, metrics, traces, and test artifacts are generated, then telemetry stays low-cardinality and redacted. Evidence records the leak-scan command and result for raw `transaction_id`, `document_name`, Backend keys, local paths, and Document bytes.
4. **AC-1.4.4 - Routing boundary preserved.** Given this story implements Transaction-scoped discovery semantics, when public cross-Shard routing is needed, then routing remains owned by Story 2.3 rather than hidden in discovery logic. Evidence records whether implementation touched index/query code, public server/store boundary code, or both, and confirms no new multi-Shard router or `tenant_id` storage identity was added.

## Tasks / Subtasks

- [x] Add characterization tests before production changes. (AC: 1-4)
  - [x] Cover a real Shard `FindDocuments` path with at least two Documents in one Transaction and at least one Document in another Transaction. Assert exact count, write order, names, content types, sizes, SHA-256 values, and `CreatedAt` values.
  - [x] Cover public gRPC `FindDocuments` through `server.Register` with a real Shard-backed `store.Store`; prefer the Story 1.3 `startReadVerificationShardServer` pattern over `internal/spike`.
  - [x] Cover empty Transaction discovery through Store and gRPC as a successful empty list. Do not map missing Transactions to `NOT_FOUND` for `FindDocuments`.
  - [x] Cover invalid `transaction_id` / `tenant_id` validation through gRPC and assert `codes.InvalidArgument` before Store side effects.
  - [x] Cover fail-closed visible metadata corruption through Shard and gRPC: corrupt `.idx` or Projection Resolution state must return `store.ErrDataLoss` / `codes.DataLoss`.
- [x] Preserve Projection Resolution and metadata-read authority. (AC: 1, 2)
  - [x] Keep `internal/index.Resolver.ListDocuments` as the write-order Projection Resolution authority. Do not duplicate `.idx` parsing in `internal/shard` or `internal/server`.
  - [x] Keep `internal/shard.FindDocuments` behind `requireLeaderRead`, `ValidateTransactionLookup`, strict Projection Resolution, and `ensureMetadataReadsAllowed`.
  - [x] Preserve metadata-only reads for evicted confirmed Blocks with retained `.idx` files. `FindDocuments` must not trigger Backend restore, Backend list, Backend HEAD, or Backend inventory scans.
  - [x] Preserve fail-closed behavior for missing local `.idx`, corrupt `.idx`, unexpected local Block loss, unconfirmed eviction markers, and Projection `DocCount` drift.
- [x] Preserve public transport and error semantics. (AC: 1-4)
  - [x] Keep `internal/server.FindDocuments` as transport mapping only: validate request fields, require `DocumentReader`, call `store.Store.FindDocuments`, map Store errors centrally, and render public `DocumentMeta`.
  - [x] Do not add public response fields, change `proto/scrap/v1/document.proto`, or expose storage internals in `FindDocumentsResponse`.
  - [x] Preserve typed error mapping: invalid input -> `INVALID_ARGUMENT`; visible metadata corruption -> `DATA_LOSS`; not-leader -> `UNAVAILABLE` with `LeaderHint`; empty Transaction -> OK empty response.
  - [x] Preserve context propagation through Store calls. Do not introduce background goroutines, unbounded channels, package-level caches, sleeps, or cross-Shard scans.
- [x] Preserve routing and identity boundaries. (AC: 1, 4)
  - [x] Do not add a multi-Shard router in this story. Story 2.3 owns public API routing by Transaction.
  - [x] Do not add `tenant_id` to Document identity, Pebble Projection keys, Block `.idx`, Backend keys, telemetry identity, or response metadata.
  - [x] If code needs route context evidence, document the single-Shard fixture and Store-compatible boundary instead of hardcoding hidden Shard ownership logic.
- [x] Add redaction and evidence coverage. (AC: 1, 3)
  - [x] Add or extend server telemetry tests for `FindDocuments` using existing manual metric reader / span recorder helpers. Assert metrics use bounded RPC labels and omit raw `transaction_id`, `document_name`, Backend keys, local paths, and Document bytes.
  - [x] Assert default span identity uses hashed `scrap.transaction.hash` and does not include `scrap.transaction_id`. `FindDocuments` has no `document_name`, so no raw document identity should appear.
  - [x] Capture logs where the path can log, such as not-leader redirect, and assert no raw request identity or local path fields are emitted.
  - [x] Add a leak-scan command to Debug Log References, for example `git diff -- ... | rg -n '<raw test tx>|<raw doc>|local/shards|\\.blk|\\.idx|/tmp/'`, and record PASS/FAIL.
- [x] Record implementation evidence before review. (AC: 1-4)
  - [x] Add Debug Log References with exact commands and PASS/FAIL result.
  - [x] Include changed-boundary list, typed-error mapping, Backend-authority proof, routing-boundary proof, and redaction proof in Completion Notes.
  - [x] Run at minimum: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/server ./internal/shard`.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/server ./internal/shard` because the story touches server/store metadata paths and Shard read locks.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries` if any package boundary, import graph, or server/store/shard boundary changes.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build make check` before moving to review unless a narrower gate is explicitly justified in the story notes.

### Review Findings

- [x] [Review][Patch] Add public gRPC invalid `transaction_id` before-Store evidence [internal/server/find_documents_internal_test.go:17]
- [x] [Review][Patch] Count Backend `HeadObject` and `ListObjects` in non-authority proof [internal/shard/find_documents_test.go:75]
- [x] [Review][Patch] Broaden redaction leak-scan evidence beyond `internal/server/server.go` [_bmad-output/implementation-artifacts/1-4-transaction-scoped-document-discovery.md:188]
- [x] [Review][Patch] Cover `DeadlineExceeded` status preservation for `FindDocuments` [internal/server/find_documents_internal_test.go:50]
- [x] [Review][Defer] Rebuilding Store errors still map to `INTERNAL` [internal/server/server.go:570] — deferred, pre-existing

## Dev Notes

### Current State

- Story 1.1, Story 1.2, and Story 1.3 are complete in this working tree, not necessarily committed. Do not revert their changed files or untracked story/test files while implementing Story 1.4.
- `proto/scrap/v1/document.proto` already defines `FindDocuments(FindDocumentsRequest) returns (FindDocumentsResponse)` and `DocumentMeta` with only public metadata fields. A proto change is not expected for this story.
- `internal/store.Store` already exposes `FindDocuments(ctx, txID string) ([]store.DocumentMeta, error)`.
- `internal/server.FindDocuments` already validates `transaction_id` and validation-only `tenant_id`, requires `security.RoleDocumentReader`, calls `store.FindDocuments`, maps errors, and renders `scrapv1.DocumentMeta`.
- `internal/shard.FindDocuments` already performs leader/read-index gating, validates Transaction lookup, calls `projectionResolver().ListDocuments(txID)`, checks local metadata readability with `ensureMetadataReadsAllowed`, and maps resolved entries to `store.DocumentMeta`.
- `internal/index.Resolver.ListDocuments` already returns resolved Documents in write order across referenced Blocks, returns an empty list when the Transaction is absent, and fails closed on unreadable `.idx`, missing Transaction entries in a referenced Block, or `DocCount` drift.
- `internal/shard/read_lifecycle_test.go` already proves `FindDocuments` stays local for evicted confirmed Blocks and does not call Backend `GetObject`; reuse the `stageEvictedConfirmedBlock` and `countingGetBackend` pattern if more evidence is needed.
- Existing server tests cover basic `FindDocuments` and empty Transaction through the older `startTestServer` spike fixture. Story 1.4 needs stronger public evidence through a real Shard-backed Store fixture and exact metadata/order assertions.

### Exact Discovery Contract

- `FindDocuments` is Transaction-scoped. It must never return Documents from another Transaction, even when Document names overlap.
- Ordering is write order as resolved by the Transaction entry's Block ID order and each Block `.idx` order. Do not sort alphabetically unless a future requirement changes the contract.
- Empty Transaction lookup is a successful empty response. It is not a storage corruption case and not a Backend inventory claim.
- Public metadata is limited to name, content type, size, SHA-256 hex string, and created timestamp. Do not expose Block ID, Frame offsets, Shard ID, Backend object key, local path, eviction state, upload state, raft index, or internal error details.
- Corrupt visible metadata is `store.ErrDataLoss` / gRPC `DATA_LOSS`. Do not mask visible Projection or `.idx` corruption as empty discovery or `NOT_FOUND`.
- Metadata-only discovery must not restore `.blk` files. Retained `.idx` metadata plus strict Projection Resolution is enough for `FindDocuments`; Backend restore belongs to `ReadDocument`.

### Implementation Guardrails

- Keep discovery behavior behind `store.Store` / `internal/shard`. `internal/server` must not become the authority for Projection, Block `.idx`, Backend lifecycle, or Shard routing.
- Do not duplicate Block index parsing. Use `internal/index.Resolver.ListDocuments`, which intentionally depends on `internal/block`.
- Do not use Backend inventory, Backend HEAD, Backend list, local file existence alone, Upload Outbox state, audit, logs, or telemetry as public discovery authority.
- Do not add a router, Shard map, slot hash, or public cross-Shard scan. ADR 0026 and Story 2.3 own that routing boundary.
- Do not add `tenant_id` to storage identity. It remains validation-only future routing input for this story.
- Do not introduce new dependencies, assertion libraries, mocking frameworks, or public protobuf changes.
- Preserve `log/slog` as the application logging API and existing OpenTelemetry identity hashing defaults.
- If storage format, wire protocol, dependency/runtime choices, security/encryption contracts, or cross-package ownership changes appear necessary, stop and add/update an ADR before implementation closure.

### Project Structure Notes

- Likely touched production files:
  - `internal/server/server.go` - only if current transport validation, context/status preservation, or rendering needs correction. Keep it transport-only.
  - `internal/shard/shard.go` - only if current Store discovery semantics fail the new evidence. Keep authority in strict Projection Resolution.
  - `internal/index/resolution.go` - only if write-order, absent Transaction, or fail-closed semantics are wrong. Avoid new resolver paths unless tests prove a gap.
- Likely touched tests:
  - `internal/shard/shard_test.go` or a new focused `internal/shard/find_documents_test.go` for exact Transaction scope, order, and metadata.
  - `internal/shard/read_lifecycle_test.go` for Backend non-authority and metadata-only local behavior around evicted Blocks.
  - `internal/server/metadata_test.go` or a new focused `internal/server/find_documents_test.go` for registered gRPC behavior with a real Shard-backed Store.
  - `internal/server/telemetry_test.go`, `internal/server/identifier_mode_test.go`, or `internal/server/not_leader_test.go` for redaction and low-cardinality telemetry evidence.
  - `internal/index/resolution_test.go` only if resolver behavior needs new direct evidence.

### Testing Notes

- Prefer existing helpers before adding new ones: `openTestShard`, `openUploadTestShard`, `stageEvictedConfirmedBlock`, `newCountingGetBackend`, `server.Register`, `startServerWith`, `startReadVerificationShardServer`, `writeDocument`, span/metric helper functions in `internal/server/telemetry_test.go`, and local corruption helpers.
- Tests must assert exact metadata, not just count. Capture `WriteDocumentResponse` values and compare SHA-256 and `CreatedAt` in `FindDocuments` response entries.
- For public gRPC tests, avoid `internal/spike` for new Story 1.4 acceptance evidence when a real Shard-backed fixture is available.
- For empty Transaction, assert successful empty list through both Store and gRPC.
- For Backend non-authority, use a counting Backend and assert zero Backend calls for `FindDocuments`; if only `GetObject` is observable in existing test helpers, explicitly state the evidence scope and do not imply Backend list/HEAD was tested unless the fake records those calls.
- For corruption, mutate only temp-dir `.idx` / Projection fixtures. Do not mutate checked-in fixtures or shared paths.
- For redaction, assert absence in spans, metric attributes, logs, and diff/evidence output. Use concrete forbidden strings unique to the test such as `tx-find-secret`, `invoice-secret.xml`, `classified-payload`, `local/shards`, `.blk`, `.idx`, and temp paths.

### Previous Story Intelligence

- Story 1.3 hardened read/head all-or-error behavior and added explicit Block header / Frame sequence verification. Story 1.4 should not weaken that path while adding discovery evidence.
- Story 1.3 confirmed that `FindDocuments` remains transaction-scoped and write-order preserving, but Story 1.4 owns public discovery semantics and evidence.
- Story 1.3 changed server read error mapping to preserve context and gRPC status errors. If Story 1.4 touches `FindDocuments`, preserve context-derived cancellation/deadline and existing Store status behavior instead of remapping known statuses to `Internal`.
- Story 1.3 review found that test-only coverage can miss in-progress failure modes. For Story 1.4, add tests that prove public gRPC behavior and Store/Shards agree, not only direct Shard calls.
- Story 1.3 kept `tenant_id` as validation-only future routing input. Continue that boundary here.
- Story 1.2 hardened duplicate metadata corruption with `store.ErrDataLoss`; use the same fail-closed standard for visible find metadata corruption.
- Story 1.1 and Story 1.3 server tests deliberately use `server.Register`; Story 1.4 public tests should not bypass registration when proving gRPC behavior.

### Git Intelligence

- Recent commits show narrow, test-backed Shard/security changes: `d970de3 fix(security): enforce peer Shard scope`, `4013b66 fix(security): harden public API and deploy controls`, `69ad47f feat(shard): coordinate upload pressure pause ownership`, `954bfda feat(shard): harden upload confirmation replay`, and `e0c72ce feat(shard): add upload outbox event boundary`.
- The local pattern is characterization test first, minimal production change, central Store error mapping, exact evidence notes, and full local gates before code review.

### Technical Research Notes

- Repo-pinned versions remain the authority: Go `1.26.4`, gRPC `v1.81.1`, Pebble `v1.1.5`, OpenTelemetry `v1.44.0`. No dependency upgrade or registry search is in scope.
- Official Go `context` docs define Context as carrying deadlines and cancellation signals across API boundaries and warn that `CancelFunc` must be called to release resources. Source: https://pkg.go.dev/context
- Official gRPC status docs define `INVALID_ARGUMENT`, `NOT_FOUND`, `UNAVAILABLE`, and `DATA_LOSS`; `DATA_LOSS` is reserved for unrecoverable data loss or corruption. Source: https://grpc.io/docs/guides/status-codes/
- Official gRPC-Go `status` package for repo-pinned `v1.81.1` remains the central transport-status API. Keep Store/core packages free of `grpc/status` imports. Source: https://pkg.go.dev/google.golang.org/grpc/status
- Official OpenTelemetry RPC semantic conventions caution that request/response metadata capture should be explicitly configured because broad metadata capture can leak sensitive information. Source: https://opentelemetry.io/docs/specs/semconv/attributes-registry/rpc/

### References

- `_bmad-output/planning-artifacts/epics.md` - Epic 1 and Story 1.4 source acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-1, NFR-1 through NFR-7, and user journeys requiring safe reconciliation metadata.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - source of truth for multi-Shard boundary, `FindDocuments` Transaction scope, Content Quarantine metadata behavior, and routing ownership.
- `_bmad-output/implementation-artifacts/1-3-verified-read-and-metadata-inspection.md` - previous story learnings and current read/head/find boundary notes.
- `CONTEXT.md` - glossary, V2 API contract, Transaction-scoped metadata lookup, Projection Resolution, and safety invariants.
- `docs/adr/0014-projection-resolution-boundary.md` - strict vs lenient Projection Resolution semantics.
- `docs/adr/0016-phase-4-partial-eviction-boundary.md` - metadata-only reads for evicted Blocks and no Backend authority for `FindDocuments`.
- `docs/adr/0026-multi-shard-v2-release-boundary.md` - future routing boundary and no hidden cross-Shard scan in discovery.
- `docs/go-style-guide.md` - Go style, errors, tests, metrics, and package conventions.
- `proto/scrap/v1/document.proto` - public `FindDocuments` request/response contract.
- `internal/store/store.go` - Store contract and public metadata type.
- `internal/server/server.go` - public gRPC transport mapping.
- `internal/shard/shard.go` - Store-backed `FindDocuments` implementation.
- `internal/index/resolution.go` - strict `Resolver.ListDocuments` authority.
- `https://pkg.go.dev/context` - official Go context cancellation reference.
- `https://grpc.io/docs/guides/status-codes/` - official gRPC status code semantics.
- `https://pkg.go.dev/google.golang.org/grpc/status` - official gRPC-Go status API for repo-pinned module.
- `https://opentelemetry.io/docs/specs/semconv/attributes-registry/rpc/` - official OpenTelemetry RPC semantic conventions.

## Dev Agent Record

### Agent Model Used

GPT-5 Codex

### Implementation Plan

- Add Story 1.4 characterization tests first for real Shard discovery, registered gRPC discovery, empty results, invalid lookup, fail-closed visible metadata corruption, Backend non-authority, and redacted telemetry/log evidence.
- Keep Projection Resolution authority in `internal/index` and Store/Shards; use `internal/server` only for request validation, security, Store invocation, status mapping, and public `DocumentMeta` rendering.
- Apply only the minimal production correction exposed by the red test: preserve context-derived and existing gRPC status errors around `FindDocuments` Store calls.

### Debug Log References

- FAIL (RED): `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server ./internal/shard -run 'FindDocuments'` - `TestFindDocumentsCanceledContextReturnsBeforeStore` returned `OK`, wanted `Canceled`; `TestFindDocumentsStoreContextErrorPreservesCanceledStatus` returned `Internal`, wanted `Canceled`.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server ./internal/shard -run 'FindDocuments'`.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/server ./internal/shard`.
- FAIL then PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/server ./internal/shard` - first combined run reported `internal/server` PASS and noisy `internal/shard` FAIL without extracted race/failure detail; isolated shard rerun and final combined rerun passed.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard`.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries`.
- PASS: `if git diff -- internal/server/server.go | rg -n 'tx-find-secret|invoice-secret\\.xml|classified-payload|local/shards|\\.blk|\\.idx|/tmp/'; then exit 1; else exit 0; fi`.
- FAIL then PASS: `env GOCACHE=/tmp/scrap-v2-go-build make check` - first run found lint issues in new tests/helper shape; after cleanup, full `make check` passed.
- PASS: `git diff --check`.
- PASS (review patches): `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server ./internal/shard -run 'FindDocuments'`.
- PASS (review patches): `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/server ./internal/shard`.
- PASS (review patches): `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/server ./internal/shard`.
- PASS (review patches): `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries`.
- PASS (review patches): `if git diff -- internal/server/server.go proto/scrap/v1/document.proto internal/store/store.go internal/shard/shard.go internal/index/resolution.go | rg -n 'tx-find-secret|invoice-secret\\.xml|classified-payload|local/shards|\\.blk|\\.idx|/tmp/'; then exit 1; else exit 0; fi`.
- PASS (review patches): `env GOCACHE=/tmp/scrap-v2-go-build make check`.

### Completion Notes List

- Ultimate context engine analysis completed - comprehensive developer guide created.
- Added focused Shard `FindDocuments` tests proving Transaction scope, write order, exact public metadata, successful empty discovery, fail-closed corrupt `.idx` behavior, and zero Backend `GetObject` calls for evicted confirmed Block metadata discovery.
- Added registered gRPC `FindDocuments` tests through a real Shard-backed Store fixture for exact metadata/order, OK empty response, corrupt `.idx` -> `codes.DataLoss`, not-leader -> `codes.Unavailable`, and validation before Store side effects.
- Added server context tests proving already-canceled `FindDocuments` calls do not hit Store and Store-returned `context.Canceled` maps to public `Canceled`, not `Internal`.
- Added redaction evidence for `FindDocuments` spans, metrics, and not-leader logs: bounded RPC labels only, hashed `scrap.transaction.hash`, no raw `transaction_id`, no `document_name`, and no Backend/local layout or payload evidence.
- Changed boundary list: `internal/server` transport mapping only plus new/updated `internal/server` tests and new `internal/shard` tests. `internal/index`, `internal/shard` production discovery, `internal/store`, public protobufs, Block/Frame layout, and routing identity were not changed.
- Typed-error mapping proof: invalid lookup remains `INVALID_ARGUMENT`; corrupt visible metadata remains `DATA_LOSS`; not-leader remains `UNAVAILABLE` with existing `LeaderHint`; empty Transaction remains OK empty response; context cancellation now preserves `CANCELED`.
- Backend-authority proof: `FindDocuments` continues to use strict Projection Resolution and local metadata readability; tests assert no Backend `GetObject` restore on evicted confirmed Blocks. Existing Backend list/HEAD/inventory surfaces are not used by this public discovery path.
- Routing-boundary proof: no multi-Shard router, Shard map, route cache, cross-Shard scan, public response field, or `tenant_id` storage identity was added. Story 2.3 remains owner of public API routing by Transaction.
- Redaction proof: production diff leak scan passed for raw test Transaction/Document/body strings and storage-layout markers; test assertions cover spans, metrics, and logs.
- Lint cleanup converted the existing span status helper to a fixed `rpc.grpc.status_code` helper and split a new metric lookup helper to stay under package complexity limits.
- Review patches resolved: added registered gRPC invalid Transaction/Tenant before-Store evidence, preserved `DeadlineExceeded` status evidence, counted Backend `GetObject`/`HeadObject`/`ListObjects` for non-authority proof, and broadened redaction evidence to production discovery boundary files.
- Deferred review work recorded separately: rebuilding Store errors still map to `INTERNAL` and remain outside Story 1.4 scope as a pre-existing central transport mapping issue.

### File List

- `_bmad-output/implementation-artifacts/1-4-transaction-scoped-document-discovery.md`
- `_bmad-output/implementation-artifacts/deferred-work.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `internal/server/find_documents_internal_test.go`
- `internal/server/find_documents_test.go`
- `internal/server/server.go`
- `internal/server/telemetry_test.go`
- `internal/shard/find_documents_test.go`

### Change Log

- 2026-06-11: Created Story 1.4 Transaction-Scoped Document Discovery context package and marked it ready for development.
- 2026-06-11: Implemented Story 1.4 discovery tests and context-status preservation; marked ready for review.
- 2026-06-11: Resolved accepted code review patches, recorded deferred pre-existing mapping issue, and marked Story 1.4 done.
