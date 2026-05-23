# Storage Gateway Operator Runbooks

Status: production-readiness contract for GitHub issue `#47`
Last updated: 2026-05-23

These runbooks define the operator workflows required before S.C.R.A.P. accepts
production traffic. They use the admin API through `scrapctl` and durable
operation records. They do not authorize ad hoc storage mutation scripts.

Deployment automation may create pods, PersistentVolumes, network policy, and
service objects. Once a S.C.R.A.P. member is running, storage-state changes,
dangerous maintenance, recovery, repair, and evidence collection must flow
through admin RPCs or `scrapctl`.

## Common Requirements

Set these values before running any command:

- `ADMIN_ADDR`: admin gRPC address for the target cell.
- `SOURCE_ADMIN_ADDR`: source-cell admin gRPC address for DR drills.
- `TARGET_ADMIN_ADDR`: fresh target-cell admin gRPC address for DR drills.
- `CELL_ID`: target S.C.R.A.P. cell.
- `PROFILE_ID`: approved deployment or capacity profile.
- `MEMBER_ID`: target storage member when a member is involved.
- `OPERATION_ID`: approved UUIDv7 operation ID from the change, incident, or
  drill record.

Use real values in the commands below. Do not use high-cardinality document,
block, operation, or backend-object identifiers as metric labels; use them only
in `scrapctl` targets, logs, traces, audit evidence, and durable operation
records.

Common inspection commands:

```sh
scrapctl --admin-addr "$ADMIN_ADDR" inspect summary
scrapctl --admin-addr "$ADMIN_ADDR" inspect capacity-runway --capacity-profile-id "$PROFILE_ID"
scrapctl --admin-addr "$ADMIN_ADDR" inspect recovery-readiness
scrapctl --admin-addr "$ADMIN_ADDR" operations list --state queued --state running
```

Common plan/start/watch workflow:

```sh
scrapctl --admin-addr "$ADMIN_ADDR" plan <operation> <operation flags> --dry-run
scrapctl --admin-addr "$ADMIN_ADDR" plan <operation> <operation flags> --metadata change_id="$CHANGE_ID"
scrapctl --admin-addr "$ADMIN_ADDR" start <operation> --plan-id "$PLAN_ID" --plan-hash "$PLAN_HASH" --operation-id "$OPERATION_ID"
scrapctl --admin-addr "$ADMIN_ADDR" watch --operation-id "$OPERATION_ID"
scrapctl --admin-addr "$ADMIN_ADDR" status --operation-id "$OPERATION_ID"
```

Expected operation states are `PLANNED`, `QUEUED`, `RUNNING`, `SUCCEEDED`,
`FAILED`, `CANCELED`, or `EXPIRED`. A plan expires after the admin operation
plan TTL; re-plan instead of starting an expired plan.

Abort behavior:

```sh
scrapctl --admin-addr "$ADMIN_ADDR" operations cancel --operation-id "$OPERATION_ID"
```

Canceling records an `operation_canceled` audit event. Some operations may
finish or reach a safe terminal state before cancellation is observed; always
check final status and dashboard state before retrying.

Required dashboard references are defined in
[Storage Gateway Dashboard And Alert Contract](storage-gateway-dashboard-alert-contract.md).
Required audit evidence includes `operation_started`, `operation_canceled`,
`member_cordoned`, and `member_uncordoned` where applicable, plus critical
action audit events for restore, repair, scrub, key rotation, capacity
override, and DR workflows.

## Bootstrap A Cell

Use when creating a new cell or validating that a freshly deployed non-traffic
cell is safe to promote.

Prerequisites:

- release evidence approved for the target image digest;
- production write ACK mode still disabled until all readiness gates pass;
- authorization policy loaded for public and admin APIs;
- dashboard and alert contract deployed for the target profile;
- initial storage members and PVs created by deployment automation.

Commands:

