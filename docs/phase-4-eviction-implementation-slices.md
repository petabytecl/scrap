# Phase 4 eviction implementation slices

Status: Published

Source: ADR 0016

## Purpose

Break Phase 4 partial local eviction into independently reviewable slices. These
are published GitHub issues under the `storage-gateway-v2` milestone.

## Published Issues

| Slice | Issue |
| ----- | ----- |
| 1. Split upload confirmation metadata and catalog confirmed uploads | #372 |
| 2. Add eviction configuration and local lifecycle classification | #373 |
| 3. Expose catalog-backed eviction dry-run plans | #374 |
| 4. Implement Backend `.blk` restore before eviction | #375 |
| 5. Keep metadata reads local and make loss states fail closed | #376 |
| 6. Apply bounded eviction campaigns with crash-safe marker/unlink | #377 |
| 7. Add sampled restore/read validation for evidence campaigns | #378 |
| 8. Teach scrub and repair about evicted peers and Backend fallback | #379 |
| 9. Wire Phase 4 evidence, health, and operator output | #380 |

## Proposed Slices

### 1. Split upload confirmation metadata and catalog confirmed uploads (#372)

Type: AFK

Blocked by: None

What to build:
Hard-cut the V2 `ConfirmUpload` contract so committed upload confirmation carries
separate `.blk` and `.idx` Backend metadata. Materialize a rebuildable Confirmed
Upload Catalog from Raft apply so later restore and eviction planning can use
committed Backend metadata without Backend listing.

Acceptance criteria:

- `ConfirmUpload` carries separate `.blk` and `.idx` validation tokens and sizes.
- Upload confirmation apply writes Confirmed Upload Catalog rows and clears the
  Upload Outbox.
- Catalog rebuild fails closed when matching sealed Block metadata is missing.
- Existing local development data may be hard-cut; no compatibility migration is
  required.

### 2. Add eviction configuration and local lifecycle classification (#373)

Type: AFK

Blocked by: Slice 1

What to build:
Add Phase 4 eviction configuration and local lifecycle classification without
unlinking data. Classify Blocks as hot, evicted, hot-cleanup-needed,
metadata-loss, or unexpected-loss from local `.blk`, `.idx`, eviction marker, and
restore marker state.

Acceptance criteria:

- Startup validates `SCRAP_EVICTION_*` configuration and fails on invalid values.
- Eviction and restore markers are versioned JSON and fail closed when malformed.
- `hot_cleanup_needed` is serving-allowed, health-degraded, and cleaned up
  automatically in the background.
- Missing `.idx` is classified as metadata loss, not a normal eviction.

### 3. Expose catalog-backed eviction dry-run plans (#374)

Type: AFK

Blocked by: Slices 1, 2

What to build:
Expose a dry-run-only eviction planner through the admin HTTP surface and
`scrapctl eviction plan`. The planner computes `evictable` from the Confirmed
Upload Catalog, local lifecycle state, leadership state, open readers, repair
state, and hot residency window.

Acceptance criteria:

- `POST /admin/eviction/plans` creates an in-memory plan token with TTL.
- `scrapctl eviction plan` prints selected Blocks, recommended bounds, skipped
  candidates, active config, and evidence fields.
- Plans target `member_hostname`, resolve to `member_id`, and store both.
- Plan requests may narrow recommended bounds but cannot expand past configured
  ceilings.
- No `.blk` unlink occurs in this slice.

### 4. Implement Backend `.blk` restore before eviction (#375)

Type: AFK

Blocked by: Slices 1, 2

What to build:
Implement Shard-owned, per-Block singleflight restore of evicted `.blk` files
from Backend using Confirmed Upload Catalog metadata and retained local `.idx`
files. Restore publishes only fully verified `.blk` files and updates restore
lifecycle metadata.

Acceptance criteria:

- Restore downloads to staging, verifies size, Block header, all indexed Frame
  CRC-32C values, and Document SHA-256 before serving.
- Concurrent reads for the same evicted Block join one restore operation.
- Transient Backend dependency failures map to `UNAVAILABLE` with a restore
  reason; confirmed missing/corrupt Backend objects map to `DATA_LOSS`.
- Successful restore writes restore metadata, removes the eviction marker, and
  leaves the Block hot/local.

### 5. Keep metadata reads local and make loss states fail closed (#376)

Type: AFK

Blocked by: Slices 2, 4

