# Phase 4 partial local eviction boundary

Status: Accepted

Date: 2026-05-31

## Context

Phase 3 added Backend upload, upload pressure, and evidence bundles. ADR 0012
requires throughput, mixed read/write/head, and upload-pressure evidence before
Phase 4 begins because Phase 4 removes some local `.blk` data copies and
therefore turns Backend restore into part of the read-availability story.

The SCRAP lifecycle in `CONTEXT.md` names Phase 4 as "Partial eviction" where
followers evict uploaded Block data. Phase 5 is the separate future state where
all local copies may be evicted and reads become Backend-only.

External systems research on 2026-05-31 reinforced a narrow conclusion:
S.C.R.A.P. should not copy an S3-compatible gateway shape, but it should carry
forward fail-closed security boundaries, explicit backend retry policy,
deployment invariants, and operator visibility before eviction is implemented.
See `docs/research/2026-05-31-external-storage-systems.md`.

## Tracking

GitHub tracking issue: #381.

Implementation slices are published in
`docs/phase-4-eviction-implementation-slices.md` and tracked by #372 through
#380.

## Decision

Phase 4 is limited to partial local eviction of already-uploaded sealed Block
data files. It keeps local `.idx` files so Projection Resolution, `HeadDocument`,
and `FindDocuments` remain local. It does not introduce cold-only reads, API
deletion, S3 compatibility, tenant quota authority, encryption, or multi-cell
federation.

A Block is eligible for local eviction on a Member only when all of the following
are true:

- the Block is sealed;
- the `.blk` and `.idx` Backend objects were uploaded and verified;
- the matching `ConfirmUpload` command is committed in Raft;
- the local `evictable` predicate passes from the Confirmed Upload Catalog,
  current local state, and configured hot residency window;
- the Block is not quarantined and no repair is in progress;
- no local writer, scrubber, repairer, or reader holds the Block open;
- the Member can report the eviction decision through health/telemetry;
- a restore path from Backend to the local `.blk` file has already passed tests
  for the same object key format.

Backend HEAD or list observations are not sufficient eviction authority. A Block
that appears present in the Backend but lacks a committed `ConfirmUpload` remains
pending upload and must not be locally evicted.

Phase 4 adds a derived local Confirmed Upload Catalog in the Pebble Projection.
It is populated from committed `ConfirmUpload` commands, joined with the matching
sealed Block metadata, and stores the Block ID, confirmed timestamp, sealed size,
and Backend metadata for both objects: `.blk` key, size, and ETag or provider
validation token; `.idx` key, size, and ETag or provider validation token. The
catalog is not new authority: Raft remains the source of truth, and the catalog
is rebuildable from Raft replay. It exists so eviction planning, `.blk` restore,
and explicit `.idx` repair can still find committed Backend metadata after
`applyConfirmUpload` removes the Block from the Upload Outbox.

Phase 4 must split `ConfirmUpload` object metadata before eviction depends on it.
The current combined ETag string is sufficient for upload-pressure cleanup but is
too weak for restore and repair contracts. `ConfirmUpload` should carry separate
`.blk` and `.idx` provider validation tokens and sizes so the Confirmed Upload
Catalog does not need brittle string parsing.

Because SCRAP is not production yet, this is a hard proto/Raft-log cut rather than
a backward-compatible migration. New Phase 4 code may require the split
`ConfirmUpload` shape and does not need to treat older combined-ETag
`ConfirmUpload` entries as evictable or restore-capable.

Phase 4 may also hard-cut existing local development data directories. Evidence
Cells should start from fresh data or run an explicit rebuild/reupload workflow
that writes the new Confirmed Upload Catalog shape. Refactors needed to make the
restore/eviction boundary clean are acceptable; compatibility with pre-Phase-4
local state is not a requirement.

Confirmed Upload Catalog rebuild is fail-closed. A committed `ConfirmUpload`
without matching sealed Block metadata does not produce an evictable catalog row.
The Member reports degraded health and must not locally evict the Block until
the Raft-derived lifecycle can be reconciled. Best-effort catalog rows are not
allowed because restore verification depends on the sealed size and Backend
metadata.

