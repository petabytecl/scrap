---
project_name: scrap
user_name: Coto
date: 2026-06-07
sections_completed:
  - discovery
  - technology_stack
  - language_specific_rules
  - framework_package_boundary_rules
  - testing_rules
  - code_quality_style_rules
  - development_workflow_rules
  - critical_dont_miss_rules
existing_patterns_found: 7
status: complete
rule_count: 119
optimized_for_llm: true
---

# Project Context for AI Agents

_This file contains critical rules and patterns that AI agents must follow when implementing code in this project. Focus on unobvious details that agents might otherwise miss._

---

## Technology Stack & Versions

- Go module: `github.com/petabytecl/scrap`; Go version is `1.26.4` in `go.mod` and `tools.go.mod`.
- Version sources are split intentionally: runtime dependencies live in `go.mod`, Go-managed tools in `tools.go.mod`, and Kind/Helm/Cilium defaults in `Makefile`; re-check those files before changing version claims.
- API/wire format: gRPC + protobuf. Buf v2 reads `proto/` and writes Go/gRPC output to `gen/go`; generated files may be absent in a fresh checkout. Run `make proto` or `buf generate`; never edit generated files by hand.
- Core storage/consensus: etcd Raft `v3.6.0`, etcd server `v3.6.11`, Pebble `v1.1.5`.
- Backend boundary: filesystem and S3 implementations satisfy `internal/backend.Backend`; keep Backend-facing code behind that package boundary. S3 uses AWS SDK for Go v2, including S3 `v1.101.0`.
- Telemetry: OpenTelemetry API/SDK `v1.44.0`, OTLP exporters `v1.43.0`, `otelgrpc v0.69.0`, Prometheus client `v1.20.5`, Prometheus exporter `v0.53.0`.
- Logging: application code uses Go `log/slog`; `internal/logbridge` adapts zap and etcd Raft logging into `slog`. Zap is not the application logging API.
- Tooling/environment: Buf `v1.70.0`, golangci-lint `v2.12.2`, gotestsum `v1.13.0`, govulncheck `v1.3.0`, Kustomize `v5.8.1`, Kind `v0.31.0`, Helm `v3.21.0`, Cilium `1.19.4`; E2E Backend behavior is commonly exercised through LocalStack.
- Release image: `scrapd` builds with `CGO_ENABLED=0`, uses `FROM scratch`, runs as non-root `65532:65532`, and has entrypoint `/scrapd`; do not assume a shell, package manager, dynamic libraries, or runtime tools exist in the image.

## Critical Implementation Rules

### Language-Specific Rules

- Follow `docs/go-style-guide.md` for Go design, naming, errors, concurrency, tests, metrics, and docs; `.golangci.yml` only covers the mechanical/static parts.
- Keep packages domain-specific under `internal/`; do not create `util` or `common` packages. The package name should supply context, so avoid stutter like `block.BlockWriter`.
- Define interfaces at the consumer boundary and keep most interfaces to 1-3 methods; return concrete types unless a package-level contract like `store.Store` or `backend.Backend` already exists.
- Use config structs with `applyDefaults()`/validation before use. Malformed explicit env/config input should return an error naming the key, not silently fall back.
- Keep happy paths left-aligned: return early for preconditions and errors, avoid `else` around main logic, and keep functions small enough to satisfy the configured complexity gates.
- Wrap errors with `%w` using the style `"package: operation: %w"`; use `errors.Is`/`errors.As` for sentinel and typed errors. Log or return an error, not both.
- Use `context.Context` on I/O, RPC, storage, Backend, and long-running operations; propagate cancellation rather than hiding it.
- Prefer pointer receivers. Use value receivers only for small immutable types without sync primitives.
- Do not embed mutexes; keep sync fields unexported and grouped with the data they protect.
- Application logging uses `log/slog`; do not introduce new zap-native application logging. Use `internal/logbridge` when dependency logging needs adaptation.

### Framework / Package Boundary Rules

