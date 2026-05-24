# Production Crash Recovery And Corruption Evidence Matrix

Status: production-readiness evidence contract for GitHub issue `#44`
Last updated: 2026-05-24

This matrix defines the release-blocking invariants that must be proven before
S.C.R.A.P. can accept production traffic under the write ACK contract from ADR
0012. Normal PR tests are useful current evidence, but production write ACK
readiness requires named release evidence, dedicated-runner evidence, or an
approved manual artifact for the target deployment profile.

The matrix covers local bytes, metadata WAL and snapshots, local projections,
backend upload, restore and prewarm, OpenBao envelope failures, corruption,
replay, scrub, repair, and disaster-recovery drill paths.

## Production Write ACK Gate Status

Current status: blocked.

`SCRAP_PRODUCTION_WRITE_ACK_READINESS` remains fail-closed in
`internal/config`. Individual evidence booleans are not enough: the gate also
requires the target release profile, linked release artifacts for every
readiness gate, `SCRAP_PRODUCTION_WRITE_ACK_IMPLEMENTATION`,
`SCRAP_RELEASE_OWNER_SIGNOFF`, and `SCRAP_DOWNSTREAM_DEPLOYMENT_APPROVAL`.
Local release rehearsal evidence remains repo-owned evidence only; it does not
stand in for live production capacity, retention, provider-account, OpenBao HA,
or GitOps application approval.

| Gate | Release-blocking claim | Current evidence | Remaining blockers | Status |
| --- | --- | --- | --- | --- |
| `SCRAP_METADATA_COMPATIBILITY_BOUNDARY_V1` | Internal and published metadata, block, index, and envelope records remain replayable and compatible across stored data and rolling upgrades. | `make check`, `make test-compat`, generated-code cleanliness, `internal/compat`, `internal/metastore`, `internal/published`, `internal/storageformat`. | #45, #85 | Blocked until release evidence is aggregated. |
| `SCRAP_RAFT_METADATA_DURABILITY` | Quorum metadata survives restart, snapshot, compaction, stale leader, and ReadIndex failure modes. | `internal/raftmeta` tests and spike Raft barrier evidence. | #85 | Blocked until dedicated crash/fault campaign evidence exists. |
| `SCRAP_FORENSIC_COMMIT_INTEGRITY` | Production metadata commits have independent forensic integrity evidence; single-voter Raft is not claimed as tamper-evident against privileged filesystem access. | ADR 0016 documents the current limitation and compensating controls. | External append-only witness evidence or multi-voter production topology evidence | Blocked until target-profile forensic evidence is accepted. |
| `SCRAP_PEER_BYTE_DURABILITY` | ACK waits for required local and peer byte durability before metadata visibility, and unsafe placement fails closed. | `internal/replication`, local repair tests, peer repair tests, and spike peer-prepare tests. | #85 | Blocked until production peer durability scenarios are in release evidence. |
| `SCRAP_BACKEND_RESTORE_WORKFLOW` | Backend upload, restore, prewarm, repair, and DR rebuild are idempotent, verified, resumable, and observable. | `internal/backendupload`, `internal/backend/fs`, `internal/localstorage` restore, repair, DR drill, and metadata restore tests, plus `make local-dr-drill-evidence`. | #85, #88 | Blocked until campaign and local DR drill evidence are accepted. |
| `SCRAP_OPENBAO_ENVELOPE_WORKFLOW` | Encrypted backend data can be restored only with expected envelope and OpenBao key material; outages fail as crypto-unavailable without secret leakage. | Fake Transit and envelope workflow tests plus `docs/openbao-transit-smoke-coverage.md`. | #86 | Blocked until real OpenBao smoke evidence is approved. |
| `SCRAP_CAPACITY_ADMISSION` | Write admission, disk runway, backend budgets, restore/repair lanes, and OpenBao capacity fail closed for unsafe production profiles. | `internal/backend` capacity profile tests, config gate tests, `make capacity-sample`, and `make local-soak-evidence`. | #48, #87 | Blocked until target-profile inputs and local soak/capacity evidence are accepted. |
| `SCRAP_OPERATOR_READINESS` | Operators can see, audit, stop, retry, and recover dangerous actions using admin API and `scrapctl` workflows. | Admin operation, audit, authorization, durable operation tests, `docs/storage-gateway-dashboard-alert-contract.md`, `docs/storage-gateway-operator-runbooks.md`, and `make local-dr-drill-evidence`. | #88 | Blocked until DR drill evidence is accepted. |
| `SCRAP_PRODUCTION_WRITE_ACK_IMPLEMENTATION` | Production write ACK mode can be enabled only after all evidence and signoffs are accepted for the target profile. | Structured config gate, release-gate reporting, and config tests require linked implementation evidence. | Target-profile release artifact | Blocked until the implementation artifact is included in the release evidence bundle. |
| `SCRAP_RELEASE_OWNER_SIGNOFF` | The release owner approves the target-profile evidence bundle or records an exception with owner and expiry. | `docs/production-capacity-compliance-signoff.md` and GitHub issue evidence comments. | #48, #86, #88 | Blocked until owner signoff is accepted for the current evidence bundle. |
| `SCRAP_DOWNSTREAM_DEPLOYMENT_APPROVAL` | Live production deployment-specific capacity, retention, provider account, OpenBao HA, and GitOps application approval is supplied by the downstream deployment owner. | Not repo-owned; the gate reports this as a separate missing artifact when absent. | Downstream deployment owner | Blocked until downstream deployment evidence is supplied or an explicit exception is recorded. |