Each Member computes local `evictable` state during candidate planning from the
Confirmed Upload Catalog, current local files, quarantine/repair/open-reader
state, leadership state, and the configured hot residency window. `Evictable`
means the copy may be selected by a bounded campaign; it does not mean the
Member may evict unilaterally. `Evictable` is a predicate and is not persisted
as a marker, because it can change when leadership, quarantine, repair, open
readers, or configuration changes. `Evicted` is the durable lifecycle transition:
the Member has written the eviction marker and unlinked the local `.blk`. No
extra upload-leader RPC is needed to mark copies evictable.

The default hot residency window is `24h` after committed `ConfirmUpload`. It is
configurable with `SCRAP_EVICTION_HOT_RESIDENCY_WINDOW`, parsed as a Go duration
string such as `24h`, `6h`, or `30m`. Invalid or negative values fail startup.
Evidence and local test environments may set a shorter value so Phase 4
eviction/restore runs do not need to wait a full day.

Candidate planning uses the current configured hot residency window. Changing
`SCRAP_EVICTION_HOT_RESIDENCY_WINDOW` changes future eligibility calculations,
but does not automatically restore Blocks that were already evicted. Dry-run
evidence must show the `confirmed_at` timestamp, active residency window, and
computed `eligible_at` time for candidates or time-window skips.

Successful restore resets hot residency for that local `.blk` copy. Candidate
planning computes `eligible_at` from the later of the committed `ConfirmUpload`
timestamp and the last successful local restore timestamp, plus the active hot
residency window.

Each Member records successful local restore time as local filesystem lifecycle
metadata, not Raft state and not Confirmed Upload Catalog state. The restore
marker/catalog is versioned JSON text keyed by Block ID and records
`restored_at`, restore source, and low-cardinality restore reason such as
`read`, `validation`, or `repair`. It exists only to compute future local
eviction eligibility; restore correctness still comes from full verification
against the retained `.idx` and committed Backend metadata.

Restore metadata is retained across later eviction and overwritten on the next
successful restore. Candidate planning uses `last_restored_at` only when the
local `.blk` is present and not marked evicted. If the `.blk` is absent with an
eviction marker, restore metadata is historical evidence and does not make the
Block hot or readable.

Eviction dry-run plan tokens default to a `10m` TTL, configurable with
`SCRAP_EVICTION_PLAN_TTL` as a Go duration string. Invalid, zero, or negative
values fail startup.

Eviction dry-run recommendations default to at most 10 Blocks and 640 MiB per
plan, configured by `SCRAP_EVICTION_RECOMMENDED_MAX_BLOCKS` and
`SCRAP_EVICTION_RECOMMENDED_MAX_BYTES`. Invalid, zero, or negative values fail
startup. Operator-supplied caps may reduce the recommended scope for a plan, but
cannot expand beyond the configured recommendation ceilings without changing the
environment configuration.

Validation samples are capped by `SCRAP_EVICTION_MAX_VALIDATE_SAMPLES`, default
`1`. Invalid or negative values fail startup. Operators may request fewer samples
for a campaign, but cannot exceed the configured cap.

The first Phase 4 implementation evicts follower-local `.blk` copies only. The
current leader keeps local `.blk` copies for the normal hot read path. If
leadership changes to a Member that has evicted a requested Block's data file,
the new leader restores the `.blk` from the Backend before serving the read, or
returns a typed transient unavailability error. It must never return partial or
least-bad bytes.

Operator-requested eviction of the current Shard leader's local `.blk` copy is
rejected in Phase 4 with `FAILED_PRECONDITION` and a reason such as
`leader_hot_copy_required`. Phase 4 proves follower-local eviction only; it does
not intentionally make the active leader's normal read path cold.

The first eviction trigger is operator-gated and system-evidence-driven. An
operator explicitly approves a bounded candidate campaign, and the system emits
the telemetry/evidence that proves marker, unlink, restore, and read behavior.
The operator is a safety gate, not the evidence source. Automatic disk-pressure
eviction is deferred until restore and unlink behavior has passed evidence runs.
This keeps the first Phase 4 slice focused on lifecycle correctness instead of
mixing it with admission-policy tuning.

When the serving leader needs a locally evicted Block, Phase 4 restores the full
`.blk` data file from the Backend before serving the read. It does not stream
directly from the Backend to the client. Full-Block restore keeps the existing
Block/Frame verification path as the only read implementation and preserves the
all-or-error `ReadDocument` contract. Direct cold streaming is deferred to
Phase 5.

