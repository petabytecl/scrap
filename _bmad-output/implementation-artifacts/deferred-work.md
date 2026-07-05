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
status: open

### DW-2: EncryptDocument/DecryptDocument buffer whole documents in memory

origin: full-project code review (security & telemetry), 2026-07-05
location: internal/encryption/envelope.go
severity: high
reason: Frames are accumulated into [][]byte on encrypt and returned as one []byte on decrypt, wired into shard write/read/verify — an authenticated memory-amplification vector against CONTEXT.md's bounded-memory invariant. Fixing requires a streaming crypto API across callers. Mirror: GitHub #444.
status: open

### DW-3: avscan scan watermark stalls on a permanent Block-ID gap; completed map is unbounded

origin: full-project code review (security & telemetry), 2026-07-05
location: internal/avscan/scheduler.go (advanceProgressThroughCompleted; completed map)
severity: high
reason: The frontier only advances through contiguous Block IDs, so a quarantined/evicted block at frontier+1 pins the durable watermark forever (full re-scan every restart); the in-memory completed map also grows without bound. Needs gap-aware frontier advance + map pruning + a test matrix. Mirror: GitHub #445.
status: open

### DW-4: NewRateLimiter silently drops invalid surface budgets (fails open)

origin: full-project code review (security & telemetry), 2026-07-05
location: internal/security/ratelimit.go (NewRateLimiter)
severity: medium
reason: Surfaces with Limit<=0/Window<=0 are skipped and the constructor has no error return, so a hand-built policy can fail open. Production goes through the validating LoadRateLimitPolicy, so live risk is low; the fix is a signature change. Mirror: GitHub #446.
status: open

### DW-5: audit Policy.MaxEventBytes and FailureMode are validated but never enforced

origin: full-project code review (security & telemetry), 2026-07-05
location: internal/audit/audit.go
severity: low
reason: Both fields are checked at construction but consulted nowhere — dead config that reads as an enforced control. Either wire them (truncate/reject oversized events; act on FailureMode on sink write failure) or remove them; needs a semantics decision. Mirror: GitHub #447.
status: open

### DW-6: ApplyEvictionPlan caches a terminal Failed result and strands the rest of the plan

origin: full-project code review (backend/eviction/scrub), 2026-07-05
location: internal/eviction/apply.go; internal/eviction/campaigns.go
severity: medium
reason: A single non-context block failure breaks the loop with cacheable=true, so the Failed result is cached and re-issuing apply for the same plan returns it without retrying; later Selected blocks are never attempted. Needs a transient-vs-permanent cacheability distinction or continue-past-failure — a retry-semantics decision. Mirror: GitHub #448.
status: open

### DW-7: FS ListObjects prefix semantics diverge from S3 (path-segment prune vs byte prefix)

origin: full-project code review (backend/eviction/scrub), 2026-07-05
location: internal/backend/fs.go (listWalkRoot) vs internal/backend/s3.go
severity: low
reason: A partial-segment prefix returns different sets on FS vs S3, so the provider-neutral Backend contract is not truly interchangeable. Latent — all current callers pass full-segment, slash-terminated prefixes. Fix: walk the parent and filter by string prefix, or enforce slash-terminated prefixes, plus a conformance test. Mirror: GitHub #449.
status: open

### DW-8: evidence-bundle log-value redaction is a denylist net with structural holes

origin: full-project code review (CLI & cmd), 2026-07-05
location: internal/scrapctl/evidencebundle/http.go (redactLogValue/containsSensitiveLogString)
severity: medium
reason: Only host-path fragments are redacted; arbitrary sensitive tokens or bare-value identifiers in free-text log messages pass through, relying on scrapd's upstream hashing contract as backstop. (The /data host-root gap in this denylist was fixed and both layers now share sensitiveHostPathRoots; this is the residual structural weakness.) Fix: positive allowlist of permitted shapes, or document the upstream-hashing dependency. Mirror: GitHub #450.
status: open

### DW-9: evidence-bundle leaves a partial directory on mid-run error; no atomic finalize

origin: full-project code review (CLI & cmd), 2026-07-05
location: internal/scrapctl/evidencebundle/bundle.go (Generate)
severity: low
reason: Any post-init error returns without removing the partially-written bundle or emitting a completeness marker; consumers can only infer completeness from manifest.json presence. Fix: write to a temp dir and atomically rename on success, or emit a terminal marker. Low urgency (CLI returns non-zero). Mirror: GitHub #451.
status: open

### DW-10: shard diagnostics snapshot is non-atomic across sub-reads

origin: full-project code review (CLI & cmd), 2026-07-05
location: internal/cmd/shard_diagnostics.go (applyLiveShardDiagnostics)
severity: low
reason: Readiness/leader/pressure/scanner/eviction fields are read as separate calls with no shared lock, so a concurrent leadership change can yield inconsistent fields. Display-only for a best-effort read-only endpoint; filed for visibility and may be closed as accepted. Mirror: GitHub #452.
status: open