- Protobuf files under `proto/scrap/v1` are the source of truth for public, peer, and Raft command wire contracts. Regenerate Go code through Buf after proto changes.
- `tenant_id` may appear on API requests for future routing, but storage identity is `(transaction_id, document_name)`; do not add `tenant_id` to storage identity without an ADR.
- Keep public, peer, and admin surfaces separate: `DocumentService` is public gRPC; `PeerService` handles replication, Block transfer, consistency, index rebuild, and Raft forwarding; `internal/admin` is HTTP operator/control-plane. ADR 0019 governs security-sensitive surface boundaries.
- `internal/cmd` is the composition root. It wires listeners, telemetry, Backend, peer client/server, admin server, and Shard lifecycle; feature packages should expose narrow options/interfaces instead of importing each other directly.
- `internal/server` maps gRPC requests to `internal/store.Store`; storage behavior belongs behind the store/shard boundary, not in transport handlers.
- `internal/peer` is a transport boundary. Connect it to Shard behavior through narrow interfaces such as `ReplicationSink`, `RebuildHandler`, and `RaftRouter`; do not make peer code depend directly on `internal/shard`.
- `internal/shard` owns Shard orchestration and authority adapters: leader/read gates, Raft apply, Block paths, Openlog, upload, eviction, restore, scrub coordination, Store error mapping, and side effects against Raft/Pebble/Backend/local files.
- Core storage/consensus packages must not import `grpc/status` or `grpc/codes`; Store/core packages return domain errors and transport layers map them to gRPC status. `make package-boundaries` enforces this for current core packages.
- Document bytes stay out of Raft per ADR 0001. Raft commands carry metadata and trace context; peer gRPC carries byte replication and Block transfer.
- `internal/index` owns Pebble Projection and Projection Resolution. Strict client reads fail closed on visible metadata corruption; recovery/replay may use the ADR 0014 lenient path.
- `internal/block` owns Block/Frame encoding, `.blk`/`.idx` layout, verification, listing, and quarantine. Do not duplicate Block index parsing outside this package. Block/Frame layout is governed by ADR 0003.
- `internal/localblock` owns local Block lifecycle marker files, classification, and filesystem transitions. It does not decide Document visibility, durable upload authority, or read availability policy.
- `internal/eviction` owns the in-memory eviction campaign workflow: plan assembly, TTL, stale/member validation, running/completed result state, and result aggregation.
- `internal/backend.Backend` is the Backend abstraction; filesystem and S3 implementations live under `internal/backend`. Backend object identity is governed by ADR 0009.
- Upload Outbox, Confirmed Upload Catalog, Local Block Lifecycle, and eviction campaign behavior are governed by ADR 0010 and ADR 0016-0018; do not relocate those responsibilities casually.
- `internal/spike` is Phase 1 scaffold/test context, not production contract. Do not add production dependencies on it.
- Test hooks and pprof are dangerous surfaces. Prod-like production overlays must not enable `SCRAP_TEST_HOOKS`; only prod-like E2E overlays may do so, and gate scripts enforce the distinction.
- Changes to storage format, wire protocol, dependency choices, security/encryption contracts, or cross-package boundaries require an ADR in `docs/adr/`.

### Testing Rules

- For behavior changes and bug fixes, add or update a failing test first when feasible; if not practical, state why in the PR/test plan.
- Unit tests live next to code as `internal/<pkg>/*_test.go`. Prefer same-package tests for internal invariants; use `<pkg>_test` when validating exported package contracts. Integration tests live in `test/integration/`; E2E tests live in `test/e2e/`; stress tooling lives in `test/stress/`.
- Unit tests stay inside package boundaries and must not require external services, real object storage, network listeners, wall-clock timing, shared filesystem paths, persistent global state, or test execution order. Inject clocks, IDs, randomness, and backends where needed.
- Use the Go stdlib test stack: `testing` plus standard-library helpers such as `httptest`. Do not add testify, gomega, gomock, or assertion/mocking libraries without an ADR-level dependency decision.
- Use table-driven tests when there are multiple meaningful cases; use direct one-off tests when only one behavior matters. Assertion messages use `got` before `want`.
- Use `t.Fatalf` when setup failed or further checks are meaningless; use `t.Errorf` when the test can continue. Test helpers must call `t.Helper()`. Use `t.TempDir()` for filesystem isolation and `t.Cleanup()` for teardown.
- Keep test doubles local and minimal. Shared fakes are allowed only when three or more packages need the same behavior and the fake models a real boundary, not test convenience.
- Fixtures must default to valid domain objects. Invalid fixtures should be named for the violated invariant.
- For storage and recovery changes, test crash windows, corruption, fail-closed behavior, idempotency, restart/rebuild behavior, and process-lifetime persistence where relevant.
- For Raft, watcher, scrub, retry, worker, or goroutine changes, test cancellation, timeout, cleanup, and race-sensitive paths. Do not use sleeps as synchronization; prefer contexts, readiness probes, fake clocks, or bounded polling with clear failure messages.
- For proto/gRPC changes, test compatibility around decoding, optional/required field behavior, unknown-field tolerance where applicable, and transport error mapping.
- For telemetry/evidence changes, assert low-cardinality labels, identifier privacy, and current-run evidence. Do not add raw `transaction_id`, `document_name`, idempotency keys, Backend keys, or unbounded notes as metric labels.
- Flaky tests are release risk. Do not fix flakiness by increasing timeouts or adding retries unless paired with a root-cause issue.
- Choose the narrowest verification gate that proves the change: `make test` for package logic; `make test-race` for concurrency/shared state; `make integration` for Pebble, Backend, persistence, local services, or cross-package contracts; `make e2e-up` or `make tier2-e2e-up` for deployed gateway workflows; `make tier3-evidence-up` for telemetry/evidence/security/privacy claims; `make tier1-check` before broad review. A lower tier passing does not waive a higher tier when the changed behavior only exists at that higher tier.

