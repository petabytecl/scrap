# Epic 3 Encryption-Compatible Restore Evidence

Story: 3.6 - Encryption-Compatible Restore Evidence
Status: review

Baseline commit: d5e36e12ec1e7065db9a0b45fce0d696d89cf7b6
Story creation commit: 32ca0c9479cd92dc685b91a6645bc0d5cd4c9f7b

## Scope

This artifact records Story 3.6 evidence for fixture-backed encrypted restore
behavior. It does not claim final production OpenBao proof, real S3/IAM
production rehearsal, direct Backend ciphertext streaming, or Epic 3 closure.

## Restore And Encryption Path

1. An encrypted Document write uses the Shard encryption configuration and
   Transit boundary to produce ciphertext Frames plus a persisted encryption
   envelope.
2. The sealed Block is uploaded to Backend as opaque Block bytes. Backend is not
   an encryption authority and does not interpret envelope metadata.
3. Confirmed Upload Catalog metadata is committed before restore authority
   exists.
4. Local Block eviction removes the `.blk` while retaining local `.idx` metadata
   and an eviction marker.
5. `ReadDocument` restores the full Backend Block object through the Shard
   restore path, stages it locally, verifies committed Backend metadata and the
   retained `.idx`, then verifies Block/Frame storage integrity.
6. After the restored Block is local, the normal read path reads ciphertext
   Frames, unwraps the envelope through Transit, decrypts through
   `internal/encryption`, and verifies plaintext SHA-256 before returning bytes.

## Acceptance Evidence

| AC | Claim | Evidence | Status | Notes |
| --- | --- | --- | --- | --- |
| AC-3.6.1 | Restore preserves Backend ciphertext and the envelope read path. | `TestEncryptedReadDocumentRestoresThenUsesEnvelopePath` | pass | Backend and restored local Block bytes omit plaintext; restore uses one full-object `GetObject` to committed metadata and zero Backend HEAD/LIST calls. Direct Backend streaming remains out of scope. |
| AC-3.6.2 | Transit/key-material unavailability fails closed after restore. | `TestReadDocumentEncryptedRestoreFailsClosedWhenKeyMaterialUnavailable`; `TestReadDocumentCryptoUnavailableReturnsSanitizedErrorInfoDetail` | pass | Unavailable, auth-denied, and missing-key unwrap failures after restore return no reader, zero metadata, bounded `crypto_unavailable`, ciphertext-only local Block, and sanitized public message. |
| AC-3.6.3 | Rewrapped envelope metadata survives restore. | `TestReadDocumentEncryptedRestoreUsesRewrappedEnvelope` | pass | Rewrap rotates envelope to key version 2 after upload, waits for replacement upload generation, evicts local Block, restores from updated confirmed metadata, preserves Block payload bytes, and verifies plaintext after restore. |
| AC-3.6.4 | Fixture boundary is explicit and production OpenBao proof is not claimed. | This artifact; changed-file scope; `make package-boundaries`; focused fake-Transit restore tests. | pass | Story 3.6 uses deterministic fake Transit and existing OpenBao boundary tests only; real production OpenBao interaction remains Epic 4 release evidence. |
| AC-3.6.5 | Unavailable key service and wrong key version fail closed. | `TestReadDocumentEncryptedRestoreFailsClosedWhenKeyMaterialUnavailable`; `TestReadDocumentEncryptedRestoreFailsClosedWhenKeyVersionRejected`; `TestReadDocumentCryptoUnavailableReturnsSanitizedErrorInfoDetail` | pass | Fake Transit unavailable and minimum-version rejection fail closed after restore without returning plaintext or public crypto internals. |

## Fixture Boundary

- Story 3.6 uses the repo-owned deterministic fake Transit boundary for
  encrypted restore behavior unless implementation evidence proves a narrow test
  adapter is required.
- Existing OpenBao adapter tests remain Transit-boundary evidence only. They do
  not prove production OpenBao deployment, policy, token custody, or live
  production readiness.
- Production OpenBao proof is owned by Epic 4 release evidence and must remain a
  separate concern in this artifact.

