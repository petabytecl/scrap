---
story: 4.3-openbao-backed-encrypted-write-and-read
status: done
created: 2026-06-12T01:49:35-04:00
story_context_baseline: 562c70e4c5c05544324be3be574bab708a486762
implementation_start_commit: 2ff7f8b3244213a2b6632fa2e668fff763ecc5f9
owner: Coto
---

# Epic 4 OpenBao Encrypted Write/Read Evidence

## Scope

Story 4.3 closes the local encrypted write/read crypto path for FR-10. It proves:

- new encrypted writes persist ciphertext Frame payloads and envelope metadata;
- normal reads decrypt through the Shard path and verify plaintext SHA-256 before return;
- Transit/key failures fail closed without plaintext fallback; and
- local crypto-path evidence is distinct from Story 4.7 production outage rehearsal.

Story 4.3 does not claim production security rehearsal closure, OpenBao bootstrap UX, durable rewrap closure, metadata encryption, transparent migration for old unencrypted Blocks, or direct Backend ciphertext streaming.

## Files Reviewed

- `CONTEXT.md`
- `_bmad-output/project-context.md`
- `_bmad-output/planning-artifacts/epics.md`
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md`
- `_bmad-output/planning-artifacts/architecture.md`
- `docs/adr/0020-openbao-envelope-encryption-contract.md`
- `docs/phase-4.5-security-implementation-slices.md`
- `_bmad-output/implementation-artifacts/4-2-surface-authorization-audit-and-rate-limits.md`
- `_bmad-output/implementation-artifacts/4-3-openbao-backed-encrypted-write-and-read.md`
- `_bmad-output/implementation-artifacts/epic-4-openbao-encrypted-write-read-evidence.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `internal/encryption/transit.go`
- `internal/encryption/envelope.go`
- `internal/encryption/fake.go`
- `internal/encryption/openbao.go`
- `internal/encryption/transit_test.go`
- `internal/encryption/fake_test.go`
- `internal/encryption/openbao_test.go`
- `internal/shard/encryption.go`
- `internal/shard/shard.go`
- `internal/shard/encryption_test.go`
- `internal/shard/restore_test.go`
- `internal/shard/openlog_write_attempt_test.go`
- `internal/shard/projection_test.go`
- `internal/shard/write_ack_test.go`
- `internal/block/writer.go`
- `internal/block/reader.go`
- `internal/block/index.go`
- `internal/block/index_test.go`
- `internal/block/verify_test.go`
- `internal/cmd/tls.go`
- `internal/cmd/app.go`
- `internal/cmd/authorization_test.go`
- `test/integration/openbao_transit_test.go`
- `test/integration/testinfra/openbao/openbao.go`

## Final Coverage Matrix

| AC | Status | Current proof | Remaining evidence needed |
| --- | --- | --- | --- |
| AC-4.3.1 writes persist ciphertext only | PASS | `TestEncryptedShardWriteReadPersistsCiphertextAndEnvelope` proves encrypted Shard writes return plaintext metadata while `.blk` omits the plaintext marker and `.idx` contains a parseable envelope. `TestVerifyBlock_EncryptedEntryVerifiesStoredCiphertext` proves Block verification checks stored ciphertext bytes. `TestWriteDocumentAckAfterPeerReplicationRaftApplyAndVisibility`, `TestOpenlogWriteAttemptCommitCommand`, `TestApplyCommitDocumentWritesCurrentBlockIndex`, and `TestAppendDocumentIndexEntryReportsCurrentWriterError` prove write ACK waits for peer durability, Raft apply, and index persistence/error propagation. | None for Story 4.3. |
| AC-4.3.2 reads decrypt and verify before return | PASS | `readDocumentBytes` resolves committed metadata, reads ciphertext Frames through `internal/block`, decrypts through `internal/encryption`, and returns plaintext only after `DecryptDocument` validates plaintext SHA-256 and length. `TestEnvelopeDecryptVerifiesPlaintextSHA256`, `TestEncryptedShardReadReportsDataLossOnCiphertextCorruption`, and `TestEncryptedShardReadMapsInvalidTransitRequestToDataLoss` cover digest, ciphertext authentication, and invalid Transit request failure paths. | None for Story 4.3. |
| AC-4.3.3 Transit/key failures fail closed and stay redacted | PASS | `TestEncryptedShardWriteFailsClosedWhenTransitUnavailable`, `TestEncryptedShardReadFailsClosedWhenKeyMaterialUnavailable`, `TestEncryptedShardReadFailsClosedWhenShardEncryptionDisabled`, restore encryption failure tests, fake Transit tests, OpenBao adapter redaction tests, and production fake-Transit rejection tests prove crypto failures stay typed and do not expose plaintext fallback. Final leak scans found no forbidden shaped values; matches were allowed policy, fixture, and artifact vocabulary. | None for Story 4.3. |
| AC-4.3.4 production outage rehearsal remains separate | PASS | Story 4.3 ran local crypto-path and OpenBao adapter evidence only. `make production-rehearsal-security` was intentionally not run because production outage drills are outside this story. | None for Story 4.3; Story 4.7 owns production rehearsal closure. |