```sh
scrapctl --admin-addr "$ADMIN_ADDR" inspect summary
scrapctl --admin-addr "$ADMIN_ADDR" inspect capacity-runway --capacity-profile-id "$PROFILE_ID"
scrapctl --admin-addr "$ADMIN_ADDR" inspect recovery-readiness
scrapctl --admin-addr "$ADMIN_ADDR" operations list --state queued --state running
```

Expected status:

- storage member count and shard count match the bootstrap plan;
- capacity runway has no production-blocking warnings;
- recovery readiness reports the latest restorable checkpoint when configured;
- no unexpected queued or running dangerous operations exist;
- dashboards show healthy write admission, disk runway, Raft health, OpenBao
  health, backend lag, and operation backlog.

Rollback or abort:

- do not shift production traffic to the cell;
- keep production write ACK mode disabled;
- stop deployment automation for additional members;
- preserve bootstrap logs, dashboard snapshots, and audit evidence for review.

Escalation signals:

- `SCRAPRaftQuorumUnavailable`;
- `SCRAPDiskRunwayCritical`;
- `SCRAPOpenBaoUnavailable`;
- unexpected operation backlog or missing audit evidence.

## Scale Out Storage Members

Use when adding storage members or capacity to an existing cell.

Prerequisites:

- capacity profile and placement policy approved for the new member count;
- new member has a distinct eligible storage node or failure domain;
- dashboards show no active durability-risk alert;
- change record includes member IDs and rollback point.

Commands:

```sh
scrapctl --admin-addr "$ADMIN_ADDR" inspect summary
scrapctl --admin-addr "$ADMIN_ADDR" inspect member --member-id "$MEMBER_ID"
scrapctl --admin-addr "$ADMIN_ADDR" member eviction-safety --member-id "$MEMBER_ID"
scrapctl --admin-addr "$ADMIN_ADDR" inspect repair-queue --page-size 50
scrapctl --admin-addr "$ADMIN_ADDR" member uncordon --member-id "$MEMBER_ID" --operation-id "$OPERATION_ID"
```

Expected status:

- new member is `ONLINE`;
- placement dashboard shows healthy distinct storage-node placement;
- peer catch-up lag drains before the member becomes byte-serving eligible;
- `member_uncordoned` audit evidence exists for production serving changes.

Rollback or abort:

```sh
scrapctl --admin-addr "$ADMIN_ADDR" member cordon --member-id "$MEMBER_ID" --reason "scale-out rollback" --operation-id "$OPERATION_ID"
scrapctl --admin-addr "$ADMIN_ADDR" plan drain --member-id "$MEMBER_ID" --dry-run
```

Escalation signals:

- `SCRAPPlacementUnhealthy`;
- peer catch-up lag rising instead of draining;
- repair queue age increasing after the member joins.

## Planned Drain

Use for maintenance that intentionally removes a healthy member from serving.

Prerequisites:

- no active quorum, placement, corruption, or disk runway alerts;
- drain target is not required for current quorum or byte-serving safety;
- change window and rollback owner are assigned.

Commands:

```sh
scrapctl --admin-addr "$ADMIN_ADDR" member cordon --member-id "$MEMBER_ID" --reason "planned drain" --operation-id "$OPERATION_ID"
scrapctl --admin-addr "$ADMIN_ADDR" member eviction-safety --member-id "$MEMBER_ID"
scrapctl --admin-addr "$ADMIN_ADDR" plan drain --member-id "$MEMBER_ID" --dry-run --metadata change_id="$CHANGE_ID"
scrapctl --admin-addr "$ADMIN_ADDR" plan drain --member-id "$MEMBER_ID" --metadata change_id="$CHANGE_ID"
scrapctl --admin-addr "$ADMIN_ADDR" start drain --plan-id "$PLAN_ID" --plan-hash "$PLAN_HASH" --operation-id "$OPERATION_ID"
scrapctl --admin-addr "$ADMIN_ADDR" watch --operation-id "$OPERATION_ID"
```

Expected status:

- member is cordoned before the drain plan starts;
- eviction safety returns safe or gives explicit warnings accepted by the
  change owner;
