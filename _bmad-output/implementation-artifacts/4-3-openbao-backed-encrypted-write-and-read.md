---
baseline_commit: 562c70e4c5c05544324be3be574bab708a486762
created: 2026-06-12T01:46:31-04:00
---

# Story 4.3: OpenBao-Backed Encrypted Write and Read

Status: review

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a security engineer,
I want production writes encrypted before Block persistence and reads decrypted through the normal path,
so that plaintext is never stored as the production default.

## Traceability

- Epic: Epic 4 - Operators Can Run Fail-Closed Security and OpenBao Workflows.
- Requirement: FR-10 - OpenBao envelope encryption and durable rewrap.
- Security slice: #406 - encrypted write/read path. Transit boundary work from #405 is already present in code and must be reused.
- Governing ADR: ADR 0020 - OpenBao envelope encryption contract.
- Current baseline: Story 4.2 is done at `562c70e4c5c05544324be3be574bab708a486762`; security surface authorization, audit, and rate-limit review fixes were committed and pushed before this story was created.
- Related future stories: Story 4.4 owns durable envelope rewrap workflow, Story 4.5 and 4.6 own `scrapctl` OpenBao bootstrap, and Story 4.7 owns production security rehearsal evidence closure with real mTLS/OpenBao gates.

## Acceptance Criteria

1. **AC-4.3.1 - Writes persist ciphertext only.** Given production encryption is configured, when a Document is written, then payload bytes are encrypted before Block persistence and Frame CRC covers stored ciphertext. Evidence proves plaintext is not written to Block storage.
2. **AC-4.3.2 - Reads decrypt and verify before return.** Given an encrypted Document is read, when Transit is available, then the normal read path decrypts and verifies plaintext SHA-256 before return. Evidence identifies the changed boundary and verification command.
3. **AC-4.3.3 - Transit/key failures fail closed and stay redacted.** Given Transit or key material is unavailable, when write/read paths run, then operations fail closed without plaintext fallback. Evidence proves no key material, wrapped-key ciphertext, or plaintext leaks into logs, metrics, traces, or artifacts.
4. **AC-4.3.4 - Production outage rehearsal remains separate.** Given production outage drills are required, when Transit outage or key-denial behavior is exercised beyond the write/read crypto path, then operational rehearsal evidence is owned by Story 4.7 rather than hidden inside this story. Evidence records the split between crypto-path tests and production rehearsal gates.

## Tasks / Subtasks

- [x] Create the Story 4.3 evidence artifact before behavior changes. (AC: 1-4)
  - [x] Create `_bmad-output/implementation-artifacts/epic-4-openbao-encrypted-write-read-evidence.md`.
  - [x] Record baseline commit, timestamp, owner, exact files reviewed, current coverage, gaps, commands, expected results, actual results, redaction proof, and remaining Story 4.7 production-rehearsal scope.
  - [x] Use strict result language per row: `PASS`, `CONCERNS`, or `FAIL`; do not use hybrid phrases.
  - [x] If existing code already satisfies a row, prove it with current tests or source evidence. Do not mark a row pass from intent, architecture, or old story notes alone.

- [x] Audit and reuse the existing Transit/envelope implementation. (AC: 1-4)
  - [x] Read and preserve `internal/encryption/transit.go`, `internal/encryption/envelope.go`, `internal/encryption/fake.go`, `internal/encryption/openbao.go`, and their tests.
  - [x] Reuse `encryption.Transit`, `encryption.OpenBaoTransit`, `encryption.FakeTransit`, `encryption.EncryptDocument`, `encryption.DecryptDocument`, `encryption.MarshalEnvelope`, and `encryption.ParseEnvelope` before adding any new abstraction.
  - [x] Confirm `internal/cmd/tls.go` and `internal/cmd/app.go` pass production-capable Transit into `shard.EncryptionConfig`, while development fake Transit does not silently enable encrypted production behavior.
  - [x] Do not add a new crypto library, assertion/mock framework, Transit package, key cache, OpenBao wrapper, direct Backend decrypt path, or alternate envelope format unless the evidence proves the current boundary cannot satisfy an AC.