## Evidence Artifact Rules

Every automated, dedicated-runner, or manual release artifact referenced by
this matrix must record:

- commit SHA and dirty-tree marker;
- command, workflow, or runbook step used to produce the evidence;
- runner class, operating system, filesystem profile, and relevant environment;
- deployment profile or explicit non-production profile;
- random seed, scenario ID, workload shape, document sizes, and failure
  schedule when applicable;
- duration, result, and threshold decision;
- sanitized logs, metrics, traces, operation IDs, and audit event IDs needed for
  diagnosis;
- owner and approval or exception record when the evidence is manual.

Artifacts must not include document bytes, plaintext secrets, plaintext data
encryption keys, wrapped DEKs, OpenBao tokens, backend credentials, or raw
customer payloads.

## Dedicated Campaign Command

`make crash-fault-evidence` runs `scrap-crash-fault-evidence`, which executes a
repo-owned scenario catalog outside normal PR `make check`. The command emits a
JSON `crash-fault-evidence-report` with commit SHA, dirty-tree marker, runner
profile, filesystem profile, seed, config, duration, scenario IDs, failure
schedules, sanitized command logs, and release-gate evidence entries for:

- `crash-fault`;
- `peer-byte-durability`;
- `backend-restore`.

The catalog covers acknowledged-write and visible-read crash boundaries, local
prepare/openlog replay, Raft metadata restart/snapshot/stale-leader/ReadIndex
faults, peer byte preparation and repair, backend upload/restore/prewarm/DR
drill paths, durable operation recovery, and fake OpenBao Transit outage and
missing-key paths. Target-profile release aggregation remains responsible for
retaining the report artifact and deciding whether the runner profile is
authoritative for production readiness.

## Release-Blocking Invariants

