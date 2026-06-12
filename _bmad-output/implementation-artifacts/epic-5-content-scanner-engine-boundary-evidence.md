# Epic 5 Content Scanner Engine Boundary Evidence

Status: review
Story: 5.1 - Content Scanner Engine Boundary and Scheduling
Story frontmatter baseline commit: `b085bac4f9dd76491166033382ee5f7692cb6b8f`
Implementation start commit after Story 5.1 creation checkpoint: `cfe32e3b5f2f06726a6b9866762898fbe30e6bf0`
Started: 2026-06-12T12:07:25-04:00
Last updated: 2026-06-12T12:36:48-04:00

## Scope

This artifact records Story 5.1 evidence for FR-11 scanner boundary and
scheduling:

- prove scanner scheduling is post-ACK and outside write durability;
- prove scanner engine outage and lag are operator-visible without blocking
  writes;
- prove scanner telemetry and evidence are bounded and redacted; and
- prove scanner crash, poison fixture, and duplicate scheduling behavior stay
  bounded, observable, and retry-safe.

Out of scope:

- persisted scanner watermarks and rescan priority, owned by Story 5.2;
- `QuarantineDocument` Raft command and Projection state, owned by Story 5.3;
- quarantined read denial and metadata `scan_status`, owned by Story 5.4;
- admin quarantine list/inspect/confirm/release, owned by Story 5.5;
- `scrapctl` quarantine operator workflow, owned by Story 5.6; and
- Epic 5 aggregate closure, owned by Story 5.7.

## Source References

- `_bmad-output/implementation-artifacts/5-1-content-scanner-engine-boundary-and-scheduling.md`
- `_bmad-output/planning-artifacts/epics.md`
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md`
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md`
- `docs/adr/0008-async-content-scanning-architecture.md`
- `docs/adr/0025-content-quarantine-admin-surface.md`
- `docs/adr/0026-multi-shard-v2-release-boundary.md`
- `docs/research/2026-05-31-external-storage-systems.md`
- `internal/block/listing.go`
- `internal/shard/scrub_coordinator.go`
- `internal/shard/upload_controller.go`
- `internal/admin/shard_diagnostics.go`
- `internal/cmd/shard_diagnostics.go`
- `internal/scrapctl/status.go`
- ClamAV Clamd protocol docs: https://docs.clamav.net/manual/Usage/ClamdProtocol.html
- ClamAV scanning docs: https://docs.clamav.net/manual/Usage/Scanning.html
- YARA C API docs: https://yara.readthedocs.io/en/latest/capi.html

## Acceptance Matrix

| AC | Evidence required | Current proof command or artifact | Decision | Gap |
| --- | --- | --- | --- | --- |
| AC-5.1.1 | Scanner scheduling is post-ACK and outside write durability/visibility; Deep Scrub remains separate. | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/avscan ./internal/shard ./internal/admin ./internal/cmd ./internal/scrapctl -count=1`; `TestShardScannerScansSealedBlocksAfterWriteAck`; `TestSchedulerSkipsWhenNotLeader`; `env GOCACHE=/tmp/scrap-v2-go-build make check`. | `PASS` | None for Story 5.1. |
| AC-5.1.2 | Scanner outage/lag is operator-visible while writes continue. | `TestShardScannerUnavailableDoesNotBlockWritesAndIsObservable`; `TestShardDiagnosticsScannerDegradesSnapshot`; `TestStatusTextOutputIncludesCellMemberShardTerms`; `env GOCACHE=/tmp/scrap-v2-go-build make check`. | `PASS` | None for Story 5.1. |
| AC-5.1.3 | Scanner telemetry/evidence is bounded and redacted. | `TestOTelMetricsInstrumentsAndBoundedAttributes`; `TestServerHealthBoundsSuccessfulShardDiagnostics`; `TestStatusTextOutputBoundsShardDiagnosticFields`; scanner-sensitive scan over exact Story 5.1 touched files; `env GOCACHE=/tmp/scrap-v2-go-build make check`. | `PASS` | Generic credential regex false positives are documented below; no scanner-sensitive matches remain. |
| AC-5.1.4 | Crash, poison fixture, and duplicate scheduling are bounded and retry-safe. | `TestSchedulerPanicRecoveryRecordsBoundedStatus`; `TestSchedulerDuplicateSchedulingIsIdempotent`; `TestSchedulerStopCancelsAndWaitsForWorker`; `TestSchedulerRunsOnInjectedTick`; `env GOCACHE=/tmp/scrap-v2-go-build make check`. | `PASS` | None for Story 5.1. |

## Changed Boundaries

Implemented:

- `internal/avscan/`
- `internal/shard/`
- `internal/admin/shard_diagnostics.go`
- `internal/cmd/shard_diagnostics.go`
- `internal/cmd/shard_set.go`
- `internal/cmd/telemetry.go`
- `internal/scrapctl/status.go`
- `internal/scrapctl/output.go`
- `_bmad-output/implementation-artifacts/5-1-content-scanner-engine-boundary-and-scheduling.md`
- `_bmad-output/implementation-artifacts/epic-5-content-scanner-engine-boundary-evidence.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