## Source Evidence Notes

- `internal/encryption.EncryptDocument` uses a per-Document data key from Transit, local AES-256-GCM payload encryption, per-Frame AAD, plaintext SHA-256 metadata, and versioned envelope JSON.
- `internal/encryption.DecryptDocument` parses envelope metadata, unwraps the data key, decrypts every Frame, validates ciphertext length, and verifies plaintext SHA-256 and length before returning plaintext bytes.
- `internal/shard.appendDocumentPayload` writes ciphertext Frames through `block.Writer.AppendDocumentFrames` when `shard.EncryptionConfig` is enabled.
- `internal/shard.readDocumentBytes` fails closed with `crypto_unavailable` when an encrypted index entry is read without enabled Shard encryption.
- `internal/cmd.newAppTransit` rejects fake Transit in production, and `appShardEncryptionConfig` does not enable development fake Transit unless explicit test mode fake Transit is selected.
- `internal/encryption.openbao` classifies OpenBao provider failures into provider-neutral errors and does not include provider body text or token values in returned errors.

## Current Test Evidence Scope

The implementation diff adds one missing test: `TestEncryptedShardReadFailsClosedWhenShardEncryptionDisabled`.
All other PASS rows are based on current reruns of existing tests, not unstated intent:

- Auth denied, missing key, outage, and minimum-version behavior: `TestFakeTransitFailsClosedWithTypedErrors`, `TestFakeTransitMinimumVersionFailure`, `TestEncryptedShardReadFailsClosedWhenKeyMaterialUnavailable`, `TestReadDocumentEncryptedRestoreFailsClosedWhenKeyMaterialUnavailable`, and `TestReadDocumentEncryptedRestoreFailsClosedWhenKeyVersionRejected`.
- Provider error redaction and token handling: `TestOpenBaoTransitClassifiesAndRedactsProviderFailures` and `TestOpenBaoConfigValidationRedactsToken`.
- Envelope metadata persistence and ACK ordering: `TestOpenlogWriteAttemptCommitCommand`, `TestApplyCommitDocumentWritesCurrentBlockIndex`, `TestAppendDocumentIndexEntryReportsCurrentWriterError`, and `TestWriteDocumentAckAfterPeerReplicationRaftApplyAndVisibility`.
- Envelope storage and stored-ciphertext CRC behavior: `TestEncryptedShardWriteReadPersistsCiphertextAndEnvelope`, `TestVerifyBlock_EncryptedEntryVerifiesStoredCiphertext`, and `TestVerifyBlock_FrameCRCCorruption`.

## Command Evidence

- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run TestEncryptedShardReadFailsClosedWhenShardEncryptionDisabled -count=1 -v` - PASS. Added coverage for encrypted Document read with Shard encryption disabled.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/encryption -count=1 -v` - PASS. Covered fake Transit, OpenBao adapter mapping, readiness, provider failure classification, context failures, error sentinels, size validation, and token redaction tests.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'Encrypted|Encryption|Crypto|Transit|Envelope|Ciphertext|Plaintext|DataLoss|WriteDocumentAckAfterPeerReplicationRaftApplyAndVisibility|OpenlogWriteAttemptCommitCommand|ApplyCommitDocumentWritesCurrentBlockIndex|AppendDocumentIndexEntryReportsCurrentWriterError' -count=1 -v` - PASS. Covered encrypted write/read, fail-closed read/write, data-loss mapping, restore encryption paths, envelope digest verification, replication ciphertext authentication, write ACK ordering, Raft command envelope metadata, and index persistence errors.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/block ./internal/cmd -run 'Frame|CRC|AppendDocumentFrames|Encrypt|Encryption|Transit|OpenBao|Production|Startup|AppShard' -count=1 -v` - PASS. Covered Frame CRC, encrypted Block verification, production security gates, fake Transit rejection, and app Shard encryption wiring.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/encryption ./internal/block ./internal/shard ./internal/store ./internal/cmd ./internal/server -count=1` - PASS. Covered affected package regression.
- `docker info --format '{{.ServerVersion}}'` - PASS, Docker server `29.5.2`.
- `env GOCACHE=/tmp/scrap-v2-go-build go test -tags integration ./test/integration -run TestIntegrationOpenBaoTransitContainerRoundTrip -count=1 -v` - PASS. Testcontainers started `openbao/openbao:2.5.4` and completed the Transit round trip.
- `git diff --check` - PASS.
- `env GOCACHE=/tmp/scrap-v2-go-build make check` - PASS. The gate ran formatter diff, package-boundary checks, `buf lint`, `buf generate`, generated diff check, `golangci-lint run`, `go test ./...`, `go test -race ./...`, integration tests with Testcontainers, and `go build` for `scrapd` and `scrapctl`.
- Post-review `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run TestEncryptedShardReadFailsClosedWhenShardEncryptionDisabled -count=1 -v` - PASS.
- Post-review `env GOCACHE=/tmp/scrap-v2-go-build make check` - PASS after Shard test cleanup and evidence fixes.
- `make production-rehearsal-security` - SKIPPED. Story 4.7 owns production outage rehearsal and real mTLS/OpenBao evidence; this skip does not block Story 4.3 local crypto-path closure.

## Leak Scan Evidence

Final leak scans are recorded after the artifact update so the committed story and evidence text are included.

Commands:

```bash
cred_pattern='(?i)(api[_-]?[k]ey|[s]ecret|[p]assword|[t]oken|[b]earer|[a]uthorization|aws_access_key_[i]d|aws_[s]ecret_access_[k]ey|private [k]ey|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-)'
identifier_pattern='([t]ransaction_id|[d]ocument_name|[i]dempotency|Backend [k]ey|Backend object [k]ey|wrapped[- ][k]ey|data [k]ey|Transit [t]oken|plaintext data [k]ey|Frame payload|trace [I]D|request [I]D|gRPC [m]etadata|auth [c]laims|peer [a]ddress|[c]ertificate|/shards/|/tmp/|/home/)'
strict_value_pattern='(AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-|BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY|aws_access_[k]ey_id|aws_[s]ecret_access_[k]ey)'
scan_scope='_bmad-output/implementation-artifacts/4-3-openbao-backed-encrypted-write-and-read.md _bmad-output/implementation-artifacts/epic-4-openbao-encrypted-write-read-evidence.md internal/encryption internal/block internal/shard internal/cmd'
rg -n --pcre2 "$cred_pattern" $scan_scope
rg -n --pcre2 "$identifier_pattern" $scan_scope
rg -n --pcre2 "$strict_value_pattern" $scan_scope
```

| Scan | Status | Forbidden matches | Classification |
| --- | --- | --- | --- |
| Credential pattern scan | PASS | 0 strict shaped-value matches; 156 broad vocabulary matches | Broad matches classified as policy/artifact prose, existing config names, OpenBao test fixture tokens, redaction test payloads, validation-token fields, or security vocabulary. |
| Identifier pattern scan | PASS | 0 strict shaped-value matches; 112 broad vocabulary matches | Broad matches classified as policy/artifact prose, validation code, test fixture Document/Transaction names, Backend key fixtures, fake/OpenBao wrapped-key tests, certificate flag names, or test-only temp paths. |

## Production Rehearsal Split

`make production-rehearsal-security` was not run for Story 4.3. This is intentional. Story 4.3 closes package-level encrypted write/read behavior plus adapter parity against an OpenBao container. Story 4.7 owns production outage drill closure with real mTLS/OpenBao gates and final rehearsal artifacts.

## Final Decision

PASS - Story 4.3 satisfies AC-4.3.1 through AC-4.3.4 with current local crypto-path tests, OpenBao adapter integration evidence, broad `make check`, and final leak scans. Production outage rehearsal remains explicitly scoped to Story 4.7.
