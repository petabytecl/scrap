---
baseline_commit: b085bac4f9dd76491166033382ee5f7692cb6b8f
created: 2026-06-12T12:02:09-04:00
---

# Story 5.1: Content Scanner Engine Boundary and Scheduling

Status: ready-for-dev

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a security operator,
I want sealed Blocks scanned asynchronously after ACK,
so that unsafe content detection does not block billing writes.

## Traceability

- Epic: Epic 5 - Security Operators Can Contain Unsafe Content Without Mutating Documents.
- Requirements: FR-11.
- Governing decision: DG-1 in `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md`.
- Governing ADRs: ADR 0008 async content scanning, ADR 0025 admin surface amendment, ADR 0026 multi-Shard release boundary.
- Current baseline: Story 4.7 is done and pushed at `b085bac4f9dd76491166033382ee5f7692cb6b8f`; Epic 5 is still unimplemented.
- Related future scope: Story 5.2 owns persisted scanner watermarks and rescan priority; Story 5.3 owns `QuarantineDocument` Raft authority; Story 5.4 owns read denial and `scan_status`; Story 5.5 and 5.6 own admin quarantine operations and `scrapctl` quarantine workflows.

## Acceptance Criteria

1. **AC-5.1.1 - Scanner scheduling is post-ACK and outside write durability.** Given a sealed Block becomes eligible for scan, when the Shard leader schedules scanner work, then scanning runs after ACK and never participates in write durability or visibility. Evidence proves scanner work is separate from write ACK and Deep Scrub concerns.
2. **AC-5.1.2 - Scanner outage is operator-visible and does not block writes.** Given the scanner engine is unavailable, when writes continue, then writes are not blocked and scanner outage/lag becomes operator-visible. Evidence records the outage command, operator signal, and write-path result.
3. **AC-5.1.3 - Scanner telemetry and evidence are bounded and redacted.** Given scan telemetry is emitted, when evidence is collected, then labels are bounded and no Document bytes, raw identifiers, signatures, or rule payloads leak. Evidence records telemetry and redaction checks.
4. **AC-5.1.4 - Scanner crash, poison, and duplicate scheduling stay bounded.** Given the scanner crashes, receives a poison Document fixture, or schedules the same Block twice, when scanner work resumes, then writes remain unblocked and scanner state remains bounded, observable, and retry-safe. Evidence records crash, poison, and duplicate-scheduling fixtures.

## Tasks / Subtasks

- [ ] Create the Story 5.1 evidence artifact before code changes. (AC: 1-4)
  - [ ] Create `_bmad-output/implementation-artifacts/epic-5-content-scanner-engine-boundary-evidence.md`.
  - [ ] Record baseline commit, command evidence, changed-boundary list, scanner outage/status evidence, telemetry names/labels, redaction scans, and final `PASS`/`CONCERNS`/`FAIL` rows.
  - [ ] Keep closure scoped to Story 5.1. Do not claim persisted watermarks, detection-to-Raft, read denial, admin quarantine operations, or Epic 5 closure.

- [ ] Add `internal/avscan` as the Content Scanner package. (AC: 1-4)
  - [ ] Add a package comment in `internal/avscan/doc.go` explaining Content Scanner vs. Deep Scrub vs. Content Quarantine.
  - [ ] Define small consumer-side interfaces for engine scans, sealed-Block discovery, I/O budget/pause, metrics, status reporting, and clock/ticker injection.
  - [ ] Define immutable value types for scheduled Block work, scan result, bounded failure reason, and status snapshot.
  - [ ] Include a fake engine for tests; do not add ClamAV, YARA, or native library dependencies in this story.

- [ ] Implement a leader-owned scanner scheduler with bounded lifecycle. (AC: 1, 2, 4)
  - [ ] Poll and/or non-blockingly wake on sealed-Block eligibility using `block.ListSealedBlocks`; exclude the current open Block through the Shard-owned `currentOpenBlockID()` boundary.
  - [ ] Run only while the owning Shard is leader. Follower state must not schedule scans or propose metadata.
  - [ ] Make `Start` and `Stop` context-driven and join all owned goroutines. Do not close channels that Shard apply or notification paths may send to after shutdown.
  - [ ] Protect worker loops with `recover()` so a panic from an engine adapter or poison fixture records a bounded status and does not leak a stuck goroutine or block writes.
  - [ ] Keep duplicate scheduling idempotent. In-memory in-flight/completed tracking is allowed, but persisted watermarks are Story 5.2 and must not be added here.