### Code Quality & Style Rules

- All Go code must follow `docs/go-style-guide.md`; `.golangci.yml` is the executable style contract. Do not bypass configured linters with local-only flags.
- Run formatting through `make fmt`/`make fmt-check`. Formatting and import ordering are owned by golangci formatters: gofmt, gofumpt, goimports, and gci. Do not hand-format imports except to remove unused imports or resolve compile errors.
- `make static` is a required implementation gate. It runs manifest checks, package-boundary checks, proto checks, lint, and other static validation. Treat new failures as part of the change; do not defer them as cleanup.
- `gen/` and `internal/spike/` are lint exclusions for generated or experimental code. Do not copy their style into production packages.
- Do not edit generated files directly. Update the source proto/config/template and regenerate through the repo's documented commands.
- Keep exported APIs documented with godoc and every package documented with a package comment, usually in `doc.go`.
- Prefer existing package patterns and error/status conventions before introducing new abstractions, helper types, or dependency choices.
- New metrics must use OpenTelemetry instruments. Prometheus client metrics are legacy/migration surface only unless preserving existing behavior while replacing it.
- Metric names use `scrap.<subsystem>.<measurement>` with OTel units; keep attributes bounded and low-cardinality.
- Define metrics interfaces only at meaningful package or consumer boundaries; avoid creating interfaces solely to wrap OpenTelemetry. Instrument creation errors are startup failures.
- New metrics and logs should answer an operational question: health, throughput, latency, durability, leadership, repair, or Backend behavior. Avoid instrumentation that cannot drive debugging, alerting, or capacity decisions.
- Logs go through `log/slog` and `internal/logbridge`. Prefer context-aware logging methods so correlation data is attached consistently.
- Use structured slog attributes instead of interpolated log strings; keep attribute names stable because dashboards and alerts depend on them.
- Expected client-driven outcomes are not server `ERROR` logs: bad input, missing Documents, not-leader redirects, and client-cancelled streams should use appropriate status codes, counters, and lower-severity logs.
- Do not add raw `transaction_id`, `document_name`, idempotency keys, Backend object keys, file paths, trace IDs, or request IDs as explicit log fields in deployed Cells. Use hashed identifiers unless the local debug override explicitly allows raw IDs.
- Public request paths, background workers, and streams must carry `context.Context`; cancellation and deadline handling are part of the API contract.
- Optimize only hot paths proven by benchmark/profile evidence. Preallocate when sizes are known, prefer `strconv` over `fmt` for primitive conversion, and do not add `sync.Pool` speculatively.
- Wrap errors with `%w` at package boundaries; use typed/sentinel errors only when callers branch on them.
- `nolint` directives must name the linter, explain the exception, and stay on the narrowest line/scope possible.

### Development Workflow Rules

