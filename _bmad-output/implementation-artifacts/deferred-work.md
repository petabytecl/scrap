## Deferred from: BMAD full-project data-reliability review (2026-07-06)

Eight parallel Blind Hunter + Edge Case Hunter passes over the storage path
(`block`, `index`, `shard`, `raft`, `peer`, `encryption`, `localblock`,
`eviction`, `scrub`, `avscan`, `backend`) with a data-integrity/durability lens
and per-package coverage sweeps. Findings verified against the code before
acting.

Fixed on branch `fix/recovery-truncation-and-durability-hardening` (PR #460),
each with a regression test that fails without the fix:
- openlog `recoverPrepFile` refuses to truncate a Block region that holds
  committed `.idx` entries (was: rolling-restart could destroy ACKed Documents);
  `truncateFile` never zero-extends.
- `openNewBlock` fsyncs the blocks directory (was: power loss could lose a
  committed Block's files while the raft log referenced it).
- scrub `repairFromPeer` calls `VerifyHeader(shardID, blockID)` before promoting
  a peer replacement (was: could install a different Block under the wrong ID).

Also fixed: applied-index watermark carried across projection rebuilds
(PR #459).

Deferred to tracked issues on milestone `storage-gateway-v2` (need design/ADR
or a larger change):
- #461 projection rebuild drops content-quarantine records + scanner watermark
  (content-safety rollback; interacts with #459).
- #462 raft install-snapshot crash windows (Ready-loop ordering,
  persist-before-Restore, missing ReportSnapshot; `applyIncomingSnapshotLocked`
  at 0% coverage).
- #463 openlog recovery decides truncation before raft replay (single-member
  committed-byte loss).
- #464 projection rebuild loses commits applied during the rebuild window.
- #465 crash between `.idx` append and projection update can duplicate an `.idx`
  entry on replay.
- #466 rewrap `Changed` compares nondeterministic ciphertext → full Block
  re-upload on every no-op rewrap.
- #467 localblock restore/eviction marker crash windows; one corrupt marker
  aborts the sweep and surfaces as `ErrDataLoss` on intact Documents.
- #468 eviction plan lacks durable-copy proof; apply loop ignores plan expiry.
- #469 backend restore integrity (S3 Head/Get version race, unknown-code →
  permanent → DataLoss, missing FS ancestor fsync).
- #470 scrub false-quarantine on transient I/O; idx-corruption aborts run;
  repair cadence; light-scrub lacks quorum; avscan frontier reset.
- #471 test-coverage epic: crash-recovery, repair-failure, encrypted-path, and
  install-snapshot paths largely untested.

## Deferred from: code review of 2-7-bound-peer-replicatedocument-input-before-side-effects (2026-06-15)

- Local (no-sink) `ReplicateDocument` path appends to the Block before SHA-256 verification and does not roll back on mismatch [internal/peer/server.go:replicateToLocalBlock]. Pre-existing (the prior implementation appended then compared); the production sink path validates and aborts via `internal/shard.validateReplicatedAppend`, and the local path is a test/legacy fallback.
- SHA-256 verification is skipped when `init.Sha256` length != 32 on the local path [internal/peer/server.go:replicateToLocalBlock]. Pre-existing condition; the production sink path enforces SHA-256 downstream in `internal/shard`.
- Internal gRPC error messages embed raw os/dependency error strings (possible temp-path leak) on the peer surface [internal/peer/server.go]. Partially pre-existing peer transport error-mapping hardening; the peer surface is authenticated (mTLS + peer identity), limiting blast radius. Aligns with existing deferred peer transport error-mapping hardening.
- Context cancellation/deadline and `ENOSPC` during peer receive map to `codes.Internal` instead of `Canceled`/`DeadlineExceeded`/`ResourceExhausted` [internal/peer/server.go:receive]. Pre-existing peer transport mapping gap that also affects other peer streams.
- Over-limit unit test writes ~128 MiB/subtest to disk [internal/peer/replicate_bounds_test.go:TestReplicateDocumentRejectsDocumentOverLimitBeforeAcceptedState]. Real test-cost concern tied to the temp-file design decision; would disappear if streaming-with-inline-validation is adopted.

## Deferred from: code review of 1-6-fail-closed-on-missing-document-sha256-verification.md (2026-06-14)

- `VerifyBlock` can report clean when an index entry has `FrameCount == 0` and all-zero SHA-256. This appears pre-existing and outside the immediate Story 1.6 changed hunks, but it is a data-integrity hardening candidate for follow-up.
- Decide whether encrypted zero-SHA256 metadata should make `VerifyBlock` report `doc_sha256` corruption. Reason: separate scope: encrypted Block verification semantics need their own story.

## Deferred from: quick-dev scope split "fix open github issues" (2026-06-13)

- GitHub issue #438 — Validate Tier 3 evidence stack + stress/bundle phases end-to-end on CI. Blocked by #437 (Tier 3 `evidence-gate.yml` runs the full E2E suite before the stress/evidence-bundle phases). Deferred so #437 (flaky multi-member E2E suite) can be fixed first; resume #438 once a green E2E run is achievable. Remaining scope: confirm observability stack (loki/mimir/tempo/pyroscope/alloy/otel-collector/grafana/kube-state-metrics) reaches Ready on a hosted runner, validate the stress run + evidence-bundle generation (`manifest.json`, `gates.json`, `privacy-scan.json`, retention, privacy PASS), and consider readiness probes on monitoring deployments.

## Deferred from: code review of 1-4-transaction-scoped-document-discovery (2026-06-11)

- Rebuilding Store errors still map to `INTERNAL` [internal/server/server.go:570]: `storeapi.ErrRebuilding` from Shard read gates is not handled by central server Store error mapping, so public read/discovery paths can report `INTERNAL` instead of a transient unavailable-style status during projection rebuild. Deferred as pre-existing because Story 1.4 did not introduce `mapStoreError` or the Shard rebuilding sentinel.

## Deferred from: code review of 2-2-multi-shard-cell-startup-composition (2026-06-11)

- Peer replication sink buffers full replicated Documents before dispatch [internal/peer/server.go]: `replicateToSink` existed before Story 2.2 and buffers the full peer replication body before calling the sink. Deferred as pre-existing peer transport hardening outside the Story 2.2 composition boundary.
- Peer replication sink wraps status-bearing sink errors as `INTERNAL` [internal/peer/server.go]: `replicateToSink` existed before Story 2.2 and maps sink errors through a generic internal status. Deferred as pre-existing peer transport error-mapping hardening outside the Story 2.2 composition boundary.

## Deferred from: code review of 2-6-multi-shard-evidence-closure (2026-06-11)

- CI runner migration is outside Story 2.6 scope [.github/workflows/ci.yml:21]: the runner migration was committed before the Story 2.6 evidence implementation and needs separate CI evidence if it is kept. Deferred as pre-existing relative to this story review.

## Deferred from: full-project adversarial code review (2026-07-05)

Parallel Blind Hunter + Edge Case Hunter passes over the whole `internal/`
tree; verified fixes committed on branch `code-review-hardening`. The items
below were deferred because they need a bigger change (streaming APIs, new
subsystem, signature changes) or a design decision. Each is mirrored to a
GitHub issue on milestone `storage-gateway-v2`.

### DW-1: raft has no snapshot creation, log compaction, or WAL segment release

origin: full-project code review (consensus & transport), 2026-07-05
location: internal/raft/node.go (Ready loop; storage/WAL lifecycle)
severity: critical
reason: The node loads and receives snapshots but never calls CreateSnapshot/Compact/ReleaseLockTo, so MemoryStorage and the WAL grow without bound (eventual OOM / disk exhaustion) and a lagging follower cannot be caught up via install-snapshot. Needs a snapshot subsystem + retention window + likely an ADR. Mirror: GitHub #443.
status: resolved (2026-07-05) — snapshot creation/compaction/WAL release + purge on branch code-review-hardening (e2b8d30, ADR 0029); foreign install-snapshot fails closed with re-seed guidance, automated catch-up via replica repair is the documented follow-up

### DW-2: EncryptDocument/DecryptDocument buffer whole documents in memory

origin: full-project code review (security & telemetry), 2026-07-05
location: internal/encryption/envelope.go
severity: high
reason: Frames are accumulated into [][]byte on encrypt and returned as one []byte on decrypt, wired into shard write/read/verify — an authenticated memory-amplification vector against CONTEXT.md's bounded-memory invariant. Fixing requires a streaming crypto API across callers. Mirror: GitHub #444.
status: resolved (2026-07-05) — streaming DocumentEncryptor/DocumentDecryptor wired into shard write/read/verify (617bf83, ADR 0028); remaining whole-Document copy is the peer replication buffer, tracked by the pre-existing peer transport deferred item

### DW-3: avscan scan watermark stalls on a permanent Block-ID gap; completed map is unbounded

origin: full-project code review (security & telemetry), 2026-07-05
location: internal/avscan/scheduler.go (advanceProgressThroughCompleted; completed map)
severity: high
reason: The frontier only advances through contiguous Block IDs, so a quarantined/evicted block at frontier+1 pins the durable watermark forever (full re-scan every restart); the in-memory completed map also grows without bound. Needs gap-aware frontier advance + map pruning + a test matrix. Mirror: GitHub #445.
status: resolved (2026-07-05) — gap-aware frontier advance + restored-block rescan eligibility + completed-map pruning (720a50f)

### DW-4: NewRateLimiter silently drops invalid surface budgets (fails open)

origin: full-project code review (security & telemetry), 2026-07-05
location: internal/security/ratelimit.go (NewRateLimiter)
severity: medium
reason: Surfaces with Limit<=0/Window<=0 are skipped and the constructor has no error return, so a hand-built policy can fail open. Production goes through the validating LoadRateLimitPolicy, so live risk is low; the fix is a signature change. Mirror: GitHub #446.
status: resolved (2026-07-05) — NewRateLimiter returns an error on invalid/duplicate/unknown surface budgets (ed198d3)

### DW-5: audit Policy.MaxEventBytes and FailureMode are validated but never enforced

origin: full-project code review (security & telemetry), 2026-07-05
location: internal/audit/audit.go
severity: low
reason: Both fields are checked at construction but consulted nowhere — dead config that reads as an enforced control. Either wire them (truncate/reject oversized events; act on FailureMode on sink write failure) or remove them; needs a semantics decision. Mirror: GitHub #447.
status: resolved (2026-07-05) — PolicySink enforces MaxEventBytes (reject oversized) and FailureMode (fail_closed propagates, fail_open logs and drops); wired in newAppAuditSink (8ef00e9)

### DW-6: ApplyEvictionPlan caches a terminal Failed result and strands the rest of the plan

origin: full-project code review (backend/eviction/scrub), 2026-07-05
location: internal/eviction/apply.go; internal/eviction/campaigns.go
severity: medium
reason: A single non-context block failure breaks the loop with cacheable=true, so the Failed result is cached and re-issuing apply for the same plan returns it without retrying; later Selected blocks are never attempted. Needs a transient-vs-permanent cacheability distinction or continue-past-failure — a retry-semantics decision. Mirror: GitHub #448.
status: resolved (2026-07-05) — ApplyBlocks continues past per-block failures and results with failures are not cached, so re-apply retries exactly the failed blocks (7dd33db)

### DW-7: FS ListObjects prefix semantics diverge from S3 (path-segment prune vs byte prefix)

origin: full-project code review (backend/eviction/scrub), 2026-07-05
location: internal/backend/fs.go (listWalkRoot) vs internal/backend/s3.go
severity: low
reason: A partial-segment prefix returns different sets on FS vs S3, so the provider-neutral Backend contract is not truly interchangeable. Latent — all current callers pass full-segment, slash-terminated prefixes. Fix: walk the parent and filter by string prefix, or enforce slash-terminated prefixes, plus a conformance test. Mirror: GitHub #449.
status: resolved (2026-07-05) — FS listWalkRoot prunes only to the parent of the last (possibly partial) segment; byte-prefix parity locked by test (24f058c)

### DW-8: evidence-bundle log-value redaction is a denylist net with structural holes

origin: full-project code review (CLI & cmd), 2026-07-05
location: internal/scrapctl/evidencebundle/http.go (redactLogValue/containsSensitiveLogString)
severity: medium
reason: Only host-path fragments are redacted; arbitrary sensitive tokens or bare-value identifiers in free-text log messages pass through, relying on scrapd's upstream hashing contract as backstop. (The /data host-root gap in this denylist was fixed and both layers now share sensitiveHostPathRoots; this is the residual structural weakness.) Fix: positive allowlist of permitted shapes, or document the upstream-hashing dependency. Mirror: GitHub #450.
status: resolved (2026-07-05) — log evidence rebuilt through a positive allowlist (enum status/resultType, bounded labels, timestamps only); free text never copied (d09e319)

### DW-9: evidence-bundle leaves a partial directory on mid-run error; no atomic finalize

origin: full-project code review (CLI & cmd), 2026-07-05
location: internal/scrapctl/evidencebundle/bundle.go (Generate)
severity: low
reason: Any post-init error returns without removing the partially-written bundle or emitting a completeness marker; consumers can only infer completeness from manifest.json presence. Fix: write to a temp dir and atomically rename on success, or emit a terminal marker. Low urgency (CLI returns non-zero). Mirror: GitHub #451.
status: resolved (2026-07-05) — bundle staged under <name>.partial and renamed atomically on success; failures remove staging (2eabe34)

### DW-10: shard diagnostics snapshot is non-atomic across sub-reads

origin: full-project code review (CLI & cmd), 2026-07-05
location: internal/cmd/shard_diagnostics.go (applyLiveShardDiagnostics)
severity: low
reason: Readiness/leader/pressure/scanner/eviction fields are read as separate calls with no shared lock, so a concurrent leadership change can yield inconsistent fields. Display-only for a best-effort read-only endpoint; filed for visibility and may be closed as accepted. Mirror: GitHub #452.
status: accepted (2026-07-05) — closed #452 as not-planned per triage decision; display-only best-effort endpoint, no consumer acts on the snapshot

## Deferred from: final overnight review of branch code-review-hardening (2026-07-05, post-DW fixes)

Multi-angle review (line-by-line, removed-behavior, cross-file, reuse,
efficiency, altitude, conventions) with per-candidate verification over
`git diff main...HEAD`. Verified findings below, most severe first.

Triage outcome (2026-07-05): the confirmed correctness findings FR-1, FR-2,
FR-3, and FR-4 were fixed on this branch with regression tests (see each item).
FR-5 through FR-10 (medium/low severity and cleanups) remain open for follow-up.

- FR-1 (CONFIRMED, high): `block.Writer.Truncate` resets only `offset`, not `docSeq`/`docCount`. Abort paths that truncate after a successful append (peer SHA-mismatch rollback internal/peer/server.go:~294; leader `abortWriteAttemptLocked` internal/shard/shard.go:~541) leak a docSeq, so the next committed Document's frame DocSeq no longer matches its `.idx` position; deep scrub (`verifyFrames` positional mapping) then reports CorruptionMissing/doc_sha256 and quarantines an intact Block. Self-heals on restart (`scanWriterState` recomputes). Fix: roll back docSeq/docCount on truncate-to-pre-append or have abort paths restore writer doc counters. **RESOLVED (2026-07-05):** `Truncate` re-derives docSeq/docCount from the surviving Frames via `scanWriterState`, so counters match disk after any partial or complete aborted append; regression `TestBlockWriterTruncateRollsBackDocCounters`.
- FR-2 (CONFIRMED, medium-high): `internal/scrub/deep.go` RunOnce force-clears `SetBadDiskSuspected(false)` before `ListSealedBlocks`; on a listing error it returns early, so during an actual disk failure the latched bad-disk gauge is cleared every cycle and reads healthy. Move the clear after a successful listing (or re-raise on the error path). **RESOLVED (2026-07-05):** the clear now runs only after `ListSealedBlocks` succeeds, so a failing listing leaves the latched gauge intact; regression `TestDeepScrubber_KeepsBadDiskGaugeWhenListingFails`.
- FR-3 (CONFIRMED, medium): strict `parseCanonicalBlockID` makes `block.ListSealedBlocks` hard-error on any stray `*.blk` filename, halting ALL scanning and scrubbing every cycle until the file is removed; `blockIDFromBlockPath` (scrub_dependencies.go) still uses the lenient parse, so the two disagree. Decide: skip-with-loud-log for unrecognized names, or quarantine the stray file. **RESOLVED (2026-07-05):** owner chose skip-with-loud-log. `ListSealedBlocks(dir, openBlockID, logger)` now skips non-canonical `*.blk` names and emits a `Warn("block: ignoring unrecognized block filename", …)` instead of erroring; scan (`scannerCoordinator`) and scrub (`scrubCoordinator`) pass their shard logger. `TestListSealedBlocks_MalformedFilenameFails` replaced by `TestListSealedBlocks_SkipsMalformedFilename`. Residual: the loud signal is the WARN log only; a dedicated `malformed_block_file` metric counter is an optional follow-up (not plumbed, since the two callers meter at different layers).
- FR-4 (PLAUSIBLE, medium): `projectionDirMissingOrEmpty` returns `os.IsNotExist(err)` for ReadDir errors, so EACCES/EIO reads as "present"; `recoverProjectionSwapDirs` then deletes every `pebble.previous-*` backup without restoring — fail-destructive on a failing disk. Treat non-ENOENT errors as abort (or as restore-needed). **RESOLVED (2026-07-05):** `projectionDirMissingOrEmpty` now returns `(bool, error)` and surfaces non-ENOENT read errors; `recoverProjectionSwapDirs` aborts before the backup-delete loop, preserving the pre-rebuild backup; regression `TestRecoverProjectionSwapDirsAbortsOnUnreadableProjection`.
- FR-5 (PLAUSIBLE, medium): `internal/shard/raft_snapshot.go` uses member identity as a proxy for "durable state covers snapshot index": a partial DataDir restore (pebble/blocks older than raft snap/wal) self-matches and silently diverges; identity churn with intact data spuriously fails closed. Deeper fix: record and compare a durable applied watermark in the manifest. **RESOLVED (2026-07-05, ADR 0030):** the Pebble Projection now persists a durable applied-index watermark (`\x00applied-index\x00`, synced at snapshot creation, excluded from `StreamingHash`); `SnapshotFunc` takes the applied index, the manifest records it (version 2), and `restoreRaftSnapshot` fails closed when the projection's durable applied index is below a self snapshot's index. Version 1 manifests stay accept-on-identity. Regressions: `TestRestoreRaftSnapshotFailsClosedOnPartialRestore`, `TestRestoreRaftSnapshotAcceptsLegacyV1Manifest`, `TestPersistAppliedIndex_SurvivesReopen`, `TestPersistAppliedIndex_ExcludedFromStreamingHash`.
- FR-6 (CONFIRMED, medium-low): restore markers are never removed (no `RemoveRestoreMarker` in non-test code) and the avscan `completed` map is process-local, so every restored Block below the frontier is rescanned once per process restart forever and inflates initial `LagBlocks`; markers accumulate unbounded. Add marker cleanup (e.g., after a durable post-restore scan record or on re-eviction). Mirror: GitHub #454.
- FR-7 (CONFIRMED, low-medium): ciphertext-length mismatch is now detected only after the Transit `UnwrapDataKey` round trip (`DecryptDocument`) or at stream EOF after full decryption (`documentDecryptReader.finish`); the old code rejected before any KMS/crypto work. Cheap pre-check where frame sizes are known up front. Mirror: GitHub #455.
- FR-8 (CONFIRMED, low): `advanceProgressFrontier` prune loop iterates the entire `completed` map once per scanned Block; with a stuck listed frontier Block this is O(N²). Prune only the just-advanced `(before, frontier]` delta. Mirror: GitHub #456.
- FR-9 (cleanup): `raft.Config.MaxWALSize` is a dead knob (never read) inside the very WAL-retention rewrite an operator would expect it to govern — delete or wire it. Related: `purgeOldestFiles` reimplements etcd `fileutil.PurgeFile`, purges only on snapshot creation, and only `.snap` (not `.snap.db`/`.broken`). Mirror: GitHub #457.
- FR-10 (cleanup): buffering `EncryptDocument`/`DecryptDocument` and `EncryptedDocument.Frames` are now test-only but remain exported production API — the exact unbounded-memory path ADR 0028 removed can be silently reintroduced by a new caller. Move to a test helper or delete. Related duplication: writer/encryptor one-frame-lookahead state machines and `frameStreamReader` vs `StoredFrameSource` share no code; `NewDocumentEncryptor`/`NewDocumentDecryptor`/`NewSliceFrameSource` and `StoredFrameSource` methods are missing godoc (go-style-guide: godoc on all exports). Mirror: GitHub #458.

## PR #453 review round (2026-07-05): CodeQL + Codex bot findings

Feedback on the pushed branch. Resolved on-branch with regression tests:

- CodeQL `go/uncontrolled-allocation-size` (high) on `internal/index/content_quarantine.go` and `internal/cmd/app.go` — **RESOLVED:** list allocations sized from a caller limit replaced by a fixed prealloc hint (min-with-const does not clear the taint); loops still bound growth.
- Codex P2 `internal/scrub/light.go` — **RESOLVED:** light scrub returns a degraded error when the Cell has peers but none produced a comparable consistency result, instead of recording `ok`; regression `TestLightScrubber_AllPeersUnreachableIsNotOK`.
- Codex P2 `internal/avscan/scheduler.go` (repaired Blocks below frontier) — **RESOLVED:** deep-scrub peer repair writes a restore marker (`source=peer`, `reason=repair`) so the scanner keeps the Block eligible below the frontier; regression `TestBlockRepair_VerifiedReplacementRecordsRestoreMarker`.
- Codex P2 `internal/shard/encryption.go` (ciphertext overhead) — **RESOLVED:** peer replication validates the wire body against `encryption.MaxCiphertextSize(MaxDocumentBytes)`, budgeting AES-GCM per-frame expansion; regressions in `internal/encryption` and `internal/peer`.
- Codex P2 `internal/shard/raft_snapshot.go` — **RESOLVED:** implemented as the FR-5 durable applied-index watermark (ADR 0030); see FR-5 above.

## PR #453 review rounds 2–3 (2026-07-06): Codex bot findings after the last push

Six threads landed after commit dd8dc84 (rounds at 23:37Z and 00:30Z). Each was
verified against the code before acting; five confirmed and fixed on-branch,
one refuted with a pinning regression test.

- Codex P1 `internal/shard/shard.go:506` (truncation after committed apply error) — **CONFIRMED, FIXED:** branch-introduced regression (main preserved bytes on apply error). Truncating destroyed Frames that peers indexed and that a restart replay re-indexes — the replayed index entry then pointed past EOF. `resolveCommitApplyFailure` now truncates only on the deterministic conflict rejection (`ErrAlreadyExists`); other apply errors preserve the bytes and drop the prep so Openlog recovery cannot truncate them either. Regressions `TestResolveCommitApplyFailure*`.
- Codex P2 `internal/shard/shard.go:463` (followers stranded after partial replication) — **CONFIRMED, FIXED** (worse than rated: a stranded-ahead replica keeps applying committed entries and indexes bytes it never received). `AppendReplicatedDocument` now rolls an uncommitted overhang back to the leader's offset, refusing when the open Block's index references the overhang, and removes the aborted attempts' preps so recovery cannot cut later commits at that offset. Regressions `TestRollbackReplicaOverhang*`, `TestAppendReplicatedDocument_RollsBackAbortedOverhang`. Residual (pre-existing, shared with restart recovery): a rollback racing an in-flight commit apply degrades to the existing scrub-detect/peer-repair floor.
- Codex P2 `internal/peer/server.go:436` (plaintext replication cap) — **CONFIRMED, FIXED:** the fcab885 ciphertext budget applied to unenveloped bodies too, and the plaintext sink path appends without re-checking size. The wire budget now depends on the init's encryption envelope; declared TotalBytes (plaintext on both paths) is checked against `MaxDocumentBytes` again. Regressions in `internal/peer/replicate_bounds_test.go`.
- Codex P2 `internal/scrapctl/evidencebundle/bundle.go:799` (capture-error payload read as PASS) — **REFUTED:** `jsonArtifactHasEvidence` already treats `{"error": ...}` as no evidence, so `evictionEvidence` returns FAIL for the capture-error payload. Behavior was unpinned; regression `TestEvictionEvidenceFailsOnCaptureErrorPayload` added.
- Codex P2 `.agents/skills/bmad-loop-setup/scripts/cleanup-legacy.py:112` — **CONFIRMED, FIXED:** verification required only a bare installed directory; now requires the installed `SKILL.md` before deleting legacy trees. Smoke-tested both paths.
- Codex P2 `.agents/skills/bmad-loop-setup/scripts/merge-config.py:252` — **CONFIRMED, FIXED:** user-only keys stripped from a pre-split `config.yaml` are now carried into `config.user.yaml` as fallbacks (answers and existing user-config values win). Smoke-tested migration and precedence.