- [ ] Wire scanner lifecycle through `internal/shard` without entering the write ACK path. (AC: 1, 2, 4)
  - [ ] Add narrow scanner config/dependency fields to `shard.Config` and construct the scanner coordinator near existing scrub/upload lifecycle wiring.
  - [ ] Preserve `WriteDocument` durability ordering. It must not call scanner engine code, wait for scanner status, or fail because the scanner is unavailable.
  - [ ] If the write/seal path wakes the scanner, the wake-up must be non-blocking and best-effort only.
  - [ ] Reuse existing `scrub.TokenBucket` and `scrub.PauseController` concepts for shared I/O budget pressure instead of creating an unrelated throttling model.

- [ ] Expose bounded scanner health and lag to operators. (AC: 2, 3)
  - [ ] Add scanner status fields to Shard diagnostics/admin health using bounded enum-like strings and counts, for example scanner status, lag Blocks, in-flight Blocks, last bounded reason, and last status update time.
  - [ ] Update `internal/scrapctl status` JSON/text rendering only enough to show scanner status and lag from existing admin HTTP health output.
  - [ ] Sanitize new admin fields through the existing Shard diagnostics bounding helpers. No raw Document identity, Block path, signature, rule payload, dependency diagnostic body, Unix socket path, or raw engine error may appear.

- [ ] Add scanner telemetry with low-cardinality labels. (AC: 2, 3, 4)
  - [ ] Use OpenTelemetry instruments, not new native Prometheus metrics.
  - [ ] Candidate metrics: scan run duration, scanned Blocks, engine unavailable count, bounded failure count, scheduler lag Blocks, in-flight Blocks, and duplicate-schedule skips.
  - [ ] Allowed labels: `shard_id`, bounded `status`, bounded `reason`, and bounded scan phase. Do not label by raw Transaction or Document identifiers, raw Block path, rule name, signature name, dependency error, trace identifier, or request identifier.

- [ ] Add focused tests before broad gates. (AC: 1-4)
  - [ ] `internal/avscan` unit tests prove post-ACK scheduler separation with a fake engine, engine unavailable status, bounded retry/backoff, cancellation, duplicate scheduling, panic recovery, and poison fixture behavior.
  - [ ] `internal/shard` tests prove writes continue when the scanner is unavailable, scanner wake-up is non-blocking, and Stop waits for scanner workers.
  - [ ] `internal/admin`, `internal/cmd`, and `internal/scrapctl` tests prove scanner health/lag fields are bounded and redacted.
  - [ ] Do not use sleeps as synchronization. Use fake clocks, manual ticks, channels, contexts, and bounded polling with clear failure messages.

- [ ] Update story, evidence, and sprint artifacts. (AC: 1-4)
  - [ ] Move this story to `in-progress` when implementation starts and to `review` only after local verification is complete.
  - [ ] Update the evidence artifact and this story with debug log references, completion notes, review findings, and file list.
  - [ ] Run `bmad-code-review`; address critical/high findings before marking `done`.

## Dev Notes

### Current State

- There is no `internal/avscan` package, no `QuarantineDocument` Raft command, and no `scan_status` implementation in the current tree.
- `internal/block.ListSealedBlocks(dir, openBlockID)` lists sealed `.blk`/`.idx` pairs oldest-first and excludes the current open Block and `.quarantine` files.
- `internal/shard` already owns background worker lifecycle for scrub and upload. Reuse those patterns: context cancellation, `Stop` joins, buffered never-closed notify channels, and narrow core interfaces.
- `internal/scrub` owns Deep Scrub integrity verification and exposes `TokenBucket`, `PauseController`, and latency-pause concepts. Content Scanner must stay separate but share I/O budget pressure.
- `internal/admin` exposes Shard diagnostics through HTTP `/healthz`, and `internal/scrapctl status` already decodes/renders those fields. This is the right operator-visible surface for Story 5.1 scanner status.
- `scrapd` release images are `CGO_ENABLED=0` static binaries copied into `FROM scratch`. Do not introduce native scanner bindings into the `scrapd` binary without a dependency/runtime ADR.