- Read `CONTEXT.md` before code changes. Use the exact glossary terms (`Document`, `Transaction`, `Block`, `Frame`, `Shard`, `Cell`, `Member`, `Backend`, etc.) and avoid the synonyms it forbids.
- Check `git status --short` before edits. If target files are already modified, inspect the diff and preserve existing user intent. Stage only intentional paths; before commit, inspect `git diff --cached`.
- Production package work must trace to a PRD, story, accepted ADR, or GitHub issue before implementation starts. If no source exists, create or request one before editing production code.
- Published execution work should use GitHub Issues. V2 production work must carry the `v2` label and `storage-gateway-v2` milestone per `docs/agents/issue-tracker.md`.
- Every production PR must include traceability to the governing PRD, story, ADR, or GitHub issue. Slice issues should state which parent requirement or acceptance criterion they advance.
- Use `_bmad-output/planning-artifacts` and `_bmad-output/implementation-artifacts` for generated BMAD work products. Promote only durable decisions, domain rules, operational policies, and accepted architecture into `docs/`.
- Create or update an ADR before changing storage format, wire protocol, dependency/runtime choices, security/encryption/auth contracts, or cross-package ownership boundaries. Do not create ADRs for local implementation tactics, private helpers, mechanical refactors, or test-only changes that preserve existing contracts.
- Spike code under `internal/spike` is evidence only. Production packages must not import it or treat it as a stable foundation without a follow-up issue/ADR that promotes the decision.
- For generated protobuf changes, update `proto/`, run `make proto`, and ensure `make proto-check` is clean. Do not hand-edit generated code.
- For behavior changes and bug fixes, add or update the failing test first, then implement, then refactor. Refactors require before/after gate evidence.
- Before broad review, handoff, commit, or closure, run the narrowest relevant gate first, then any higher gate required by blast radius. `make tier1-check` is the default broad local gate. Tier 2/Tier 3 gates are required when runtime behavior, production-readiness evidence, deployment contracts, integration claims, or PRD closure are affected.
- PRD closure for production-readiness work requires current linked evidence. Follow `docs/prd-closure-policy.md`; local manual evidence alone is not enough when the policy requires GitHub Actions evidence.
- Merged slices are evidence, not closure. Parent closure requires accepted evidence links, linked artifacts, no open blocking review/CI/security findings, and a written remaining-scope check. Deferred work must be linked to follow-up issues before closing the parent.
- Do not commit or push unless explicitly requested. Commit messages use `<type>: <description>` with types `feat`, `fix`, `refactor`, `docs`, `test`, `chore`, `perf`, or `ci`; commit bodies/trailers must not include agent attribution.
- Before commits, check for hardcoded secrets, unsafe auth/authz changes, missing input validation, rate-limit gaps, sensitive error/log leakage, and raw identifier exposure.
- Final reports must list exact verification commands run and their result: pass, fail, or skipped with reason.

### Critical Don't-Miss Rules