- [x] Close encrypted write evidence. (AC: 1, 3)
  - [x] Prove `Shard.WriteDocument` encrypts before `block.Writer.AppendDocumentFrames` writes Frame payloads.
  - [x] Prove `.blk` payload bytes do not contain the plaintext marker and `.idx` stores a parseable envelope for the written Document.
  - [x] Prove Frame CRC validates stored ciphertext, not plaintext; corrupting stored ciphertext must fail as data loss before any plaintext return.
  - [x] Prove write ACK requires encryption and envelope metadata persistence. Transit data-key failure, invalid envelope state, Block append failure, Raft/apply failure, or envelope metadata persistence failure must not ACK a Document or expose plaintext fallback.
  - [x] Prove the plaintext SHA-256 and plaintext size remain the metadata values returned to clients, while stored Frame bytes are ciphertext.

- [x] Close encrypted read evidence. (AC: 2, 3)
  - [x] Prove normal `Shard.ReadDocument` follows Projection Resolution, reads ciphertext Frames through `internal/block`, decrypts through `internal/encryption`, verifies plaintext SHA-256, and returns plaintext bytes only after verification succeeds.
  - [x] Prove plaintext SHA mismatch, envelope metadata mismatch, malformed envelope, invalid Transit request, and ciphertext authentication failure map to data loss where appropriate.
  - [x] Prove an encrypted Document cannot be read when Shard encryption is disabled or Transit is unavailable; the error must be typed as `crypto_unavailable` and must not fall back to ciphertext streaming or plaintext bypass.
  - [x] Keep `internal/server` responsible only for gRPC mapping. Core packages must not import `grpc/status` or `grpc/codes`.

- [x] Close Transit failure and redaction matrix. (AC: 3, 4)
  - [x] Cover write and read failures for Transit unavailable, auth denied, missing key, and minimum-version rejection.
  - [x] Prove `internal/encryption/openbao.go` classifies provider failures without logging or returning Transit tokens, provider response bodies, wrapped-key ciphertext, plaintext data keys, raw Transaction IDs, raw Document names, cert/key material, raw paths, or dependency error strings that embed sensitive values.
  - [x] Use deterministic fake Transit for Tier 1 crypto-path tests and existing OpenBao testcontainer integration for OpenBao adapter parity where the environment supports it.
  - [x] If `make production-rehearsal-security` is skipped, record it as Story 4.7 scope and do not claim production rehearsal readiness from package tests.

- [x] Preserve package, authority, and storage boundaries. (AC: 1-4)
  - [x] Keep Transit and envelope helpers in `internal/encryption`; Shard orchestration in `internal/shard`; Block/Frame CRC and `.idx` envelope persistence shape in `internal/block`; production composition in `internal/cmd`; Backend bytes opaque in `internal/backend`.
  - [x] Do not move decryption into `internal/backend`, `internal/server`, `internal/peer`, `internal/admin`, `internal/scrapctl`, or test/evidence tooling.
  - [x] Do not change storage identity, Shard authority, Raft command semantics, Backend object keys, public/peer/admin protobuf contracts, or Pebble Projection authority for this story.
  - [x] Do not edit generated `gen/` files directly. If a proto/storage/envelope contract change becomes unavoidable, stop and justify the ADR/proto impact before proceeding.

- [x] Update evidence and tracker artifacts. (AC: 1-4)
  - [x] Update this story with debug logs, completion notes, review findings, and file list.
  - [x] Update `_bmad-output/implementation-artifacts/epic-4-openbao-encrypted-write-read-evidence.md` with final matrix rows and command evidence.
  - [x] Update any affected evidence-bundle signal only if Story 4.3 needs a current local crypto-path signal. Do not claim Story 4.7 production rehearsal closure.
  - [x] Move `sprint-status.yaml` to `review` only when implementation and local verification are complete.