- drain operation reaches `SUCCEEDED`;
- Raft quorum, backend lag, repair lag, and disk runway remain healthy.

Rollback or abort:

```sh
scrapctl --admin-addr "$ADMIN_ADDR" operations cancel --operation-id "$OPERATION_ID"
scrapctl --admin-addr "$ADMIN_ADDR" member uncordon --member-id "$MEMBER_ID" --operation-id "$OPERATION_ID"
```

Escalation signals:

- `SCRAPRaftQuorumUnavailable`;
- `SCRAPPlacementUnhealthy`;
- `SCRAPBackendLagCritical`;
- drain operation stuck or failed.

## Lost Node Or Lost Persistent Volume

Use when a storage node or PV is unavailable, suspect, or permanently lost.

Prerequisites:

- incident owner assigned;
- affected member, shard, and block evidence collected through `scrapctl`;
- support bundle excludes document bytes and secrets;
- no manual deletion of metadata, blocks, or backend objects.

Commands:

```sh
scrapctl --admin-addr "$ADMIN_ADDR" inspect member --member-id "$MEMBER_ID"
scrapctl --admin-addr "$ADMIN_ADDR" member cordon --member-id "$MEMBER_ID" --reason "lost node or PV" --operation-id "$OPERATION_ID"
scrapctl --admin-addr "$ADMIN_ADDR" inspect repair-queue --page-size 100
scrapctl --admin-addr "$ADMIN_ADDR" plan repair --target member:"$MEMBER_ID" --dry-run --metadata incident_id="$INCIDENT_ID"
scrapctl --admin-addr "$ADMIN_ADDR" plan repair --target member:"$MEMBER_ID" --metadata incident_id="$INCIDENT_ID"
scrapctl --admin-addr "$ADMIN_ADDR" start repair --plan-id "$PLAN_ID" --plan-hash "$PLAN_HASH" --operation-id "$OPERATION_ID"
scrapctl --admin-addr "$ADMIN_ADDR" watch --operation-id "$OPERATION_ID"
```

Expected status:

- member remains cordoned while suspect;
- repair operation uses verified peer or backend sources only;
- repair queue age decreases;
- corruption and integrity failure dashboards do not increase.

Rollback or abort:

- cancel the repair if it is still safe to do so;
- keep the member cordoned until byte verification and placement checks pass;
- re-plan repair from the latest queue state before retrying.

Escalation signals:

- all-sources-corrupt evidence;
- `SCRAPCorruptionIncidentCritical`;
- `SCRAPPlacementUnhealthy`;
- no verified source available for repair.

## Rolling Upgrade

Use for canary and progressive member upgrades.

Prerequisites:

- target image digest approved by release evidence;
- mixed-version compatibility evidence accepted;
- rollback or roll-forward decision documented for active formats;
- dashboards and alerts healthy before the first canary.

Commands for each member cohort:

```sh
scrapctl --admin-addr "$ADMIN_ADDR" inspect summary
scrapctl --admin-addr "$ADMIN_ADDR" member cordon --member-id "$MEMBER_ID" --reason "rolling upgrade" --operation-id "$OPERATION_ID"
scrapctl --admin-addr "$ADMIN_ADDR" member eviction-safety --member-id "$MEMBER_ID"
scrapctl --admin-addr "$ADMIN_ADDR" plan drain --member-id "$MEMBER_ID" --dry-run --metadata release_sha="$RELEASE_SHA"
scrapctl --admin-addr "$ADMIN_ADDR" plan drain --member-id "$MEMBER_ID" --metadata release_sha="$RELEASE_SHA"
scrapctl --admin-addr "$ADMIN_ADDR" start drain --plan-id "$PLAN_ID" --plan-hash "$PLAN_HASH" --operation-id "$OPERATION_ID"
scrapctl --admin-addr "$ADMIN_ADDR" watch --operation-id "$OPERATION_ID"
scrapctl --admin-addr "$ADMIN_ADDR" inspect member --member-id "$MEMBER_ID"
scrapctl --admin-addr "$ADMIN_ADDR" member uncordon --member-id "$MEMBER_ID" --operation-id "$OPERATION_ID"
```