- Do not treat S.C.R.A.P. as an S3-compatible API or generic object store. It is a gRPC storage gateway for immutable Documents grouped by Transaction.
- Do not add `tenant_id` to storage identity, Backend object identity, cardinality-bearing metrics, or deployed logs without an ADR. Do not let tenant language imply storage partitioning, customer isolation, billing scope, or observability identity unless the requirement explicitly says so.
- Never allow API-level Document mutation, overwrite, or delete after ACK. Duplicate-write handling must distinguish exact replay from conflicting payload/metadata and preserve immutability/idempotency.
- Do not ACK writes from memory, cache, Projection state, queued work, Upload Outbox state, or pending Backend upload. ACK requires the repo-defined local durability path for metadata and Block bytes.
- Do not put Document bytes or Block payload bytes in Raft. Raft carries commands, metadata, and trace context; peer gRPC and Block transfer carry bytes.
- Do not change Block layout, Frame encoding, Backend object key format, or public/peer/admin wire contracts without an ADR and compatibility/migration plan.
- Do not use the Pebble Projection as production source of truth. Outside the Phase 1 spike, it is rebuildable from Raft log replay plus Block bytes.
- Client reads must fail closed on visible metadata corruption. ADR 0014 lenient Projection Resolution is for recovery/replay paths, not normal reads.
- Do not make rebuild/replay paths visible as successful client reads until metadata and Block-byte verification have completed.
- Do not make Backend upload part of the write ACK path. Documents are ACK'd from local replica durability; sealed Blocks upload asynchronously through the Shard leader.
- Do not list, probe, heal from, or use Backend inventory/object existence as a consistency oracle in hot read/write paths. Backend access should follow confirmed Block metadata and explicit restore/upload workflows.
- Do not conflate Upload Outbox, Confirmed Upload Catalog, Local Block Lifecycle, Backend inventory, or eviction state. They are separate authority and evidence models.
- Local Block Lifecycle is per-Member filesystem evidence only; it must not decide Document visibility, durable upload authority, Shard membership, or client read availability policy.
- Do not infer Shard membership, leadership, or byte-serving authority from local files, hostnames, Backend objects, cached peer addresses, network address, or certificate presence. Use the authoritative membership/metadata path.
- Do not conflate `cell_id`, `member_hostname`, and `member_id`. Local non-production identity defaults must stay visibly marked in admin output, metrics, and diagnostics, and must never satisfy production ACK, peer, or admin gates.
- Do not import `internal/spike` from production packages or promote spike code by copying it into production without a follow-up issue/ADR.
- Do not use raw `document_name`, Backend keys, Block IDs, or request fields as filesystem paths. Path/object-key construction must use canonical encoding, bounds checks, and traversal-safe joins.
- Do not buffer full Documents, Blocks, uploads, restores, or peer transfers in memory. Production paths must stream with bounded buffers, size limits, cancellation, and backpressure.
- Do not ignore `context.Context` cancellation/deadlines in peer, Backend, Raft-adjacent, filesystem, transfer, rebuild, or worker loops.
- Do not add package-level globals, background goroutines, singleton caches, cross-Shard coordination, global scans, or shared mutable registries in production hot paths without lifecycle ownership, shutdown, tests, and an ADR when the boundary changes.
- Production and prod-like overlays must not enable `SCRAP_TEST_HOOKS`, unauthenticated pprof, or debug-only bypasses; these surfaces must stay local/test scoped and excluded from readiness evidence.
- Do not expose admin, peer, pprof, metrics, or test-hook surfaces across the wrong network boundary. Bind scope, authn/authz, rate limits, and deployment overlay behavior must be verified.
- Do not treat OpenBao/key material failures as recoverable production defaults. Production encryption paths must fail closed unless a documented local override is active.
- Do not log or metric raw `transaction_id`, `document_name`, idempotency keys, Backend object keys, file paths, trace IDs, request IDs, sensitive peer addresses, auth claims, gRPC metadata, or dependency error strings that embed paths/object keys. Telemetry evidence must redact or bound identifiers across logs, metrics, traces, exemplars, screenshots, and CI artifacts; low-cardinality labels are mandatory.
- Do not introduce zap-native application logging. Application logging is `log/slog`; dependency logging goes through `internal/logbridge`.
- Do not add runtime dependencies, assertion libraries, mocking frameworks, or native Prometheus metrics as convenience shortcuts without an ADR-level dependency decision.
- Do not fix flaky tests by only increasing timeouts, sleeps, retries, or CI resource limits. Pair any timing change with root-cause evidence and deterministic readiness checks.
- Do not disable, skip, quarantine, or weaken tests, race checks, security scans, coverage gates, e2e gates, or stress gates to make CI green without a linked follow-up issue and explicit risk acceptance.
- Do not treat mocked Backend, mocked Raft, or local filesystem-only tests as sufficient evidence for cross-Cell behavior, recovery, upload confirmation, or production closure.
- Do not merge behavior that passes unit tests but lacks failure-path evidence for corruption, replay, duplicate writes, partial uploads, peer loss, restart, and timeout/cancellation cases.
- Do not relax authentication, authorization, encryption, rate limits, public/peer/admin boundaries, or production ACK gates without an ADR, explicit requirement trace, and security-focused verification.
- Do not close a PRD requirement, GitHub issue, milestone item, story, production-readiness claim, or phase from implementation intent, stale local notes, old CI runs, outdated CodeQL/security state, unlinked logs, local output alone, or a merged PR alone. Closure requires current attributable evidence, accepted criteria, linked artifacts, issue/milestone alignment, and required GitHub Actions/Tier/CodeQL/hosted CI proof when applicable.
- Do not create, rename, or split product concepts, workflow states, labels, milestones, BMAD artifacts, or user-facing API terms in a way that loses traceability to the source requirement, acceptance criteria, issue, PRD, or ADR.

---

## Usage Guidelines

**For AI Agents:**

- Read this file before implementing code in this repo.
- Follow all rules exactly as documented.
- When in doubt, prefer the more restrictive option and preserve existing repo boundaries.
- Update this file when new durable implementation patterns emerge.

**For Humans:**

- Keep this file lean and focused on agent needs.
- Update it when technology stack, package boundaries, or workflow policies change.
- Review it periodically for outdated rules.
- Remove rules that become obvious or duplicated elsewhere.

Last Updated: 2026-06-07