## Planned Verification

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestEncryptedReadDocumentRestoresThenUsesEnvelopePath|TestReadDocumentEncryptedRestore.*|TestEncryptedRestore.*|TestReadDocumentRestore.*Key|TestReadDocumentRestore.*Transit|TestReadDocumentRestore.*Rewrap' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestEncryptedShardReadFailsClosedWhenKeyMaterialUnavailable|TestEncryptedShardReadMapsInvalidTransitRequest|TestEncryptedShardRewrapUpdatesEnvelopeWithoutRewritingBlock|TestEnvelopeDecryptVerifiesPlaintextSHA256' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/encryption -run 'TestFakeTransit|TestOpenBaoTransit|TestErrorClass|TestProductionCapable' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server -run 'TestReadDocument.*Crypto|TestReadDocument.*Unavailable|TestReadDocument.*DataLoss' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl/evidencebundle -run 'TestGenerateFailsWhenEncryptedRestoreProofIsMissing|TestGenerate|TestGate' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard ./internal/encryption ./internal/block ./internal/backend ./internal/server ./internal/store ./internal/rewrap ./internal/scrapctl/evidencebundle -count=1
env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard -run 'Test.*Encrypted.*Restore|Test.*Restore.*Key|Test.*Restore.*Transit|Test.*Restore.*Rewrap|Test.*Rewrap' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build make check
```

## Leak Scan Allowlist

Expected scan matches are limited to:

- Story/evidence prose naming forbidden leak classes.
- Test-only fixture strings used to prove plaintext is not stored or returned.
- Source identifiers such as `EncryptionEnvelope`, `WrappedKey`, `TransitKey`,
  and bounded reason constants.
- Environment paths in exact verification commands.

Any deployed public error, log, metric, trace, evidence output, or screenshot
that includes plaintext, wrapped-key ciphertext, data keys, Transit tokens,
Backend object keys, raw Transaction IDs, raw Document names, filesystem paths,
or dependency error detail is a failure.

## Verification Log

- 2026-06-11: Created evidence artifact before production-code changes. AC rows
  remain pending until focused tests and leak scans are run.
- 2026-06-11: PASS - `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run TestEncryptedReadDocumentRestoresThenUsesEnvelopePath -count=1 -v`.
- 2026-06-11: RED - `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server -run TestReadDocumentCryptoUnavailableReturnsSanitizedErrorInfoDetail -count=1 -v` failed because the public status message included leaky crypto details.
- 2026-06-11: PASS - `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server -run 'TestReadDocumentCryptoUnavailableReturnsSanitizedErrorInfoDetail|TestReadDocumentRestoreUnavailableReturnsErrorInfoDetail' -count=1 -v`.
- 2026-06-11: PASS - `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestReadDocumentEncryptedRestoreFailsClosedWhenKeyMaterialUnavailable|TestReadDocumentEncryptedRestoreFailsClosedWhenKeyVersionRejected|TestEncryptedReadDocumentRestoresThenUsesEnvelopePath' -count=1 -v`.
- 2026-06-11: PASS - `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run TestReadDocumentEncryptedRestoreUsesRewrappedEnvelope -count=1 -v`.
- 2026-06-11: PASS - `make package-boundaries`.
- 2026-06-11: PASS - `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestEncryptedReadDocumentRestoresThenUsesEnvelopePath|TestReadDocumentEncryptedRestore.*|TestEncryptedRestore.*|TestReadDocumentRestore.*Key|TestReadDocumentRestore.*Transit|TestReadDocumentRestore.*Rewrap' -count=1 -v`.
- 2026-06-11: PASS - `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestEncryptedShardReadFailsClosedWhenKeyMaterialUnavailable|TestEncryptedShardReadMapsInvalidTransitRequest|TestEncryptedShardRewrapUpdatesEnvelopeWithoutRewritingBlock|TestEnvelopeDecryptVerifiesPlaintextSHA256' -count=1 -v`.
- 2026-06-11: PASS - `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/encryption -run 'TestFakeTransit|TestOpenBaoTransit|TestErrorClass|TestProductionCapable' -count=1 -v`.
- 2026-06-11: PASS - `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server -run 'TestReadDocument.*Crypto|TestReadDocument.*Unavailable|TestReadDocument.*DataLoss' -count=1 -v`.
- 2026-06-11: PASS - `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl/evidencebundle -run 'TestGenerateFailsWhenEncryptedRestoreProofIsMissing|TestGenerate|TestGate' -count=1 -v`.
- 2026-06-11: PASS - `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard ./internal/encryption ./internal/block ./internal/backend ./internal/server ./internal/store ./internal/rewrap ./internal/scrapctl/evidencebundle -count=1`.
- 2026-06-11: PASS - `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard -run 'Test.*Encrypted.*Restore|Test.*Restore.*Key|Test.*Restore.*Transit|Test.*Restore.*Rewrap|Test.*Rewrap' -count=1 -v`.
- 2026-06-11: PASS - Touched-file credential scan found only allowlisted BMAD prose, test-fixture, and source-identifier matches; no real secrets were found.
- 2026-06-11: PASS - Touched-file identifier scan found only allowlisted BMAD prose, command paths, test-fixture strings, and existing source identifiers; no deployed raw identifier leaks were found.
- 2026-06-11: PASS - `env GOCACHE=/tmp/scrap-v2-go-build make check`.

## Remaining Scope

- Story 3.6 is fixture-backed encryption-compatible restore evidence. It does
  not close real production OpenBao interaction, token custody, policy setup, or
  production rehearsal; those remain Epic 4 release evidence.