Guarded out of scope:

- `proto/`
- `gen/`
- public and peer gRPC wire contracts
- Raft command shape
- Pebble Projection prefixes
- Content Quarantine read path
- quarantine admin operations
- deployment overlays
- scanner engine runtime dependency pinning

## Command Evidence

Pre-implementation:

- `git status --short --branch` - PASS; branch `v2` was clean and synced with
  `origin/v2` before Story 5.1 implementation started.
- `git diff --check` - PASS after story creation.
- `scripts/check-e2e-gates.sh` - PASS after story creation.
- Strict shaped-secret scan over Story 5.1 and sprint status - PASS with no
  matches.
- Scanner-sensitive pattern scan over Story 5.1 - PASS with no matches.

Implementation evidence:

Focused red/green evidence:

- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/avscan -count=1`
  - RED before scheduler implementation: scheduler API undefined.
  - PASS after scheduler types, lifecycle, duplicate skipping, panic recovery,
    pause/budget hooks, and tests.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/admin ./internal/cmd ./internal/scrapctl -run 'ShardDiagnostics|Status.*Shard' -count=1`
  - RED before diagnostics implementation: scanner diagnostic fields and
    rendering missing.
  - PASS after bounded scanner diagnostics and `scrapctl status` rendering.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/avscan -count=1`
  - RED before OTel metrics: `NewOTelMetrics` undefined.
  - PASS after OTel metrics and bounded attribute tests.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd -run TestNewShardTelemetryIncludesEvictionMetrics -count=1`
  - RED before telemetry bundle wiring: scanner metrics missing from Shard
    telemetry bundle.
  - PASS after creating scanner metrics in the telemetry bundle.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestShardScanner' -count=1`
  - RED before Shard ID metric wiring: scanner metrics recorded Shard ID `0`.
  - PASS after threading owning Shard ID into `avscan.Config`.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/avscan -count=1`
  - RED before ticker injection: scheduler ticker injection interfaces missing.
  - PASS after `Ticker` and `TickerFactory` support.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/avscan ./internal/shard ./internal/admin ./internal/cmd ./internal/scrapctl -count=1`
  - PASS after all focused Story 5.1 implementation work.
- `git diff --check`
  - PASS after implementation and artifact updates.
- `scripts/check-e2e-gates.sh`
  - PASS after implementation and artifact updates.
- Scanner-sensitive pattern scan over exact Story 5.1 touched files
  - PASS with no matches after fixture cleanup.
- `env GOCACHE=/tmp/scrap-v2-go-build make check`
  - PASS; covered formatter diff, package boundaries, buf lint/generate,
    generated diff check, golangci-lint, `go test ./...`,
    `go test -race ./...`, tagged integration tests, and `scrapd`/`scrapctl`
    builds.

Telemetry instruments added:

- `scrap.avscan.runs`
- `scrap.avscan.run.duration`
- `scrap.avscan.blocks`
- `scrap.avscan.failures`
- `scrap.avscan.engine_unavailable`
- `scrap.avscan.lag_blocks`
- `scrap.avscan.in_flight_blocks`
- `scrap.avscan.duplicate_schedules`

Allowed scanner metric attributes are bounded to `scrap.shard_id`, `status`,
and `reason`. Attribute tests intentionally pass raw-looking values and verify
they collapse to `unknown`.

## Redaction Evidence

Current state:

- Scanner-sensitive pattern scan over exact Story 5.1 touched files passed with
  no matches after redaction fixture cleanup.
- The broader generic credential regex from the story notes reports expected
  false positives on existing redaction denylist words, `TokenBucket`, and
  authorization metric names. Those are not hardcoded credentials and are not
  scanner evidence leaks.
- Scanner admin/status fields expose bounded enum-like strings and counts only.
  They do not expose Block paths, engine diagnostic bodies, rule material,
  dependency errors, or scanned payload content.
- Scanner OTel attributes are bounded to `scrap.shard_id`, `status`, and
  `reason`; unknown status/reason values collapse to `unknown`.

Review handoff:

- Story 5.1 local verification is complete and the story is in `review`.
- `bmad-code-review` still needs to run before the story can be marked `done`.
