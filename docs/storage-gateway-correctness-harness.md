# Storage Gateway Correctness Harness And CI Tiers

Status: planning gate for GitHub issue `#19`
Last updated: 2026-05-22

This document defines the correctness harness tiers for S.C.R.A.P. storage
gateway work. The goal is to make durability and consistency evidence explicit
before production write ACK mode can be enabled.

The current PR gate is `make check`, which runs generated-code checks, vet,
unit tests, race tests, and builds. As heavier harnesses are implemented, they
must be attached to the tiers below rather than added ad hoc.

## Tier Definitions

| Tier | Purpose | Normal PR | Nightly | Dedicated runner | Manual release evidence |
| --- | --- | --- | --- | --- | --- |
| Unit invariants | Fast package-local tests for identity, validation, codecs, command application, placement, and small state transitions. | Required. | Required. | Required if package participates in dedicated suite. | Required as part of release gate. |
| Deterministic simulator | Deterministic shard lifecycle simulation for writes, prepares, commits, reads, duplicate attempts, replay, repair eligibility, and membership events. | Required smoke subset once available. | Longer generated sequences. | Long runs with larger state and failure schedules. | Representative run report with seed/config artifacts. |
| Model-based tests | Compare implementation behavior with a small authoritative model for write lifecycle, visibility, retries, recovery, and repair. | Short bounded subset when stable. | Long generated operation sequences. | Long runs and higher operation counts. | Evidence bundle for storage release candidates. |
| Crash/recovery tests | Prove crash boundaries around byte append, sync, openlog sync, metadata apply, ACK, replay, cleanup, backend jobs, and restore. | Narrow deterministic unit-level crash tests only. | Broader process/fault suites. | Required for filesystem/Raft/backend durability evidence. | Required before production write ACK mode. |
| Fault-injection tests | Inject file IO, clock, backend, OpenBao, Raft transport, and dependency failures. | Fast deterministic smoke cases. | Broader matrix and longer failure schedules. | Required for storage-member and backend/OpenBao evidence. | Required for release-blocking invariants. |
| Race tests | Detect data races in core packages and transport scaffolding. | Required through `make test-race`. | Broader race runs or repeated race runs. | Required for long-running harnesses. | Required release evidence. |
| Goroutine-leak tests | Prove servers, workers, streams, jobs, and simulations stop cleanly. | Required for new long-lived goroutine owners. | Broader leak checks across integration suites. | Required for long-running harnesses. | Required for release readiness. |
| Fuzz tests | Exercise parsers, binary formats, protobuf compatibility, request validation, and corruption handling. | Corpus regression only when package has fuzz targets. | Time-bounded fuzz runs. | Longer fuzz campaigns for storage formats and codecs. | Required for format/release evidence when affected. |
| Compatibility tests | Protect old readers, old writers, old stored bytes, mixed versions, generated code, metadata, published metadata, blocks, indexes, and envelopes. | Required for schema/format changes and generated-code checks. | Mixed-version and fixture expansion. | Required for migration or format-gate evidence. | Required before format or metadata feature gates. |
| Performance-smoke tests | Detect obvious latency, throughput, allocation, GC, fsync, and backlog regressions. | Lightweight smoke only where stable and cheap. | Trend collection if runners are stable enough. | Authoritative performance profile on pinned hardware/runtime. | Required for production capacity/readiness claims. |

## CI Gate Policy

Normal PR checks are for fast feedback and review protection. They must stay
bounded enough to run on ordinary GitHub-hosted runners unless a specific PR
changes dedicated-runner configuration.

Normal PR checks include:

- `buf lint`, breaking checks where a base exists, code generation, and
  generated-code cleanliness;
- `gofmt`, `go vet`, ordinary `go test ./...`, `go test -race ./...`, and
  build checks through `make check`;
- unit invariants for changed packages;
- deterministic simulator smoke tests once those packages exist;
- corpus-regression fuzz tests for packages that define fuzz targets;
- compatibility fixture tests for schema or storage-format changes.