### Existing Code To Reuse

- `internal/block/listing.go` - sealed Block discovery.
- `internal/block/index.go` and `internal/block/reader.go` - Block index entry and Document read primitives. If scanner needs a new streaming helper, add it here with tests and preserve existing public read semantics.
- `internal/shard/scrub_coordinator.go` - Shard-owned worker composition, leadership boundary, and lifecycle patterns.
- `internal/shard/upload_controller.go` - non-blocking notifications, retry/backoff, bounded status, and cancellation patterns.
- `internal/scrub/deep.go` and `internal/scrub/ratelimit.go` - I/O budget and pause-controller concepts.
- `internal/admin/shard_diagnostics.go`, `internal/cmd/shard_diagnostics.go`, and `internal/scrapctl/status.go` - bounded operator status surfaces.
- `internal/telemetry/identity.go` - hashed/raw identifier policy. Scanner telemetry must fail closed to no raw identifiers.

### Implementation Guardrails

- Content Scanner is not Deep Scrub. It must not call Block Quarantine rename/unquarantine paths or treat integrity corruption as malware.
- Content Scanner is not Content Quarantine authority. In this story, scanner detections may be recorded as bounded scan results/status only. Raft-owned quarantine state starts in Story 5.3.
- Scanner watermarks are not Document visibility authority. Do not add persisted Projection watermark keys in Story 5.1; Story 5.2 owns that.
- Public gRPC behavior must not change in this story. Do not edit `proto/`, `gen/`, public `DocumentService`, peer `PeerService`, or read-path `scan_status` fields.
- Do not add admin quarantine list/inspect/confirm/release endpoints. Story 5.1 may add status/lag fields only.
- Do not send scanned payload content, matched rule material, dependency diagnostics, raw identifiers, or filesystem paths to logs, metrics, audit, traces, evidence, or public tracker output.
- Do not scan via a local path when the engine might be a sidecar with a different filesystem view. The real ClamAV adapter, when added later, should stream bytes over the engine boundary.
- Do not buffer full Blocks or Documents in new production scanner paths. If existing block readers are insufficient, add a narrow streaming helper instead of duplicating index parsing in `internal/avscan`.
- Do not introduce `github.com/hillu/go-yara/v4`, libyara, ClamAV client packages, or CGO/native dependencies in this story. Engine adapters can be interfaces plus fakes until a later dependency ADR or story accepts the runtime shape.

### Latest Tech Information

- ClamAV `clamd` supports scanning through sockets, and remote/TCP clients stream local contents with `INSTREAM` instead of sending local paths for the daemon to open. This supports an engine boundary based on streaming bytes, not path handoff. Source: https://docs.clamav.net/manual/Usage/ClamdProtocol.html
- ClamAV scanning behavior is configured through `clamd.conf`; current official docs describe `clamd` as a multithreaded daemon that needs signature databases, commonly managed by `freshclam`. Source: https://docs.clamav.net/manual/Usage/Scanning.html
- As of 2026-06-12, ClamAV 1.4 is the latest LTS line, and ClamAV 1.5.2 / 1.4.4 include a 2026 security patch for CVE-2026-20031. Do not pin or change container/runtime versions in Story 5.1 unless an implementation story explicitly owns that deployment decision. Sources: https://docs.clamav.net/faq/faq-eol.html and https://github.com/Cisco-Talos/clamav/releases
- YARA's official C API scans files or memory buffers through compiled rules and callbacks. A Go binding would introduce an additional native dependency surface, so Story 5.1 should keep the engine as an interface/fake boundary. Source: https://yara.readthedocs.io/en/latest/capi.html
- The latest upstream YARA release found during story creation is v4.5.4. Do not migrate to YARA-X or change the ADR 0008 engine choice without a new ADR. Source: https://github.com/VirusTotal/yara/releases

### Project Structure Notes

Likely update during implementation:

- `internal/avscan/` - new scanner scheduler, engine boundary, metrics/status types, and package tests.
- `internal/shard/` - scanner lifecycle wiring and Shard status snapshot integration.
- `internal/admin/shard_diagnostics.go` and tests - bounded scanner diagnostics fields.
- `internal/cmd/shard_diagnostics.go` and tests - live scanner status wiring into health.
- `internal/scrapctl/status.go` and tests - status JSON/text rendering.
- `_bmad-output/implementation-artifacts/5-1-content-scanner-engine-boundary-and-scheduling.md`
- `_bmad-output/implementation-artifacts/epic-5-content-scanner-engine-boundary-evidence.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

Avoid unless a failing test proves it is required:

- `proto/`, `gen/`, public/peer/admin wire contracts, Block/Frame layout, Backend object identity, Raft command shape, Pebble Projection prefixes, Content Quarantine read path, quarantine admin operations, deployment overlays, and release evidence matrix.

### Testing Requirements

Run targeted package tests after implementation:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/avscan ./internal/shard ./internal/admin ./internal/cmd ./internal/scrapctl -count=1
```

Run static and structural gates:

```bash
git diff --check
scripts/check-e2e-gates.sh
```

Run the broad local gate before code review:

```bash
env GOCACHE=/tmp/scrap-v2-go-build make check
```

Run leak scans over story, evidence, and touched scanner/admin/status code. Keep patterns in shell variables so commands do not self-match copied secrets:

```bash
cred_pattern='(?i)(api[_-]?[k]ey|[s]ecret|[p]assword|[t]oken|[b]earer|[a]uthorization|aws_access_key_[i]d|aws_[s]ecret_access_[k]ey|private [k]ey|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-)'
scanner_sensitive_pattern='([t]ransaction_id=|[d]ocument_name=|[i]dempotency[_-]?[k]ey=|Backend [k]ey:|trace[_-]?[i]d=|request[_-]?[i]d=|[s]ignature=|[r]ule=|clamd_[e]rror=)'
scan_scope='_bmad-output/implementation-artifacts/5-1-content-scanner-engine-boundary-and-scheduling.md _bmad-output/implementation-artifacts/epic-5-content-scanner-engine-boundary-evidence.md internal/avscan internal/shard internal/admin internal/cmd internal/scrapctl'
rg -n --pcre2 "$cred_pattern" $scan_scope
rg -n --pcre2 "$scanner_sensitive_pattern" $scan_scope
```

### References

- `CONTEXT.md` - Content Scanner glossary, Content Quarantine distinction, write ACK contract, and V2 process rules.
- `_bmad-output/project-context.md` - Go package boundaries, testing rules, no raw identifier telemetry, and static scratch image rules.
- `_bmad-output/planning-artifacts/epics.md` - Epic 5 split and Story 5.1 acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-11 async Content Scanner and FR-12 downstream quarantine scope.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - DG-1 scanner/quarantine architecture and boundary map.
- `docs/adr/0008-async-content-scanning-architecture.md` - async post-ACK scanner, leader-only scanning, engine choice, and future quarantine/watermark behavior.
- `docs/adr/0025-content-quarantine-admin-surface.md` - HTTP admin plus `scrapctl` amendment and redaction rules.
- `docs/adr/0026-multi-shard-v2-release-boundary.md` - scanner/quarantine remain Shard-local authority flows.
- `docs/research/2026-05-31-external-storage-systems.md` - bounded, durable, resumable scanner work design note.
- `docs/go-style-guide.md` - interfaces, concurrency, lifecycle, tests, and metrics conventions.
- ClamAV Clamd protocol docs: https://docs.clamav.net/manual/Usage/ClamdProtocol.html
- ClamAV scanning docs: https://docs.clamav.net/manual/Usage/Scanning.html
- ClamAV EOL policy: https://docs.clamav.net/faq/faq-eol.html
- ClamAV releases: https://github.com/Cisco-Talos/clamav/releases
- YARA C API docs: https://yara.readthedocs.io/en/latest/capi.html
- YARA releases: https://github.com/VirusTotal/yara/releases

## Dev Agent Record

### Agent Model Used

GPT-5 Codex for story creation.

### Debug Log References

- 2026-06-12T12:02:09-04:00 - Story 5.1 created from sprint status after Story 4.7 implementation, review, commit, and push completed.

### Completion Notes List

- Story context created with Epic 5 scope boundaries, current code reuse points, external scanner research, and testing/redaction requirements.

### File List

- `_bmad-output/implementation-artifacts/5-1-content-scanner-engine-boundary-and-scheduling.md`