Metadata-only reads do not trigger restore in Phase 4. `HeadDocument` and
`FindDocuments` continue to use the retained local `.idx` files through
Projection Resolution and remain independent of Backend availability. Only
`ReadDocument` needs the `.blk` bytes and may trigger restore.

If restore cannot proceed because the Backend dependency is transiently
unavailable, the API returns gRPC `UNAVAILABLE` with a restore-specific reason
such as `backend_restore_unavailable`. `DATA_LOSS` is reserved for verified
corrupt bytes, checksum failure, or committed metadata that cannot match the
restored Block.

If restore reaches the Backend but a previously confirmed `.blk` object is
missing or fails verification, the API returns gRPC `DATA_LOSS` with a
restore-specific reason such as `backend_restore_missing` or
`backend_restore_corrupt`. A missing or corrupt confirmed Backend object breaks
the upload-confirmation invariant; it is not a transient dependency failure. The
restore must not publish staged bytes, and the eviction marker remains for
repair or operator intervention.

Restore is owned by the Shard as a per-Block singleflight operation. The first
read that needs an evicted Block starts the restore, and concurrent reads for
the same Block wait on that in-flight restore instead of issuing duplicate
Backend downloads. Client cancellation stops that client from waiting, but does
not automatically cancel a restore that may satisfy later requests. Restore work
has its own timeout, backoff, and concurrency budget, and must not hold the
Shard's main mutex while downloading from the Backend. After restore publishes
the full `.blk` locally, waiting reads use the normal local Block reader.

Eviction state is local member state, not metadata authority. Raft remains the
authority for Document visibility and physical refs. Pebble remains a derived
Projection. Removing a local `.blk` file does not alter Document existence.

Each Member records intentional local eviction with a small filesystem marker for
the Block data file. The marker is local lifecycle state, not Raft state. It
records enough to restore and verify the `.blk` from the Backend, including the
Block ID, expected object key, size, checksum or ETag available from committed
metadata, eviction time, and eviction reason. On restart, marker present with
`.blk` absent and `.idx` present means intentionally evicted and
restore-eligible; marker absent with `.blk` absent means unexpected local loss
and must fail closed into repair or operator intervention. Any missing `.idx` in
Phase 4 is unexpected metadata loss and also fails closed. Quarantine files
remain the corruption path and take precedence over eviction markers.

The eviction marker is versioned JSON text so operators can inspect it during
evidence runs without decoding a binary format. The marker records a
low-cardinality trigger such as `operator_requested` and a low-cardinality reason
such as `evidence_run`; it does not use `operator_evidence`, because the system
emits evidence after the operator-gated action. A malformed marker fails closed
during restart classification.

Campaign reason is low-cardinality and suitable for metrics, logs, and marker
fields. Freeform operator text belongs in an optional audit note that is emitted
to evidence/audit logs but is not used as a metric label. Initial reason values
include `evidence_run`, `disk_recovery_drill`, and `operator_requested`.
Reason values are hard-coded in Phase 4 rather than configurable; adding a new
reason requires an explicit code/docs change.

Eviction is not deletion. Retention, legal hold, and future cold-only lifecycle
rules are outside this ADR.

## Required Work Before Local Unlink

Implementation must land restore before unlink. The system must be able to fetch
the uploaded `.blk`, verify size and integrity against committed metadata and the
retained local `.idx`, stage it safely, fsync the file and directory entry, and
atomically publish it before any read path depends on an evicted copy.

Phase 4 implementation order is:

1. split `ConfirmUpload` proto/Raft fields for `.blk` and `.idx` metadata;
2. materialize Confirmed Upload Catalog rows during Raft apply;
3. expose catalog-backed candidate dry-run with no unlink behavior;
4. implement restore-from-Backend using catalog metadata;
5. implement marker/unlink eviction apply;
6. add sampled restore/read validation.

The restore path must verify:

- Backend object keys match ADR 0009;
- the `.blk` size matches committed metadata;
- the retained local `.idx` still resolves the requested Document metadata;
- the staged `.blk` header is valid and matches the Block ID;
- all Frames referenced by the retained local `.idx` pass CRC-32C verification;
- Document SHA-256 verifies before bytes are streamed;
- failed restore leaves either the old local `.blk` or no visible staged files;
- successful restore atomically publishes the staged `.blk`, fsyncs the
  directory, writes and fsyncs the restore marker, then removes the eviction
  marker and fsyncs the directory; the Block then returns to the ordinary hot
  local state;