Expected status:

- one bounded member or shard cohort changes at a time;
- quorum and byte-serving eligibility stay healthy;
- no format writer is enabled until the consensus-owned feature gate is
  committed and all required readers are compatible;
- rollout audit evidence records image digest, config generation, member
  cohort, and operator approval.

Rollback or abort:

- pause the deployment automation;
- cancel the active drain if safe;
- roll back by digest only when the previous binary can read all active formats;
- roll forward when a writer has committed a format older binaries cannot read.

Escalation signals:

- `SCRAPRaftQuorumUnavailable`;
- `SCRAPBackendLagCritical`;
- `SCRAPOpenBaoUnavailable`;
- corruption incidents after canary.

## Disk Pressure

Use when local disk runway is below warning or critical threshold.

Prerequisites:

- capacity owner and incident owner assigned;
- current capacity profile and runway evidence available;
- no unapproved deletion or backend object mutation.

Commands:

```sh
scrapctl --admin-addr "$ADMIN_ADDR" inspect capacity-runway --capacity-profile-id "$PROFILE_ID"
scrapctl --admin-addr "$ADMIN_ADDR" operations list --state queued --state running
scrapctl --admin-addr "$ADMIN_ADDR" inspect repair-queue --page-size 100
scrapctl --admin-addr "$ADMIN_ADDR" plan capacity-override --capacity-profile-id "$PROFILE_ID" --expires-at "$EXPIRES_AT" --reason "$REASON" --dry-run
scrapctl --admin-addr "$ADMIN_ADDR" plan capacity-override --capacity-profile-id "$PROFILE_ID" --expires-at "$EXPIRES_AT" --reason "$REASON"
scrapctl --admin-addr "$ADMIN_ADDR" start capacity-override --plan-id "$PLAN_ID" --plan-hash "$PLAN_HASH" --operation-id "$OPERATION_ID"
scrapctl --admin-addr "$ADMIN_ADDR" watch --operation-id "$OPERATION_ID"
```

Expected status:

- runway trend stabilizes or improves;
- write admission stays in normal or controlled reserve state;
- backend upload and repair lanes continue draining;
- capacity override is time-bound and audited.

Rollback or abort:

- cancel the override before it starts when it is no longer needed;
- let the override expire instead of extending silently;
- re-plan with a new owner-approved expiry when more time is required.

Escalation signals:

- `SCRAPDiskRunwayCritical`;
- write admission blocked;
- backend lag consuming the local durability window.

## Readiness And Byte-Serving Debugging

Use when a member is online but readiness, replacement, or byte-serving
eligibility is unclear.

Prerequisites:

- request or incident has an owner;
- target member, shard, or block identifiers came from logs, traces, audit
  records, or operation state, not metric labels.

Commands:

```sh
scrapctl --admin-addr "$ADMIN_ADDR" inspect summary
scrapctl --admin-addr "$ADMIN_ADDR" inspect member --member-id "$MEMBER_ID"
scrapctl --admin-addr "$ADMIN_ADDR" member eviction-safety --member-id "$MEMBER_ID"
scrapctl --admin-addr "$ADMIN_ADDR" inspect shard --shard-id "$SHARD_ID"
scrapctl --admin-addr "$ADMIN_ADDR" inspect block --shard-id "$SHARD_ID" --block-id "$BLOCK_ID"
scrapctl --admin-addr "$ADMIN_ADDR" inspect repair-queue --shard-id "$SHARD_ID" --page-size 100
scrapctl --admin-addr "$ADMIN_ADDR" operations list --state queued --state running
```

Expected status:

- member state, cordon state, shard leader, and voter set are explicit;
- byte-serving eligibility is explained by repair, placement, or catch-up
  state;
- no stale leader or unsafe placement state is hidden behind pod readiness.

Rollback or abort:

- keep suspect members cordoned;
- do not force uncordon until eviction safety and byte verification are clean;
- open a repair or drain plan instead of editing local state.