What to build:
Integrate Phase 4 lifecycle classification with read paths so metadata-only
reads stay local, `.blk` restore is triggered only by `ReadDocument`, and loss
states fail closed for affected Blocks without making the whole Shard
unavailable.

Acceptance criteria:

- `HeadDocument` and `FindDocuments` never restore `.blk` or `.idx`.
- `ReadDocument` restores `.blk` only when `.idx` is retained and local state is
  intentionally evicted.
- `metadata_loss` and `unexpected_loss` return `DATA_LOSS` for affected
  Documents while unrelated Blocks continue serving.
- Missing `.idx` repair is explicit repair behavior, not automatic metadata-read
  behavior.

### 6. Apply bounded eviction campaigns with crash-safe marker/unlink (#377)

Type: AFK

Blocked by: Slices 3, 4, 5

What to build:
Enable `SCRAP_EVICTION_ENABLED` apply campaigns through admin HTTP and
`scrapctl eviction apply`. Apply uses the stored dry-run plan, revalidates each
selected Block, writes the eviction marker before unlink, removes only `.blk`,
and reports per-Block drift, skips, failures, and bytes freed.

Acceptance criteria:

- `POST /admin/eviction/plans/{plan_id}/apply` requires a valid in-memory plan.
- `scrapctl eviction apply --plan-id <id> --confirm` applies the stored plan
  without redefining limits.
- Apply is idempotent per plan ID while the plan remains valid.
- Partial drift produces `completed_with_skips` or `no_effect`; systemic failure
  stops the campaign.
- Crash tests cover marker-before-unlink and restart classification.

### 7. Add sampled restore/read validation for evidence campaigns (#378)

Type: AFK

Blocked by: Slice 6

What to build:
Add bounded sampled validation after eviction apply. For `evidence_run`
campaigns, restore and read the first Document from the first successfully
evicted Block by default, subject to the validation sample cap.

Acceptance criteria:

- `evidence_run` defaults to one validation sample; other reasons default to
  zero.
- Validation picks the first N successfully evicted Blocks in selection order.
- Validation reads the first Document in the retained `.idx`.
- Successful validation leaves sampled Blocks restored and hot/local.
- Validation failure reports `evicted_with_validation_failure`.

### 8. Teach scrub and repair about evicted peers and Backend fallback (#379)

Type: AFK

Blocked by: Slices 2, 4

What to build:
Update Deep Scrub and repair so intentionally evicted `.blk` files are skipped
with observable reason, corrupt hot Blocks can still be quarantined, and peer
repair can distinguish a locally evicted peer from corruption or metadata loss.
When peers cannot supply a hot copy, repair may restore from confirmed Backend
metadata.

Acceptance criteria:

- Deep Scrub records `evicted` skips and does not restore solely to scrub.
- `TransferBlock` distinguishes locally evicted copies from corrupt/missing
  Blocks.
- Quarantine/repair can restore confirmed Backend `.blk` when peers are evicted
  or unsuitable.
- Backend repair uses the same verification and publish rules as read restore.

### 9. Wire Phase 4 evidence, health, and operator output (#380)

Type: AFK

Blocked by: Slices 3, 6, 7, 8

What to build:
Expose the operator and telemetry evidence required by ADR 0016: candidate-plan
evidence, eviction/restore counters and durations, cleanup-needed state, skip
reasons, validation outcomes, and health detail that separates upload pressure,
eviction pressure, restore failure, quarantine, metadata loss, and cleanup
needed.

Acceptance criteria:

- Admin health reports evicted counts, cleanup-needed state, metadata loss, and
  restore failures separately.
- Metrics use low-cardinality reason values and never use audit notes as labels.
- `scrapctl eviction status` presents final campaign results and evidence.
- Evidence bundles include candidate plan, apply result, validation result, and
  relevant health/metric snapshots.

## Suggested Dependency Order

1. Slice 1
2. Slice 2
3. Slice 3 and Slice 4 in parallel
4. Slice 5
5. Slice 6
6. Slice 7 and Slice 8 in parallel
7. Slice 9

## Review Notes

- Slice 5 stays separate so metadata-read behavior and loss-state mapping can be
  verified independently from restore mechanics.
- Slice 8 runs after restore is available; read restore is the safety
  prerequisite for unlink, while scrub repair fallback can proceed in parallel
  with validation work.
- All published slices are AFK-ready and labelled `ready-for-agent`.
