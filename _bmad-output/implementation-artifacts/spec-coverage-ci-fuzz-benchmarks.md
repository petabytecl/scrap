---
title: 'Harden product coverage, fuzzing, and benchmark CI'
type: 'chore'
created: '2026-07-18'
status: 'done'
review_loop_iteration: 0
baseline_commit: '03798da1b57429d2243732c061784ca859f3c343'
context:
  - '{project-root}/docs/go-style-guide.md'
  - '{project-root}/docs/adr/0006-build-system-and-ci-structure.md'
  - '{project-root}/docs/adr/0022-testcontainers-integration-fixtures.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** Coverage currently instruments test harnesses, fails to exclude `internal/spike`, omits tagged LocalStack/OpenBao coverage from Codecov, and has permissive global gates. The repository also has no fuzz targets or runnable microbenchmarks for its highest-risk parsers and read paths.

**Approach:** Measure only shipped `cmd/**` and `internal/**` packages, upload normal and tagged-integration coverage separately for Codecov to merge, introduce risk-based fixed floors, and add bounded parser fuzzing plus informational hot-path benchmarks with CI smoke execution.

## Boundaries & Constraints

**Always:** Preserve production behavior and wire/storage formats; use only Go's standard testing/coverage tools and existing dependencies; bound fuzz input and runtime; seed valid and corrupt encodings; report benchmark allocations and bytes; build fixtures outside benchmark timers; keep unit, integration, fuzz, benchmark, race, Tier 2, and Tier 3 meanings distinct; keep CI failures visible after report upload.

**Ask First:** Any fuzz discovery that requires production-code modification; any proposed floor below the measured current baseline; any need to serialize previously parallel unit and Testcontainers jobs; any new dependency or persistent corpus larger than inline seeds.

**Never:** Add product features, release evidence, performance optimizations, retry-based flake masking, branch-coverage claims, machine-dependent latency thresholds, 128 MiB routine benchmarks, fuzzing under the race detector, or changes to Tier 2/Tier 3 behavior.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Product coverage | Normal and tagged integration suites | Profiles instrument only shipped packages; Codecov merges both uploads | Test failure remains a failed CI job after artifacts upload |
| Harness packages | `scripts`, `test/**`, fixtures, `internal/spike`, `enctest` | Tests still execute where applicable but do not dilute product coverage | Empty or failed package expansion stops the target |
| Arbitrary encoded bytes | Bounded Frame, Block `.idx`, or Pebble Projection value input | Fuzzer never panics or allocates beyond defined bounds; successful decodes preserve semantic round trips | Invalid encodings return errors without becoming test failures |
| Benchmark smoke | One iteration on an unpinned CI runner | Every benchmark runs, validates its result, and emits `ns/op`, `B/op`, and `allocs/op` | No performance percentage or latency gate |

</frozen-after-approval>

## Code Map

- `Makefile` -- product/test package selection and coverage, fuzz, benchmark targets.
- `.github/workflows/ci.yml` -- parallel unit/integration uploads and fuzz/benchmark smoke job.
- `codecov.yml` -- fixed project/patch targets and risk-oriented components.
- `internal/block/frame_fuzz_test.go` -- public Frame parser fuzzing.
- `internal/block/index_fuzz_test.go` -- Block `.idx` V1/V2 codec fuzzing.
- `internal/index/value_fuzz_test.go` -- Pebble Projection transaction-value codec fuzzing.
- `internal/block/reader_benchmark_test.go` -- verified read and Block verification benchmarks.
- `internal/index/resolution_benchmark_test.go` -- Projection Resolution benchmarks.
- `docs/development-guide.md` -- developer commands and measurement semantics.

## Tasks & Acceptance

**Execution:**
- [x] `Makefile` -- separate executed test packages from product instrumentation; fix exact exclusions; add tagged integration coverage/JUnit, three bounded fuzz commands, and benchmark/smoke targets.
- [x] `.github/workflows/ci.yml` -- upload unit and integration coverage/JUnit independently, add required fuzz/benchmark smoke, and include it in the aggregate check without serializing unit and integration execution.
- [x] `codecov.yml` -- set project and patch targets to 80% with no tolerance; remove spike; add component floors: storage 82%, Shard authority 80%, Backend lifecycle 82%, security/control 80%, integrity background work 84%, transport/composition 75%.
- [x] `internal/block/*_fuzz_test.go`, `internal/index/value_fuzz_test.go` -- add bounded inline corpora and semantic invariants for Frame, Block `.idx`, and Pebble Projection decoders.
- [x] `internal/block/reader_benchmark_test.go` -- benchmark 128 KiB/4 MiB verified reads and clean 64 MiB Block verification, reporting bytes and allocations.
- [x] `internal/index/resolution_benchmark_test.go` -- benchmark worst-case seven-Block `ResolveDocument` and `ListDocuments` with fixtures built before timing.
- [x] `docs/development-guide.md` -- document product-only statement coverage, merged Codecov uploads, fuzz commands, and informational benchmarks.