- repeated restore attempts are idempotent.

The unlink path must be crash-safe:

- do not remove open Blocks;
- do not remove quarantined Blocks through the ordinary eviction path;
- write, fsync, rename, and directory-fsync the eviction marker before removing
  `.blk`;
- remove `.blk` as the local data lifecycle operation with observable failure
  state;
- keep `.idx` local in Phase 4;
- leave Raft, Projection, and Upload Outbox state unchanged;
- on restart, distinguish "evicted locally" from "corrupt/missing unexpectedly";
- treat marker plus `.blk` and `.idx` as a prepared but incomplete eviction that
  can stay hot or retry unlink, and marker plus `.blk` but missing `.idx` as
  unexpected metadata loss that must fail closed.

Startup classifies final `.blk` plus `.idx` plus eviction marker as
`hot_cleanup_needed` after cheap structural checks such as expected filenames,
readable marker JSON, and local file presence. It does not fully verify every
such `.blk` at startup; full corruption detection remains the responsibility of
fail-closed reads and Deep Scrub. The local hot copy is treated as present, and
the stale eviction marker can be removed when safe. `.idx` plus eviction marker
with no `.blk` is `evicted`. If cheap checks fail, startup fails closed into
repair or operator intervention.

`hot_cleanup_needed` markers are cleaned up automatically in the background.
Cleanup is observable and retryable. If cleanup fails, serving may continue from
the present local `.blk`, but health reports cleanup-needed state until the
stale marker is removed.

Reads are allowed while `hot_cleanup_needed` cleanup is pending. The final
`.blk` and retained `.idx` are present, so normal fail-closed read verification
still protects byte integrity. `hot_cleanup_needed` is serving-allowed but
health-degraded until cleanup succeeds.

Startup classification for Phase 4 is:

- eviction marker plus `.idx` present plus `.blk` absent means `evicted`;
- eviction marker plus `.idx` absent means `metadata_loss`, even if `.blk` is
  also absent, because Phase 4 never intentionally evicts `.idx`;
- no eviction marker plus missing `.blk` or `.idx` means `unexpected_loss`;
- quarantine files take precedence over eviction classification.

`metadata_loss` and `unexpected_loss` fail closed for the affected Block and its
Documents, not the entire Shard. Reads or metadata requests that need the
affected Block return `DATA_LOSS`; unrelated Blocks may continue serving. Shard
health reports degraded local lifecycle state until repair or operator
intervention resolves the loss.

Phase 4 read and metadata paths do not automatically restore missing `.idx`
files from the Backend. Missing `.idx` is unexpected metadata loss. Explicit
repair may restore the confirmed Backend `.idx`, verify it, and publish it, but
`HeadDocument`, `FindDocuments`, and `ReadDocument` do not make metadata reads
Backend-dependent.

Deep Scrub skips intentionally evicted `.blk` files in Phase 4 and records an
observable `evicted` skip reason. It must not restore Blocks solely to scrub
them during Phase 4. A missing `.blk` without an eviction marker is not a skip;
it is unexpected local loss and must enter the repair or operator-intervention
path.

If Deep Scrub finds corruption in a hot local `.blk`, ordinary Block Quarantine
and repair still apply. Peer repair cannot assume another follower has a hot
copy after Phase 4 eviction begins: `TransferBlock` must distinguish a locally
evicted peer copy from corruption or missing metadata. If peers are evicted,
unavailable, or unsuitable for repair, the repair path may restore the confirmed
Backend `.blk`, verify it fully against the retained local `.idx`, and publish it
using the same restore rules as `ReadDocument`.

## Observability and Operator Controls

Phase 4 must expose, at minimum:

- candidate-plan evidence: eligible Blocks, eligible bytes, target Member,
  target Shards, prerequisite check outcomes, and skip counts by reason;
- count and bytes of locally evicted Blocks by Shard and Member;
- restore attempts, failures, durations, and Backend error class;
- read failures caused by Backend unavailability for evicted Blocks;
- restore failures caused by confirmed Backend objects that are missing or
  corrupt;
