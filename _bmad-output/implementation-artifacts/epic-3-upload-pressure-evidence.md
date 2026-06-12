# Epic 3 Upload Pressure Evidence

Status: done

Story: 3.2 - Upload Pressure and Safe Admission Evidence

Baseline: `38e27ab docs: create story 3.2 upload pressure`

## Scope

This artifact covers the Story 3.2 upload-pressure evidence gate. It proves
that pending Backend upload obligations create bounded, observable admission
pressure before local durability runway becomes unsafe, and that pressure
rejection leaves no accepted partial Document state.

It does not close local Block eviction, restore-first cold reads, production
security, real S3/IAM rehearsal, or final V2 release readiness.

## Authority Path

1. A write that rotates a full Block enters `sealAndOpenNew`.
2. The Shard records a local upload obligation for the sealed Block.
3. Committed `SealBlock` materializes a pending Upload Outbox row.
4. `refreshUploadPressureLocked` scans pending uploads and local retry
   obligations through `blockUploadLifecycle.refreshPressure`.
5. `uploadObligations.pressureStats` aggregates pending bytes and Blocks,
   de-duplicating any Block already present in committed pending uploads.
6. `uploadController.SetPressure` normalizes thresholds, computes pressure
   level, adjusts upload concurrency, emits metrics, and updates the scrub pause
   coordinator.
7. `uploadController.rejectWrite` maps pressure/critical state to
   `storeapi.ResourceExhaustedReasonUploadPressure`.
8. `internal/server` maps the store error to gRPC `RESOURCE_EXHAUSTED` with an
   `ErrorInfo` detail.
9. Committed `ConfirmUpload` clears pending upload state, refreshes pressure,
   and permits later writes without manual metadata edits.

Backend PUT, HEAD, list, key shape, and Local Block Lifecycle markers are
evidence inputs only. They are not admission authority, Document visibility,
public routing, Shard membership, or durable upload authority.

## Evidence Checklist

| AC | Required proof | Current coverage | Gap before closure |
| --- | --- | --- | --- |
| AC-3.2.1 | Pressure state is computed from committed pending uploads and local obligations before unsafe local runway. | `internal/shard/upload_pressure_test.go`; `internal/shard/upload_outbox_boundary_test.go`; `internal/shard/upload_obligations_test.go`. | Add/confirm a focused test that committed pending uploads and duplicate local obligations count once and drive pressure snapshot fields. |
| AC-3.2.2 | Writes resume after committed upload confirmation drains pressure, without operator metadata edits. | `TestUploadPressureRejectsWritesAndResumesAfterDrain`. | Ensure the test proves recovery follows confirm/committed state and records the evidence command. |
| AC-3.2.3 | Telemetry, admin health, and public errors are bounded and redacted. | `TestUploadOTelMetricsRegistersAndEmits`; `internal/server/upload_pressure_test.go`; `internal/admin/server_test.go`; `internal/cmd/shard_diagnostics_test.go`. | Record focused commands and leak-scan results against changed paths. |
| AC-3.2.4 | A rejected pressure write leaves no accepted Block, Frame, Openlog prep, or Projection visibility for the rejected Document. | Partial: `TestSealTriggeredUploadPressureRejectsCurrentWrite` proves rejection only. | Add deterministic cleanup/recovery assertions around the rejected write. |

## Planned Verification

Command A:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestUploadPressure|TestSealTriggeredUploadPressure|TestUploadOutbox|TestWriteDocument_NoPrepFile' -count=1 -v
```

Command B:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server ./internal/admin -run 'UploadPressure|ResourceExhausted' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd -run 'ShardDiagnosticsPressure|UploadPressure|ResourceExhausted' -count=1 -v
```

Command C:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard -run 'TestUploadPressure|TestSealTriggeredUploadPressure' -count=10 -v
```

Command D:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/shard ./internal/server ./internal/admin ./internal/cmd -count=1
```

Command E:

```bash
env GOCACHE=/tmp/scrap-v2-go-build make check
```

Command F:

```bash
env GOCACHE=/tmp/scrap-v2-go-build SCRAP_E2E=1 go test ./test/e2e -run TestE2EBackendUploadAdmissionPressure -count=1 -v
```

Command F is a Tier 2 runtime evidence target. If it is not run with
`SCRAP_E2E=1`, this artifact must record CONCERNS rather than PASS for deployed
upload-pressure evidence.