Escalation signals:

- `SCRAPPlacementUnhealthy`;
- peer catch-up lag above threshold;
- Raft commit/apply lag or ReadIndex failures.

## Corruption Incident

Use when checksum mismatch, quarantine, all-sources-corrupt, or integrity
failure evidence appears.

Prerequisites:

- incident owner, support owner, and operations owner assigned;
- affected document, transaction, block, shard, or member targets collected from
  logs, traces, audit events, or durable operation state;
- support bundle is metadata-only and excludes document bytes and secrets.

Commands:

```sh
scrapctl --admin-addr "$ADMIN_ADDR" inspect repair-queue --page-size 100
scrapctl --admin-addr "$ADMIN_ADDR" inspect block --shard-id "$SHARD_ID" --block-id "$BLOCK_ID"
scrapctl --admin-addr "$ADMIN_ADDR" plan scrub --target block:"$SHARD_ID/$BLOCK_ID" --dry-run --metadata incident_id="$INCIDENT_ID"
scrapctl --admin-addr "$ADMIN_ADDR" plan repair --target block:"$SHARD_ID/$BLOCK_ID" --dry-run --metadata incident_id="$INCIDENT_ID"
scrapctl --admin-addr "$ADMIN_ADDR" plan repair --target block:"$SHARD_ID/$BLOCK_ID" --metadata incident_id="$INCIDENT_ID"
scrapctl --admin-addr "$ADMIN_ADDR" start repair --plan-id "$PLAN_ID" --plan-hash "$PLAN_HASH" --operation-id "$OPERATION_ID"
scrapctl --admin-addr "$ADMIN_ADDR" watch --operation-id "$OPERATION_ID"
```

Expected status:

- suspect source is quarantined from serving;
- reads fail closed instead of streaming corrupt bytes;
- repair uses only verified local, peer, or backend sources;
- audit evidence links the repair operation and sanitized incident metadata.

Rollback or abort:

- cancel repair only before verified copy installation begins;
- keep suspect sources quarantined;
- re-plan from current repair queue state if the first plan fails or expires.

Escalation signals:

- `SCRAPCorruptionIncidentCritical`;
- all-sources-corrupt evidence;
- repair blocked because no verified source remains.

## Restore Or Prewarm

Use when data is cold, restore-pending, crypto-unavailable after recovery, or
needs planned prewarm.

Prerequisites:

- target document, transaction, or block identified from metadata;
- backend and OpenBao health are known;
- restore owner accepts any archive restore budget impact.

Commands:

```sh
scrapctl --admin-addr "$ADMIN_ADDR" inspect document --tenant-id "$TENANT_ID" --transaction-id "$TRANSACTION_ID" --document-name "$DOCUMENT_NAME"
scrapctl --admin-addr "$ADMIN_ADDR" plan restore --target document:"$TENANT_ID/$TRANSACTION_ID/$DOCUMENT_NAME" --dry-run --metadata incident_id="$INCIDENT_ID"
scrapctl --admin-addr "$ADMIN_ADDR" plan restore --target document:"$TENANT_ID/$TRANSACTION_ID/$DOCUMENT_NAME" --metadata incident_id="$INCIDENT_ID"
scrapctl --admin-addr "$ADMIN_ADDR" start restore --plan-id "$PLAN_ID" --plan-hash "$PLAN_HASH" --operation-id "$OPERATION_ID"
scrapctl --admin-addr "$ADMIN_ADDR" watch --operation-id "$OPERATION_ID"
```

For planned prewarm:

```sh
scrapctl --admin-addr "$ADMIN_ADDR" plan prewarm --target transaction:"$TENANT_ID/$TRANSACTION_ID" --pin-until "$PIN_UNTIL" --dry-run
scrapctl --admin-addr "$ADMIN_ADDR" plan prewarm --target transaction:"$TENANT_ID/$TRANSACTION_ID" --pin-until "$PIN_UNTIL"
scrapctl --admin-addr "$ADMIN_ADDR" start prewarm --plan-id "$PLAN_ID" --plan-hash "$PLAN_HASH" --operation-id "$OPERATION_ID"
```