- eviction skips by reason;
- scrub skips caused by intentional local eviction;
- stale eviction marker cleanup attempts and failures;
- post-eviction evidence: markers written, `.blk` files unlinked, bytes freed,
  restart classification result, restore validation result, and read validation
  result for sampled evicted Blocks;
- health detail that separates upload pressure, eviction pressure, restore
  failure, and quarantine.

Dangerous operator commands for forced restore and fault injection belong on the
existing admin HTTP surface and must be unavailable in production unless the
target Cell explicitly enables the relevant non-production or evidence hooks.
Phase 4 eviction is operator-gated, but the operator approves bounded
system-generated candidate plans instead of manually choosing individual Blocks.

The first operator eviction control is a dry-run plus bounded approve campaign.
The system selects eligible follower `.blk` candidates, reports candidate counts,
bytes, target Member/Shards, prerequisite checks, and skip reasons, then
recommends a bounded selection. The operator approves or rejects the system's
recommended plan instead of manually choosing Blocks or precomputing limits. The
implementation still performs per-Block marker/unlink lifecycle operations
internally. Automatic free-space targets and ungated disk-pressure policy
selection are deferred until bounded candidate eviction has passed evidence runs.

Eviction plans are per Member in Phase 4, with an optional Shard filter. A plan
targets one `member_hostname` for operator convenience, resolves it to the
current durable `member_id`, and stores both identities. Apply verifies the same
`member_hostname` still presents the same `member_id`; otherwise the plan is
stale and rejected. Cluster-wide balancing and multi-Member campaigns are
deferred to later automatic policy work.

Dry-run produces a concrete plan token with a plan ID, generated time, expiry
time, selected Block IDs, recommended limits, operator-supplied optional caps,
active configuration values, low-cardinality reason, optional audit note, and
skip reasons. Apply requires the plan ID and applies that selected set rather
than silently recomputing candidates. Apply still revalidates each selected Block
before marker/unlink because leadership, quarantine, repair, open readers, local
files, or configuration may have changed between plan and apply. Expired plans
are rejected.

Plan tokens are in-memory only in Phase 4. If the Member restarts, unapplied
plans disappear and operators must run a fresh dry-run. Applying an unknown,
expired, or restarted-away plan returns `FAILED_PRECONDITION` with a reason such
as `eviction_plan_not_found` or `eviction_plan_expired`.

Apply is idempotent per in-memory plan ID while the plan remains valid. A pending
plan starts when applied. A running plan returns `already_in_progress` or current
status. A completed plan returns the stored final result instead of attempting to
unlink again. After TTL expiry or restart, the plan is unavailable and a fresh
dry-run is required.

Candidate dry-run is available wherever the admin HTTP surface can report local
lifecycle state. Applying an eviction campaign in Phase 4 requires
`SCRAP_EVICTION_ENABLED=true`; the default is `false`. Plan requests may include
optional caps for maximum Blocks and maximum bytes, an optional Shard filter,
and reason/note fields, but the dry-run response must always return a bounded
recommended plan. Apply requests approve that stored bounded plan. Unbounded
eviction campaigns are rejected.

The initial HTTP shape is resource-oriented: `POST /admin/eviction/plans` creates
a dry-run plan, `POST /admin/eviction/plans/{plan_id}/apply` applies that plan,
and `GET /admin/eviction/plans/{plan_id}` reports current or final status while
the in-memory plan remains available. A future gRPC AdminService migration is
outside this ADR.

`scrapctl eviction` is the supported human operator workflow over the admin HTTP
API. It owns plan review, explicit apply, status display, member identity checks,
and evidence formatting. Raw HTTP calls are treated as internal/test automation
surface for Phase 4 rather than the primary human workflow.

`scrapctl eviction apply` requires the plan ID and an explicit confirmation flag.
It does not repeat the plan's limits; the plan already stores recommended max
Blocks, max bytes, target Member, Shard filter, reason, and configuration
snapshot. If the operator wants different limits, they must create a new dry-run
plan with optional caps.

Candidate selection is deterministic in Phase 4: filter by eligibility, sort by
ascending Block ID, then apply the dry-run planner's recommended `max_blocks`
and `max_bytes` bounds. Operator-provided caps may reduce those bounds but do
not need to be supplied. Block IDs are monotonic, so oldest sealed Blocks are
selected first without introducing a new access-tracking subsystem.