Command G:

```bash
cred_pattern='(?i)(api[_-]?[k]ey|[s]ecret|[p]assword|[t]oken|[b]earer|[a]uthorization|aws_access_key_[i]d|aws_[s]ecret_access_[k]ey|private [k]ey|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-)'
rg -n --pcre2 "$cred_pattern" _bmad-output/implementation-artifacts/3-2-upload-pressure-and-safe-admission-evidence.md _bmad-output/implementation-artifacts/epic-3-upload-pressure-evidence.md _bmad-output/implementation-artifacts/sprint-status.yaml internal/shard/upload_pressure_test.go internal/shard/upload_outbox_boundary_test.go internal/shard/upload_metrics_otel_test.go
```

Command H:

```bash
identifier_pattern='([t]ransaction_id|[d]ocument_name|[i]dempotency|Backend [k]ey|Backend object [k]ey|validation [t]oken|trace [I]D|request [I]D|gRPC [m]etadata|auth [c]laims|peer [a]ddress|[c]ertificate|/shards/|/tmp/|/home/)'
rg -n --pcre2 "$identifier_pattern" _bmad-output/implementation-artifacts/3-2-upload-pressure-and-safe-admission-evidence.md _bmad-output/implementation-artifacts/epic-3-upload-pressure-evidence.md _bmad-output/implementation-artifacts/sprint-status.yaml internal/shard/upload_pressure_test.go internal/shard/upload_outbox_boundary_test.go internal/shard/upload_metrics_otel_test.go
```

## Evidence Rows

| AC | Claim | Command | Artifact or Test Path | Ref | Result | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| AC-3.2.1 | Pending upload obligations drive visible pressure. | Command A, Command C, Command D, Command E | `internal/shard/upload_outbox_boundary_test.go`; `internal/shard/upload_obligations_test.go`; `internal/shard/upload_pressure_test.go` | Story 3.2 working tree | PASS | Added `TestUploadOutboxRefreshPressureCombinesCommittedAndLocalObligations`; committed pending and unique local obligations produced `PendingBytes=120`, `PendingBlocks=2`, and critical pressure from normalized thresholds. |
| AC-3.2.2 | Pressure clears after committed upload confirmation and writes resume without metadata edits. | Command A, Command C, Command D, Command E | `internal/shard/upload_pressure_test.go` | Story 3.2 working tree | PASS | `TestUploadPressureRejectsWritesAndResumesAfterDrain` now requires pending upload state to drain to zero after `ConfirmUploadForTest` before a later write is accepted. |
| AC-3.2.3 | Public errors, telemetry, and health fields are bounded. | Command A, Command B, Command G, Command H | `internal/shard/upload_metrics_otel_test.go`; `internal/server/upload_pressure_test.go`; `internal/admin/server_test.go`; `internal/cmd/shard_diagnostics_test.go` | Story 3.2 working tree | PASS | Added `TestUploadOTelMetricsUsesBoundedAttributes`; public rejection returned `RESOURCE_EXHAUSTED` with `ErrorInfo.reason=upload_pressure`; admin and cmd diagnostics expose bounded pressure fields. Scans found only expected tracker/story text and command documentation. |
| AC-3.2.4 | Rejected pressure write leaves no accepted partial Document state. | Command A, Command C, Command D, Command E | `internal/shard/upload_pressure_test.go` | Story 3.2 working tree | PASS | `TestSealTriggeredUploadPressureRejectsCurrentWrite` now asserts rejected Document invisibility through head/read/find, no Openlog prep file, pressure drain, and later write reuse. Empty active Block state is allowed because it remains the next writable Block, not accepted Document state. |
| AC-3.2.1 - AC-3.2.4 | Deployed prod-like upload-pressure evidence target was checked but not claimed. | Command F without `SCRAP_E2E=1` | `test/e2e/upload_e2e_test.go` | Story 3.2 working tree | CONCERNS | Explicit run skipped with `set SCRAP_E2E=1 to run E2E tests`. Tier 2 evidence remains the next runtime-evidence action. |

## Pending Evidence

- No pending unit, package, race, or broad local evidence for Story 3.2.
- Tier 2 E2E upload-pressure evidence is not claimed because `SCRAP_E2E=1` was not set for the documented target.
- Real S3/IAM production rehearsal remains deferred to Story 6.6.