**Acceptance Criteria:**
- Given the coverage targets, when package lists are expanded, then `scripts`, `test/**`, generated code, `internal/spike`, and `internal/encryption/enctest` are absent from `-coverpkg` while their applicable tests remain runnable.
- Given normal and tagged integration CI jobs pass, when Codecov processes the commit, then both product profiles and both JUnit reports are uploaded without hiding failures.
- Given each fuzz target runs for its configured budget, when arbitrary bytes are decoded, then it completes without panic, excessive input growth, or false assertions on intentionally canonicalized values.
- Given benchmark CI runs with `BENCHTIME=1x`, when all benchmarks execute, then they validate correctness and report allocations without enforcing performance thresholds.
- Given the full change, when Tier 1 verification runs, then no production source, protocol, deployment evidence, or runtime dependency has changed.

## Spec Change Log

## Design Notes

Codecov automatically merges multiple reports for one commit, so unit and tagged integration jobs remain parallel. Components are path-defined in `codecov.yml`; their initial floors stay below measured baselines and can be tightened deliberately later. Benchmark output is a baseline artifact only, not release evidence.

## Verification

**Commands:**
- `make test-cover COVERPROFILE=/tmp/scrap-unit.out TEST_RESULTS_DIR=/tmp/scrap-unit-results` -- unit/product profile and JUnit pass.
- `make integration-cover INTEGRATION_COVERPROFILE=/tmp/scrap-integration.out INTEGRATION_TEST_RESULTS_DIR=/tmp/scrap-integration-results` -- tagged LocalStack/OpenBao profile and JUnit pass.
- `make fuzz FUZZTIME=1s` -- all three fuzz targets pass their bounded smoke run.
- `make benchmark BENCHTIME=1x` -- all benchmarks execute and report allocations.
- `make static` -- workflow, formatting, package, protobuf, and lint checks pass.
- `make tier1-check` -- formatting, package boundaries, generated-code consistency, lint, normal tests, race tests, tagged integration tests, and builds pass; the final `govulncheck` step reports pre-existing standard-library advisory `GO-2026-5856` because the baseline toolchain is Go 1.26.4 instead of patched Go 1.26.5.
- YAML parsing for `.github/workflows/ci.yml` and `codecov.yml` -- passes.

## Suggested Review Order

**Coverage measurement and gates**

- Separate executed suites from shipped-package instrumentation at the primary design boundary.
  [`Makefile:45`](../../Makefile#L45)

- Generate independent unit and tagged-integration product profiles with visible failures.
  [`Makefile:310`](../../Makefile#L310)

- Upload parallel product reports and require the new quality job in Tier 1.
  [`ci.yml:65`](../../.github/workflows/ci.yml#L65)

- Enforce fixed project, patch, and risk-component floors without carryforward.
  [`codecov.yml:1`](../../codecov.yml#L1)

**Fuzzing and benchmark CI**

- Bound all parser fuzz commands and keep benchmarks informational.
  [`Makefile:353`](../../Makefile#L353)

- Run required fuzz and benchmark smoke while preserving benchmark failures through `tee`.
  [`ci.yml:177`](../../.github/workflows/ci.yml#L177)

- Exercise CRC-valid Frame error branches and semantic round trips.
  [`frame_fuzz_test.go:11`](../../internal/block/frame_fuzz_test.go#L11)

- Fuzz complete Block index framing before validating V1/V2 entry semantics.
  [`index_fuzz_test.go:13`](../../internal/block/index_fuzz_test.go#L13)

- Bound Pebble Projection value decoding and verify canonical round trips.
  [`value_fuzz_test.go:10`](../../internal/index/value_fuzz_test.go#L10)

**Informational performance baselines**

- Measure verified Document reads and clean 64 MiB Block verification.
  [`reader_benchmark_test.go:13`](../../internal/block/reader_benchmark_test.go#L13)

- Measure worst-case seven-Block Resolution across nontrivial Block indexes.
  [`resolution_benchmark_test.go:21`](../../internal/index/resolution_benchmark_test.go#L21)

**Developer-facing semantics**

- Document product statement coverage, bounded fuzzing, and non-gating benchmarks.
  [`development-guide.md:57`](../../docs/development-guide.md#L57)