Expected status:

- restore or prewarm operation reaches `SUCCEEDED`;
- restore-pending response rate returns to baseline;
- backend verification and OpenBao signals remain healthy;
- restored bytes are checksum-verified before serving.

Rollback or abort:

- cancel the queued operation when it is no longer needed;
- do not delete backend artifacts to stop a restore;
- let restored data fall back through lifecycle policy instead of ad hoc
  cleanup.

Escalation signals:

- `SCRAPBackendLagCritical`;
- `SCRAPOpenBaoUnavailable`;
- restore queue oldest age above threshold.

## Backend Outage

Use when object backend upload, verify, restore, or prewarm work is failing or
delayed.

Prerequisites:

- backend provider incident or maintenance context known;
- local disk runway and write admission dashboards open;
- capacity owner assigned when local durability window is at risk.

Commands:

```sh
scrapctl --admin-addr "$ADMIN_ADDR" inspect capacity-runway --capacity-profile-id "$PROFILE_ID"
scrapctl --admin-addr "$ADMIN_ADDR" operations list --state queued --state running
scrapctl --admin-addr "$ADMIN_ADDR" inspect repair-queue --page-size 100
scrapctl --admin-addr "$ADMIN_ADDR" plan capacity-override --capacity-profile-id "$PROFILE_ID" --expires-at "$EXPIRES_AT" --reason "$REASON" --dry-run
```

Expected status:

- backend backlog and oldest upload age are visible;
- local durability window remains above the target profile threshold;
- writes throttle or reject before disk runway becomes unsafe;
- restore/prewarm requests return typed pending or unavailable states.

Rollback or abort:

- do not manually mark backend uploads complete;
- avoid capacity override unless owner-approved and time-bound;
- cancel only operator-started restore, prewarm, or repair operations that are
  safe to retry after the backend recovers.

Escalation signals:

- `SCRAPBackendLagCritical`;
- `SCRAPDiskRunwayCritical`;
- restore-pending response spike;
- provider error class changes from transient to permanent.

## OpenBao Outage

Use when OpenBao Transit, key lookup, audit-device health, unwrap, rewrap, or
crypto-unavailable signals are unhealthy.

Prerequisites:

- security owner and operations owner assigned;
- no plaintext DEKs, wrapped DEK blobs, OpenBao tokens, or secrets collected in
  support evidence;
- OpenBao restore or unseal actions follow the security-owned OpenBao runbook.

Commands:

```sh
scrapctl --admin-addr "$ADMIN_ADDR" operations list --state queued --state running --type rewrap
scrapctl --admin-addr "$ADMIN_ADDR" plan key-rotation --target block:"$SHARD_ID/$BLOCK_ID" --destination-key-id "$DESTINATION_KEY_ID" --dry-run --metadata incident_id="$INCIDENT_ID"
scrapctl --admin-addr "$ADMIN_ADDR" plan key-rotation --target block:"$SHARD_ID/$BLOCK_ID" --destination-key-id "$DESTINATION_KEY_ID" --metadata incident_id="$INCIDENT_ID"
scrapctl --admin-addr "$ADMIN_ADDR" start key-rotation --plan-id "$PLAN_ID" --plan-hash "$PLAN_HASH" --operation-id "$OPERATION_ID"
scrapctl --admin-addr "$ADMIN_ADDR" watch --operation-id "$OPERATION_ID"
```

Expected status:

- encrypted reads and restores fail closed with crypto-unavailable detail while
  OpenBao is unavailable;
- no secret material appears in logs, audit events, metrics, or support bundles;
- rewrap/key-rotation operations resume or are re-planned after OpenBao
  recovers;
- OpenBao audit-device health is visible.

Rollback or abort:

- cancel queued rewrap/key-rotation operations when they are unsafe to continue;
- do not lower minimum key versions without security owner approval;
- re-plan with a fresh destination key after security validates OpenBao state.