- [x] Run verification and leak scans. (AC: 1-4)
  - [x] Run focused unit, package, and integration-adapter tests listed below.
  - [x] Run affected package regression listed below.
  - [x] Run `git diff --check`.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build make check` before code review because this story changes or closes encryption/security evidence.
  - [x] Run credential and identifier leak scans over the new evidence artifact, this story, and touched code. Classify matches as forbidden, allowed fixture/test vocabulary, allowed policy vocabulary, or artifact prose.
  - [x] If a command is skipped, record the skip reason and closure impact in the evidence artifact. Do not mark an AC as pass from intent alone.

## Dev Notes

### Current State

- `CONTEXT.md` defines Document, Transaction, Block, Frame, Shard, Cell, Member, Backend, Pebble Projection, Local Block Lifecycle, and OpenBao Transit vocabulary. Use those terms exactly.
- FR-10 requires production writes to encrypt new Document payload bytes before Block persistence, reads to decrypt through the normal path while preserving integrity checks, and rewrap to avoid rewriting Block payload bytes.
- ADR 0020 is accepted and governs this story. It requires per-Document data keys from OpenBao Transit, versioned envelope metadata, ciphertext Frame payloads, CRC over ciphertext, plaintext SHA-256 verification after decrypt, no production plaintext fallback, and typed crypto-unavailable errors.
- Architecture assigns `internal/encryption` to Transit and envelope primitives, `internal/shard` to encrypted write/read coordination, `internal/block` to Block/Frame layout and envelope persistence shape, `internal/cmd` to production Transit wiring, and `internal/backend` to opaque byte storage only.
- Story 4.2 proved security surface auth/audit/rate limits. Do not reopen public/peer/admin auth scope unless encrypted write/read tests expose a direct regression.

### Existing Code To Reuse

- `internal/encryption/transit.go` defines provider-neutral Transit operations: `GenerateDataKey`, `UnwrapDataKey`, `RewrapDataKey`, and `Readiness`, plus error classes for unavailable, auth denied, missing key, minimum version, invalid config, and invalid request.
- `internal/encryption/envelope.go` implements AES-256-GCM document payload encryption/decryption, per-Frame nonces/AAD, envelope JSON, plaintext SHA-256 verification, ciphertext length validation, and plaintext key zeroing after use.
- `internal/encryption/fake.go` implements deterministic fake Transit with data-key, unwrap, rewrap, outage, auth denied, missing key, minimum-version, and rotation behavior. It is not production-capable.
- `internal/encryption/openbao.go` implements the OpenBao adapter over the official OpenBao API client, using `datakey/plaintext`, `decrypt`, `rewrap`, and key-read readiness endpoints.
- `internal/shard/encryption.go` already branches encrypted writes through `encryption.EncryptDocument`, appends encrypted Frames with plaintext SHA/size metadata, and maps Transit failures to `store.UnavailableReasonCryptoUnavailable`.
- `internal/shard/shard.go` already reads encrypted Documents by resolving committed metadata, reading stored Frames through `block.ReadDocumentFramesFromBlock`, decrypting via `encryption.DecryptDocument`, and returning plaintext only after verification.
- `internal/block/writer.go` computes Frame CRC in `WriteFrame` over the payload it receives; for encrypted writes the payload is ciphertext from `AppendDocumentFrames`.
- `internal/block/index.go` persists `EncryptionEnvelope` in index entries and supports envelope replacement for rewrap.
- `internal/cmd/tls.go` constructs production OpenBao Transit from `SCRAP_TRANSIT_*` configuration and rejects fake Transit in production.
- `internal/cmd/app.go` wires only production-capable Transit, or explicit test-mode fake Transit, into `shard.EncryptionConfig`.

### Likely Gaps To Close

- Current code already has strong crypto-path coverage, but Story 4.3 evidence must reconcile it into one AC matrix with exact commands and current results.
- Write fail-closed evidence must prove no ACK and no plaintext fallback when encryption or envelope metadata persistence fails. Existing Transit outage tests cover part of this; verify Block append and Raft/apply failure coverage before marking AC-4.3.1/4.3.3 `PASS`.
- Read fail-closed evidence must cover disabled Shard encryption on an encrypted Document, unavailable/auth-denied/missing/min-version Transit, invalid envelope, ciphertext authentication failure, and plaintext SHA mismatch.
- Leak scans must include story/evidence artifacts, because evidence prose can accidentally include fake wrapped keys or plaintext markers. Use clearly bounded test markers and classify matches.
- Integration evidence with `test/integration/openbao_transit_test.go` proves the adapter against real OpenBao, not full production rehearsal. Story 4.7 remains responsible for prod-like real mTLS/OpenBao outage drills and final security rehearsal gates.

### Previous Story Intelligence

- Story 4.2 review found evidence overclaim risk. Keep Story 4.3 evidence local/package scoped unless a production rehearsal command actually runs.
- Story 4.2 post-review fixes tightened failure-audit wording to distinguish operation attempted from operation-specific response sent. Apply the same precision here: a package test can prove crypto-path fail-closed behavior; it is not Tier 3 production rehearsal evidence.
- Story 4.1 established production startup gates and fake-Transit rejection. Reuse that wiring instead of adding duplicate production-mode validation.
- Story 3.7 review showed closure artifacts must include artifact status, exact proof commands/test names, scan counts or classifications, strict `PASS`/`CONCERNS`/`FAIL`, and clear baseline scope.
- Commit and push before continuing to the next work was explicitly requested by the user. Keep story creation, implementation, and review-fix commits separated when practical.

### Implementation Guidance

- Start with the evidence artifact and focused tests. The highest-risk failure mode is marking FR-10 complete from existing code without proving every AC on the current head.
- Prefer adding focused tests in existing files before changing production code: `internal/shard/encryption_test.go`, `internal/encryption/*_test.go`, `internal/block/*_test.go`, and `internal/cmd/authorization_test.go` or `internal/cmd/app_test.go`.
- When proving no plaintext is stored, assert concrete `.blk` bytes do not contain the test plaintext marker and that the `.idx` entry has a parseable envelope. Also prove read returns the original plaintext through the normal Shard path.
- When proving CRC over ciphertext, corrupt the stored Frame payload and assert Block/Shard read fails as data loss before plaintext is returned.
- When testing logs/errors, inject unique forbidden strings for plaintext, wrapped-key ciphertext, and token-like values. Assert returned errors and captured log/evidence text do not contain them.
- Use fake Transit for deterministic unavailable/auth/missing/min-version tests. Use OpenBao testcontainer only for adapter parity, and skip/classify integration if Docker/Testcontainers are not available.
- Do not add a key cache, plaintext fallback, transparent migration for old unencrypted Blocks, metadata encryption, tenant-specific key policy, direct Backend ciphertext streaming, or cold-only read behavior in this story.

### Project Structure Notes

Likely update during implementation:

- `_bmad-output/implementation-artifacts/4-3-openbao-backed-encrypted-write-and-read.md` - story status, debug log, completion notes, review findings, and file list.
- `_bmad-output/implementation-artifacts/epic-4-openbao-encrypted-write-read-evidence.md` - AC matrix, commands, redaction checks, and remaining Story 4.7 scope.
- `internal/encryption/*_test.go` - Transit adapter/fake/envelope redaction or failure coverage if gaps remain.
- `internal/shard/encryption_test.go` - encrypted write/read, fail-closed, corrupted ciphertext, disabled encryption, and digest mismatch coverage.
- `internal/block/*_test.go` - Frame CRC over prepared ciphertext payloads if Shard-level tests cannot prove it clearly.
- `internal/cmd/*_test.go` - production/test Transit wiring coverage if gaps remain.
- `_bmad-output/implementation-artifacts/sprint-status.yaml` - status transitions.

Likely avoid:

- `proto/`, `gen/`, `internal/backend`, `internal/server`, `internal/peer`, `internal/admin`, `internal/scrapctl`, deployment manifests, OpenBao bootstrap CLI, durable rewrap implementation, production rehearsal closure docs, and release closure docs.

No ADR is required if the implementation follows ADR 0020. Create or update an ADR only if the implementation changes storage format, wire protocol, dependency choices, security/encryption/auth contracts, envelope metadata contract, or cross-package ownership boundary.

### Testing Requirements

Run focused encryption tests:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/encryption -count=1 -v
```

Run focused encrypted write/read tests:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'Encrypted|Encryption|Crypto|Transit|Envelope|Ciphertext|Plaintext|DataLoss' -count=1 -v
```

Run Block and app wiring tests:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/block ./internal/cmd -run 'Frame|CRC|AppendDocumentFrames|Encrypt|Encryption|Transit|OpenBao|Production|Startup|AppShard' -count=1 -v
```

Run the OpenBao adapter integration test when Testcontainers/Docker are available:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test -tags integration ./test/integration -run TestIntegrationOpenBaoTransitContainerRoundTrip -count=1 -v
```

Run affected package regression:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/encryption ./internal/block ./internal/shard ./internal/store ./internal/cmd ./internal/server -count=1
```

Run leak scans with patterns kept in shell variables so the command does not self-match copied secrets:

```bash
cred_pattern='(?i)(api[_-]?[k]ey|[s]ecret|[p]assword|[t]oken|[b]earer|[a]uthorization|aws_access_key_[i]d|aws_[s]ecret_access_[k]ey|private [k]ey|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-)'
identifier_pattern='([t]ransaction_id|[d]ocument_name|[i]dempotency|Backend [k]ey|Backend object [k]ey|wrapped[- ][k]ey|data [k]ey|Transit [t]oken|plaintext data [k]ey|Frame payload|trace [I]D|request [I]D|gRPC [m]etadata|auth [c]laims|peer [a]ddress|[c]ertificate|/shards/|/tmp/|/home/)'
rg -n --pcre2 "$cred_pattern" _bmad-output/implementation-artifacts/4-3-openbao-backed-encrypted-write-and-read.md _bmad-output/implementation-artifacts/epic-4-openbao-encrypted-write-read-evidence.md internal/encryption internal/block internal/shard internal/cmd
rg -n --pcre2 "$identifier_pattern" _bmad-output/implementation-artifacts/4-3-openbao-backed-encrypted-write-and-read.md _bmad-output/implementation-artifacts/epic-4-openbao-encrypted-write-read-evidence.md internal/encryption internal/block internal/shard internal/cmd
```

Run broad gates before review:

```bash
git diff --check
env GOCACHE=/tmp/scrap-v2-go-build make check
```

If a command is skipped, record the skip reason and closure impact in the evidence artifact. Do not mark an AC as pass from intent alone.

### Latest Technical Information

- OpenBao's current Transit documentation is Version 2.5.x and still documents configurable mount paths, `datakey/plaintext`, `decrypt`, `rewrap`, key versioning, and caller-owned ciphertext storage. This matches `internal/encryption/openbao.go`; no endpoint migration is required for this story.
- The official OpenBao 2.5.x release notes list `v2.5.4` with a May 20, 2026 release date. `test/integration/testinfra/openbao/openbao.go` currently pins `openbao/openbao:2.5.4`, so the fixture aligns with the latest checked 2.5.x release context.
- OpenBao 2.5.0 added an `associated_data` parameter for data-key generation. The current S.C.R.A.P. contract uses OpenBao key derivation `context` for the wrapped data key and local AES-GCM AAD for Document/Frame identity. Do not change that contract without ADR review.
- OpenBao docs warn that convergent encryption makes identical plaintext deterministic. ADR 0020 explicitly rejects Transit convergent encryption for Document payloads; preserve local random nonce-prefix AES-GCM behavior.

### References

- `CONTEXT.md` - domain vocabulary, OpenBao Transit substrate, write state machine, and fail-closed read rules.
- `_bmad-output/planning-artifacts/epics.md` - Epic 4 and Story 4.3 acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-10 and production encryption consequences.
- `_bmad-output/planning-artifacts/architecture.md` - Transit-encrypted Document lifecycle, surface ownership, evidence requirements, package boundaries, and anti-patterns.
- `docs/adr/0020-openbao-envelope-encryption-contract.md` - authoritative envelope encryption contract.
- `docs/phase-4.5-security-implementation-slices.md` - #405 and #406 implementation slices.
- `_bmad-output/implementation-artifacts/4-2-surface-authorization-audit-and-rate-limits.md` - previous story implementation and review intelligence.
- `_bmad-output/implementation-artifacts/epic-4-surface-authorization-audit-rate-limit-evidence.md` - evidence style, command recording, and scan classification pattern.
- `internal/encryption/transit.go`
- `internal/encryption/envelope.go`
- `internal/encryption/fake.go`
- `internal/encryption/openbao.go`
- `internal/shard/encryption.go`
- `internal/shard/shard.go`
- `internal/block/writer.go`
- `internal/block/reader.go`
- `internal/block/index.go`
- `internal/cmd/tls.go`
- `internal/cmd/app.go`
- `test/integration/openbao_transit_test.go`
- `test/integration/testinfra/openbao/openbao.go`
- OpenBao Transit API docs: https://openbao.org/api-docs/secret/transit/
- OpenBao Transit docs: https://openbao.org/docs/secrets/transit/
- OpenBao 2.5.x release notes: https://openbao.org/community/release-notes/2-5-0/

## Dev Agent Record

### Agent Model Used

GPT-5 Codex

### Debug Log References

- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run TestEncryptedShardReadFailsClosedWhenShardEncryptionDisabled -count=1 -v` - PASS.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/encryption -count=1 -v` - PASS.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'Encrypted|Encryption|Crypto|Transit|Envelope|Ciphertext|Plaintext|DataLoss|WriteDocumentAckAfterPeerReplicationRaftApplyAndVisibility|OpenlogWriteAttemptCommitCommand|ApplyCommitDocumentWritesCurrentBlockIndex|AppendDocumentIndexEntryReportsCurrentWriterError' -count=1 -v` - PASS.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/block ./internal/cmd -run 'Frame|CRC|AppendDocumentFrames|Encrypt|Encryption|Transit|OpenBao|Production|Startup|AppShard' -count=1 -v` - PASS.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/encryption ./internal/block ./internal/shard ./internal/store ./internal/cmd ./internal/server -count=1` - PASS.
- `env GOCACHE=/tmp/scrap-v2-go-build go test -tags integration ./test/integration -run TestIntegrationOpenBaoTransitContainerRoundTrip -count=1 -v` - PASS.
- `git diff --check` - PASS.
- `env GOCACHE=/tmp/scrap-v2-go-build make check` - PASS.
- Story 4.3 leak scans - PASS, with final counts recorded in `_bmad-output/implementation-artifacts/epic-4-openbao-encrypted-write-read-evidence.md`.

### Implementation Plan

1. Create the Story 4.3 evidence artifact first and record baseline/current source coverage before changing behavior.
2. Run focused encryption, Shard, Block, and app-wiring tests to separate proven behavior from real gaps.
3. Patch only missing crypto-path tests or narrow production code gaps required by AC-4.3.1 through AC-4.3.4.
4. Update evidence/story status from current verification output, run leak scans plus `make check`, then move the story to review.

### Completion Notes List

- Added a focused Shard test that proves an encrypted Document cannot be read after reopening the same Shard data without Shard encryption; the read returns `crypto_unavailable`, no reader, zero metadata, and the Block still omits plaintext.
- Reused the existing ADR 0020 Transit/envelope path without adding a new crypto abstraction, dependency, envelope format, Backend decrypt path, or production-code behavior change.
- Closed Story 4.3 through current package, integration-adapter, leak-scan, and broad `make check` evidence while leaving production outage rehearsal explicitly in Story 4.7 scope.

### File List

- `_bmad-output/implementation-artifacts/4-3-openbao-backed-encrypted-write-and-read.md`
- `_bmad-output/implementation-artifacts/epic-4-openbao-encrypted-write-read-evidence.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `internal/shard/encryption_test.go`

### Change Log

- 2026-06-12: Added disabled Shard encryption read fail-closed coverage and finalized Story 4.3 encrypted write/read evidence for review.