Campaign apply continues through bounded per-Block skips or failures, but stops
on systemic failure. A Block that becomes ineligible during apply is recorded as
skipped. A marker or unlink failure is recorded as a Block failure and may
continue within the campaign's failure budget. Prerequisite failures before
apply, repeated filesystem failures, health degradation, or dependency failures
that affect the whole campaign stop the campaign. Final results report selected,
evicted, skipped, failed, and bytes freed.

Plan drift between dry-run and apply is expected and reported explicitly. If a
selected Block is no longer eligible at apply time, apply skips that Block and
continues with the remaining selected Blocks. A campaign where some selected
Blocks are skipped completes as `completed_with_skips`; if all selected Blocks
are skipped without systemic failure, the result is `no_effect`. Drift reasons
are reported per Block.

Evidence campaigns may include an explicit bounded validation step after apply:
sample one or more evicted Blocks, restore each sampled `.blk`, read at least one
Document from it through the normal `ReadDocument` path, confirm the eviction
marker is removed after successful restore, and record the result. Validation is
bounded and sampled so it proves restore/read recovery without restoring every
evicted Block.

Validation sampling defaults from the campaign reason. `evidence_run` campaigns
default to one sampled restore/read validation, subject to the configured sample
cap. Other reasons default to zero sampled validations because immediate restore
consumes some of the space just freed. Operators may override the sample count
explicitly up to the configured cap.

Validation sampling is deterministic. If validation is requested, the system
validates the first N successfully evicted Blocks in campaign selection order.
Because Phase 4 selection is oldest-first by Block ID, validation evidence is
reproducible.

For each sampled Block, validation reads the first Document recorded in the
retained local `.idx`. This avoids extra operator input and proves the retained
index, restored `.blk`, Projection Resolution, and ordinary `ReadDocument` path
work together.

Sampled validation leaves successfully restored Blocks in the ordinary hot local
state. It does not re-evict them after the validation read. The configured sample
cap bounds the disk impact, and a later campaign may evict the Block again if it
still qualifies.

Validation failure is reported separately from apply failure. If marker/unlink
work succeeds but sampled restore/read validation fails, the campaign result is
`evicted_with_validation_failure`. Successful apply with skips is
`completed_with_skips`; systemic apply failure is `failed`.

## Consequences

Positive:

- Phase 4 can reduce follower disk usage without weakening the write ACK
  contract.
- Backend restore becomes testable before Phase 5 depends on Backend-only reads.
- The leader hot-read path remains simple while eviction behavior is introduced.

Negative:

- A newly elected leader may need Backend restore before it can serve older
  Documents whose local `.blk` copy was evicted.
- Backend retry and admission behavior becomes more important because reads can
  depend on restored Blocks after local eviction.
- Operator and telemetry surfaces must grow before the storage code can safely
  remove local data files.

## Alternatives Considered

### Evict any local copy after upload confirmation

Rejected for Phase 4. This collapses Phase 4 into Phase 5 by making every read
potentially Backend-only. It also removes the simple leader-local hot-read path
before restore behavior is proven.

### Keep eviction state in Raft

Rejected for the first Phase 4 boundary. Eviction is a per-Member cache/lifecycle
fact. Raft should not be polluted with local file-presence state unless a later
design needs cross-member placement accounting.

### Add an S3-compatible cold-read or redirect API

Rejected. S.C.R.A.P. is a gRPC Document gateway with all-or-error reads. Redirect
or S3-shaped reads would bypass checksum and authority semantics unless designed
as a separate future API.

### Stream cold bytes directly from the Backend

Rejected for Phase 4. A direct cold stream would create a second read path and
force checksum, range, failure, and partial-response behavior to be re-derived
while the existing Block reader already owns those invariants. Phase 4 restores
the full Block first, then reads through the normal local path.

### Implement encryption together with eviction

Rejected. Encryption is a long-lived envelope and key-management contract. It
should be decided in its own ADR and not coupled to the first local-eviction
mechanism.

## Success Criteria

Phase 4 is ready to implement when:

- ADR 0012 evidence remains current and passing;
- restore-from-Backend tests exist before eviction tests;
- read behavior for an evicted Block is all-or-error;
- operator health explains why a Block is present, evicted, restoring,
  quarantined, or unavailable;
- no API-visible Document identity or metadata semantics change.