Nightly checks are for broader correctness signal without slowing every PR.
They should run longer generated sequences, wider fault matrices, repeated race
or leak checks, long model-based tests, crash/recovery suites, and time-bounded
fuzzing.

Dedicated-runner checks are required when the result depends on stable local
storage, filesystem behavior, kernel/runtime details, backend/OpenBao smoke
deployments, or representative performance. GitHub-hosted runners are not
authoritative for fsync-heavy crash/recovery claims or production performance.

Manual release evidence collects named artifacts for the production readiness
gate. Evidence must include command, commit SHA, runner profile, seed/config,
duration, result, and owner. A release gate may use manual evidence only when
automation is impractical or too expensive for normal CI.

## Correctness Oracle

Every simulator, model, crash, fault, and recovery harness must assert these
storage contract invariants where relevant.

Acknowledged documents:

- once a write is acknowledged, the document is readable or repairable from
  verified local, peer, or backend sources;
- acknowledged visibility is backed by authoritative metadata, not a local
  projection shortcut;
- local post-commit bookkeeping failure does not revoke a successful commit.

Unacknowledged and partial documents:

- partial writes are never visible to readers;
- prepared but uncommitted bytes remain unreadable after restart;
- cleanup may remove abandoned attempts only when it cannot remove committed
  visibility or acknowledged bytes.

Corrupt bytes:

- corrupt bytes are not streamed as valid document data;
- reads verify every touched frame before streaming;
- suspect sources are quarantined from serving;
- all-sources-corrupt outcomes produce typed data-loss/integrity evidence.

Visibility:

- metadata visibility is controlled by shard authority and freshness rules;
- stale leaders cannot acknowledge writes or serve fresh reads;
- read-only imported metadata is bounded by source ownership and import
  freshness, and conflicts fail closed.

Idempotent retries:

- duplicate writes with the same idempotency key and same payload return the
  existing result or continue the same attempt;
- duplicate writes with conflicting payload or metadata fail closed;
- duplicate jobs and admin operations verify existing side effects instead of
  repeating unsafe mutation;
- unknown client outcomes are recoverable through stable IDs.

Recovery:

- replay rebuilds local projections from authoritative metadata;
- snapshots and compaction preserve state needed by lagging or restarting
  replicas;
- backend upload, restore, repair, scrub, rewrap, and admin operation state
  resumes or reaches a typed terminal state after restart.

## Randomized Failure Reproduction

Randomized, model-based, fuzz, and fault-injection tests must preserve enough
metadata to reproduce failures.

Required failure artifact fields:

- commit SHA and dirty-tree marker;
- test name, package, tier, and harness version;
- random seed or fuzz corpus path;
- generated operation sequence or reduced counterexample when available;
- workload shape, document sizes, operation mix, and configured limits;
- runtime config, shard count, member count, backend/OpenBao fake config, and
  filesystem profile;
- failure schedule for crashes, dropped/reordered messages, dependency errors,
  throttling, corrupt reads, and restarts;
- Go version, OS/kernel, filesystem, runner class, and relevant environment;
- logs, metrics, traces, and operation IDs needed for diagnosis without
  exposing document bytes or secrets.

Nightly and dedicated runs should upload these artifacts even for flakes that
pass on retry. Flakes are correctness signals until reduced or explained.

## Harness Design Rules

- Prefer deterministic fake clocks, fake backends, fake OpenBao Transit, and
  controllable Raft transport for correctness tests.
- Keep model state small and authoritative; the model defines expected
  externally visible behavior, not implementation internals.
- Use Go fuzzing for parsers, codecs, and binary/protobuf boundary inputs.
- Choose model-test libraries through correctness-harness implementation work
  or a follow-up ADR only when a concrete library choice changes package APIs,
  scheduler hooks, or release gates.
- Production code must expose test seams through explicit interfaces, not
  hidden globals or test-only conditionals in core logic.
- Performance-smoke tests protect against obvious regressions; production
  capacity claims require measured deployment-profile evidence.