Escalation signals:

- `SCRAPOpenBaoUnavailable`;
- crypto-unavailable response spike;
- OpenBao audit-device unhealthy;
- key-version lookup failures.

## DR Rebuild Drill

Use to execute a fresh-cluster disaster-recovery drill without making an
unapproved formal RTO/RPO promise.

Prerequisites:

- drill owner, operations owner, security owner, and product observer assigned;
- fresh target cluster created without joining the source cell's shard
  consensus;
- source and target profile, image digest, backend profile, and OpenBao profile
  recorded;
- latest restorable checkpoint and snapshot target identified;
- dashboard and alert contract active for the drill target.

Commands:

```sh
scrapctl --admin-addr "$SOURCE_ADMIN_ADDR" inspect recovery-readiness
scrapctl --admin-addr "$TARGET_ADMIN_ADDR" plan recovery --target snapshot:"$SNAPSHOT_ID" --dry-run --metadata drill_id="$DRILL_ID"
scrapctl --admin-addr "$TARGET_ADMIN_ADDR" plan recovery --target snapshot:"$SNAPSHOT_ID" --metadata drill_id="$DRILL_ID"
scrapctl --admin-addr "$TARGET_ADMIN_ADDR" start metadata-restore --plan-id "$PLAN_ID" --plan-hash "$PLAN_HASH" --operation-id "$METADATA_RESTORE_OPERATION_ID"
scrapctl --admin-addr "$TARGET_ADMIN_ADDR" watch --operation-id "$METADATA_RESTORE_OPERATION_ID"
scrapctl --admin-addr "$TARGET_ADMIN_ADDR" start copy-verify --plan-id "$PLAN_ID" --plan-hash "$PLAN_HASH" --operation-id "$COPY_VERIFY_OPERATION_ID"
scrapctl --admin-addr "$TARGET_ADMIN_ADDR" watch --operation-id "$COPY_VERIFY_OPERATION_ID"
scrapctl --admin-addr "$TARGET_ADMIN_ADDR" start dr-drill --plan-id "$PLAN_ID" --plan-hash "$PLAN_HASH" --operation-id "$DR_DRILL_OPERATION_ID"
scrapctl --admin-addr "$TARGET_ADMIN_ADDR" watch --operation-id "$DR_DRILL_OPERATION_ID"
```

Expected status:

- metadata restore imports published metadata without treating local projections
  as authority;
- copy verification reads backend artifacts and fails closed on checksum or
  envelope mismatch;
- DR drill operation records measured recovery evidence, warnings, and terminal
  state;
- audit evidence links metadata restore, copy verification, and drill operation
  IDs;
- dashboard snapshots show restore lag, backend lag, OpenBao health, corruption
  incidents, operation backlog, and capacity runway for the drill target.

Rollback or abort:

- cancel queued drill operations if the fresh cluster is misconfigured;
- preserve failed evidence instead of rerunning without a failure record;
- tear down the drill cluster only after operation status, audit evidence, and
  dashboard snapshots are retained.

Escalation signals:

- missing or corrupt recovery artifacts;
- `SCRAPBackendLagCritical`;
- `SCRAPOpenBaoUnavailable`;
- `SCRAPCorruptionIncidentCritical`;
- operation failure or stuck state during metadata restore or copy verification.

## Evidence Checklist

For every runbook execution, retain:

- change, incident, or drill ID;
- operator identity and authorization policy generation;
- `scrapctl` command transcript with document bytes and secrets redacted;
- operation plan ID, plan hash, operation ID, terminal operation status, and
  warnings;
- relevant dashboard snapshots from the dashboard/alert contract;
- audit event IDs for started, canceled, cordoned, uncordoned, or critical
  workflow actions;
- abort, rollback, or escalation decision when the happy path is not completed.

The release artifact for this gate is `operator-runbook-approval`, owned by the
operations owner. It must show that these runbooks are reviewed, linked from
alerts, and usable for the target deployment profile.