| ID | Path | Invariant | Current automated evidence | Release evidence or issue | Gate status |
| --- | --- | --- | --- | --- | --- |
| ACK-1 | Write ACK boundary | A write is acknowledged only after required local bytes, required peer bytes, and authoritative metadata are durable for the configured profile. | `TestCrashAfterMetadataApplyKeepsCommittedDocumentRetryable`, `TestCrashBeforeACKKeepsCommittedDocumentRetryable`, `TestPrepareNormalWriteSucceedsWithQuorumAndMarksRepairRequired`, spike peer-prepare and Raft barrier tests. | #85 | Blocked. PR evidence is narrower than production release evidence. |
| ACK-2 | Partial write handling | Failed or uncommitted writes remain invisible after restart and cleanup cannot remove committed visibility or acknowledged bytes. | `TestCrashAfterBlockSyncLeavesDocumentInvisible`, `TestCrashAfterPrepareSyncLeavesDocumentInvisible`, `TestPrepareLogRecoveryTruncatesCrashCutTail`, `TestStoreFailedPreparedWriteStaysInvisibleAfterReopen`. | #85 | Blocked until crash campaign artifacts cover filesystem profile and failure schedule. |
| ACK-3 | Idempotent retry and unknown outcome | Duplicate public writes and duplicate admin jobs do not repeat unsafe mutation and can recover the original result. | `TestIdempotentReplayReturnsExistingDocumentWithoutAppending`, `TestPutDocumentIsIdempotentAndCountsOnce`, `TestApplyShardCommandDuplicateRequestIDIsIdempotent`, durable operation idempotency tests. | #85, #45 | Blocked until release aggregation records the evidence. |
| READ-1 | Visible read authority | `HeadDocument` and `ReadDocument` use authoritative metadata freshness, not stale local projection shortcuts. | `TestMetadataProjectionRebuildQueuesRepairForMissingLocalRef`, `TestAuthoritySnapshotCompactionReplaysSnapshotAndTail`, `TestRaftFaultHarnessTransportRestartAndReadIndex`, spike ReadIndex tests. | #85 | Blocked until dedicated Raft and projection evidence is accepted. |
| READ-2 | Restore state visibility | Cold or crypto-unavailable data returns typed restore/crypto detail before streaming bytes. | `TestReadDocumentReturnsRestorePendingDetail`, `TestReadDocumentReturnsCryptoUnavailableDetail`, `TestReadDocumentQueuesRestoreOnColdReadAndRetriesAfterRestart`. | #85, #86, #88 | Blocked until backend and OpenBao release evidence exists. |
| CORR-1 | Fail-closed reads | Full and ranged reads verify every touched frame before sending metadata or bytes; corrupt bytes are not streamed as valid document data. | `TestCorruptReadFailsBeforeSendingMetadata`, ranged corruption tests, `TestReadObjectRangeDetectsCorruptionBeforeWritingBytes`, and #143 verification-window bounds tests. | #85, #143 | Blocked until corruption scenarios are part of release evidence; #143 confirms the repo gate remains fail-closed for corrupt backend verification windows. |
| CORR-2 | Source quarantine and repair | Suspect local, peer, or backend sources are quarantined from serving; repair uses only verified sources. | `TestMetadataProjectionRebuildQueuesRepairForCorruptLocalRef`, `TestRunQueuedOperationsOnceQuarantinesCorruptPeerAndFailsWithoutVerifiedSource`, `TestRunQueuedOperationsOnceRepairsQuarantinedLocalBlock`, `TestInstallVerifiedRangeRepairsPreparedDocumentRange`, and the dashboard/alert contract. | #85 | Blocked until corruption scenarios are part of release evidence. |
| CORR-3 | All-sources-corrupt handling | If no verified source can satisfy a read or repair, the system produces typed integrity evidence and does not return bytes. | `TestRunQueuedOperationsOnceQuarantinesCorruptPeerAndFailsWithoutVerifiedSource`, `TestRunQueuedOperationsOnceDRDrillFailsWhenRequiredArtifactCorrupt`, backend verification mismatch tests, the dashboard/alert contract, and operator runbooks. | #85 | Blocked until corruption scenarios are part of release evidence. |
| REPLAY-1 | Local replay | Prepare/openlog records, committed metadata, and local projection rebuilds replay safely after crash or process restart. | `TestPrepareLogRecoveryTruncatesCrashCutTail`, `TestMetadataProjectionRebuildQueuesRepairForMissingLocalRef`, `TestLogAppendReplayAndContinueAfterReopen`, `TestAuthoritySnapshotCompactionReplaysSnapshotAndTail`. | #85 | Blocked until dedicated crash campaign records runner and filesystem profile. |
| REPLAY-2 | Durable operation replay | Restore, repair, scrub, rewrap, tombstone, drain, DR, and capacity operations resume or reach terminal state after restart. | `TestStoreRecoverInterruptedRequeuesRunningSupportedOperation`, `TestStoreRecoverInterruptedKeepsQueuedRetryEvidenceAcrossReopen`, `TestAdminServerStartedOperationStatusSurvivesStoreReopen`, `TestRecoverInterruptedOperationsRequeuesRunningCapacityOverrideAfterRestart`. | #85, #45 | Blocked until release aggregation consumes operation recovery evidence. |
| BACKEND-1 | Backend upload idempotency | Upload workers verify existing backend block, index, and envelope objects before idempotent success and retry failed or partial attempts safely. | `TestUploadBlockIsIdempotent`, `TestUploadBlockVerifiesCompleteObjectSetBeforeSuccess`, `TestProcessorRetriesPartialObjectSetAndRecordsUploaded`, `TestProcessorRecoversDurableFailedIntentAfterRestart`. | #85 | Blocked until backend fault campaign evidence exists. |
| BACKEND-2 | Backend restore and prewarm | Restore and prewarm jobs read verified backend artifacts, preserve restore-pending behavior, and record durable audit/operation evidence. | `TestRunQueuedOperationsOnceRestoresDocumentFromBackend`, `TestRunQueuedOperationsOncePrewarmsDocumentFromBackendAndAudits`, `TestReadDocumentFallsBackToVerifiedBackendCopy`, `TestRunQueuedOperationsOnceKeepsArchiveRestorePendingAndRetries`. | #85, #88 | Blocked until release and drill evidence exist. |
| DR-1 | Metadata restore | A clean cluster can import published metadata snapshots/tails and recover cold document metadata without treating local projections as authority. | `TestRunQueuedOperationsOnceMetadataRestoreImportsColdDocuments`, published metadata import tests, and `make local-dr-drill-evidence`. | #88 | Blocked until local DR drill evidence is accepted. |
| DR-2 | DR drill evidence | DR drill output records measured recovery evidence without making an unapproved formal RTO/RPO promise. | `TestRunQueuedOperationsOnceDryRunDROperationReportsReadiness`, `TestRunQueuedOperationsOnceDRDrillRestoresScratchMetadata`, DR drill failure tests, the DR rebuild drill runbook, and `make local-dr-drill-evidence`. | #48, #88 | Blocked until drill artifact is accepted. |
| OPENBAO-1 | Envelope restore | Backend block data can be restored only with required envelope and key material; missing material is typed crypto-unavailable evidence. | Envelope workflow tests and `TestRunQueuedOperationsOnceDRDrillFailsWhenKeyMaterialMissing`. | #86 | Blocked until real OpenBao smoke evidence exists. |
| OPENBAO-2 | Secret hygiene | Envelope and audit evidence never persists plaintext DEKs, wrapped DEKs, OpenBao tokens, or plaintext secrets. | Audit metadata sanitization tests, OpenBao smoke coverage document, critical-action audit tests. | #86, #56, #57 | Blocked until real-service evidence and lint cleanup are complete. |
| CAP-1 | Admission and runway | Production profiles validate ingress, disk runway, backend budgets, lane budgets, guard bands, and breaker thresholds before writes are admitted. | `TestProductionCapacityProfileValidation`, `TestProductionWriteACKGateFailsClosedWithoutReadinessEvidence`, backend profile tests, and `scrap-local-soak-evidence` report shape tests. | #48, #87 | Blocked until target inputs and local soak/capacity run are accepted. |
| OPS-1 | Operator control path | Dangerous actions are typed, durable, authorized, audited, and idempotent through the admin API and `scrapctl`, not scripts. | Admin plan/start tests, audit tests, authz denial tests, durable operation tests, release aggregation, the dashboard/alert contract, and operator runbooks. | `operator-runbook-approval` release artifact | Blocked until target deployment evidence approves the runbooks. |
| SEC-1 | Authorization and audit evidence | Denied requests and successful critical actions produce audit events with actor, capability, operation, reason, request/correlation context, and sanitized metadata. | `TestServerAuditsDeniedRequests`, `TestPublicServerAuditsCriticalAndEphemeralWritesOnly`, `TestAdminServerCriticalWorkflowStartsWriteSanitizedAuditEvents`, authz interceptor tests. | #45, #56, #57 | Blocked until release aggregation and hardened lint cleanup are complete. |
| OBS-1 | Diagnosis surface | Dashboards and alerts expose write admission, disk runway, backend lag, repair lag, restore lag, corruption incidents, Raft health, OpenBao health, and operation backlog. | Observability/audit standards document and `docs/storage-gateway-dashboard-alert-contract.md`. | `dashboard-alert-contract-approval` release artifact | Blocked until target deployment evidence proves the contract is live. |
| REL-1 | Release evidence aggregation | Every release-blocking gate is automated or has a named manual artifact owner and can explain missing evidence by stable gate name. | `make check`, `make vuln`, CodeQL workflow, release image/rollout policy. | #45 | Blocked until aggregation scripts/tests land. |
| HUMAN-1 | Target profile signoff | Capacity, compliance, product, operations, security, and release owners approve the first target deployment profile or record explicit deferrals. | Production capacity and compliance signoff input record. | #48 | Blocked until owner-provided values replace `TBD` and `Missing`. |

## Blocking Issue Map

Open blockers:

- #48 captures human-owned capacity, compliance, product, operations, security,
  and release signoff inputs.
- #86 captures real OpenBao Transit smoke evidence.
- #87 runs local release soak and capacity rehearsal evidence.
- #88 executes fresh-cluster DR rebuild drill evidence.
- #89 implements the final production write ACK enablement gate.

Resolved tooling and evidence-command work:

- #45 automated release-gate aggregation and missing-evidence reporting.
- #56 removed or narrowed the `gosec` baseline for path and integer conversion
  findings.
- #57 replaced broad unchecked `Close` lint exclusions with explicit handling.
- #85 implements the dedicated crash and fault evidence command.

No production write ACK gate may be marked ready while any linked blocker for
its row remains open, unless a release exception with owner, scope, mitigation,
and expiry is recorded in the release evidence bundle.
