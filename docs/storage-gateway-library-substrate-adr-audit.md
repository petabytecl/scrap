# Storage Gateway Library And Substrate ADR Audit

Status: planning gate for GitHub issue `#18`
Last updated: 2026-05-22

This audit records which hard-to-reverse library and substrate decisions are
already covered by ADRs, which need new ADR coverage, and which are intentionally
deferred because they are local implementation choices or not yet selected.

The rule for adding ADRs is conservative: add an ADR only when the choice is
hard to reverse, surprising without context, and based on a real trade-off.

## Audit Result

| Topic | Current decision | Coverage | Action |
| --- | --- | --- | --- |
| Custom public API | Expose a custom gRPC API instead of S3 compatibility. | ADR 0001 | Covered; no new ADR. |
| Document storage shape | Store immutable documents in sealed blocks with indexes and checksums. | ADR 0002 | Covered; no new ADR. |
| Raft substrate | Use transaction-keyed Raft metadata shards; default Go library is `go.etcd.io/raft/v3`. | ADR 0003, ADR 0004 | Covered; no new ADR unless replacing `go.etcd.io/raft/v3` or changing consensus shape. |
| Go implementation substrate | Primary service is Go with `grpc-go`, protobuf, Raft, Pebble, native backend SDK adapters, OpenBao Go client, and Go crypto. | ADR 0004 | Covered at substrate level; individual helper libraries still follow this audit. |
| Pebble and local projection storage | Pebble is the default local projection/store substrate, but local projections are rebuildable derived state. | ADR 0004, ADR 0010 | Covered enough for v1. Add an ADR only if local projection storage becomes authoritative or a different embedded store is selected for production. |
| Buf and protobuf generation | Use versioned private protobuf for internal and published metadata; generated-code checks are part of compatibility discipline. | ADR 0010 | Covered; no new ADR. |
| Filesystem durability assumptions | Production safety depends on Linux local filesystem sync semantics, not arbitrary mounted storage. | New ADR 0013 | Added ADR 0013 because this affects production deployment shape and crash-recovery evidence. |
| OpenBao envelope boundary | Use OpenBao Transit envelope encryption and OpenBao Go client boundary for deployment-scoped KEKs. | ADR 0005, ADR 0004 | Covered; no new ADR unless changing KMS provider, exporting key material, or moving crypto into another service. |
| Backend SDK boundaries | Backend portability is behind provider-neutral adapters; native backend SDKs are implementation details behind that boundary. | ADR 0001, ADR 0004, ADR 0008 | Covered at boundary level. Add provider-specific ADRs only for hard-to-reverse SDK or backend semantics. |
| Model-test libraries | No concrete model-test library is selected yet. Correctness tiers require deterministic simulator/model checks. | Roadmap and issue `#19` | Deferred to correctness harness work. Add an ADR only when a model-testing library materially shapes tests or architecture. |
| DI/lifecycle framework | Default is explicit constructors and small consumer-owned interfaces. DI/lifecycle frameworks such as `uber/fx` or a Gaz-style container are not adopted. | Package architecture guide | Deferred. Any adoption needs an ADR because it changes composition, lifecycle, testing, and dependency visibility. |
| Error aggregation helpers | Typed errors and retryability classes are required. `multierr`-style helpers may aggregate cleanup/shutdown errors without replacing the primary typed failure. | Durability coding guidelines | No ADR needed unless an error framework becomes part of package APIs or transport mapping. |
| Test assertion libraries | `testify/require`-style assertions and limited `suite` use are test-only readability choices. | Durability coding guidelines | No ADR needed unless a test framework becomes mandatory for production harness architecture. |
| Observability libraries | OpenTelemetry, Prometheus exposition where needed, and `log/slog` remain implementation candidates. | Design notes and issue `#20` | Deferred to observability standards. Add ADR only if a vendor or framework choice becomes hard to reverse. |

## New ADR Added By This Audit

- [ADR 0013: Require Linux local filesystem durability assumptions](adr/0013-require-linux-local-filesystem-durability-assumptions.md)

## Deferred Decisions

### DI And Lifecycle Frameworks

Do not adopt `uber/fx`, Gaz-style DI, service locators, or global registries by
default. The current default is explicit constructors, narrow interfaces, and
composition in `cmd/scrapd` / `internal/node`.

Revisit this only if manual wiring becomes a demonstrable source of lifecycle
bugs or review burden. A future ADR must define allowed packages, lifecycle
hooks, shutdown behavior, test override strategy, and how the framework is kept
out of storage-core APIs.

### Model-Test And Property-Test Libraries

Issue `#19` owns correctness harness and CI tiers. It may choose libraries for
deterministic simulation, property tests, model checking, or linearizability
checks.

An ADR is needed only if the selected library shapes package APIs, scheduler
interfaces, fault-injection hooks, or release gates. Lightweight assertion or
fixture helpers do not need ADRs by themselves.

### Error Helpers

Typed errors are the architecture. Helper packages are implementation details.
`multierr`-style aggregation is acceptable for cleanup paths when the primary
failure class remains inspectable. It is not a substitute for durability,
retryability, corruption, capacity, authorization, or crypto-unavailable error
classification.

### Test Readability Helpers

Readable tests are valuable, but test helper choice should not become
architecture by accident. `testify/require` can be introduced for clearer
assertions. `testify/suite` should be limited to integration or harness tests
with expensive shared setup. Simple table tests remain preferred for local
domain behavior.

## ADR Coverage Checklist For Future Dependencies

Before adding a production dependency, answer:

- Does the dependency appear in storage-core exported APIs?
- Does it change process lifecycle, shutdown, retry, or readiness behavior?
- Does it affect on-disk format, consensus behavior, encryption, durability,
  or recovery evidence?
- Does replacing it require a migration, operator retraining, or deployment
  topology change?
- Does it introduce hidden global state, background goroutines, network calls,
  logging, metrics, or config interpretation?

If the answer is yes to any of these, prefer an ADR or an explicit update to an
existing ADR. If the dependency is a small local helper with no boundary impact,
document it in package guidelines or code review instead.
