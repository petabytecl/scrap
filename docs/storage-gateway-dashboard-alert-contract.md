# Storage Gateway Dashboard And Alert Contract

Status: production-readiness contract for GitHub issue `#46`
Last updated: 2026-05-23

This document defines the operator dashboard and alert contract required before
S.C.R.A.P. accepts production traffic. It turns the semantic observability
standard into named signals, label rules, alert classes, and required alert
examples.

The contract is vendor-neutral. Prometheus, OpenTelemetry, Grafana, Cloud
Monitoring, or another backend may be used when the release evidence maps the
implemented metric names back to the semantic names below.

## Release Evidence Rules

The release artifact for this gate is `dashboard-alert-contract-approval`,
owned by the operations owner. It must include:

- dashboard export or screenshot references for the target deployment profile;
- alert rule export or policy references for the target deployment profile;
- the commit SHA, deployment profile, cell, and metric backend used to validate
  the contract;
- proof that required alert examples evaluate into the expected alert class;
- a high-cardinality label review;
- owner approval or a time-bound release exception.

The contract does not require a specific graphing vendor, but it does require
the release evidence to prove that every required semantic signal is either
implemented directly or mapped one-to-one by instrumentation adapters.

## Metric Label Contract

Default dashboard and alert metrics may use only bounded labels. These labels
are allowed when the value set is controlled by deployment configuration or a
small enum:

| Label | Required bounds |
| --- | --- |
| `cell_id` | Deployment cell identifier, not a tenant or source namespace. |
| `member_id` | Storage member identity. Bounded by the deployment profile. |
| `deployment_profile` | Profile name such as `dev`, `staging`, or the approved production profile. |
| `priority_class` | Bounded write/read priority enum. |
| `document_class` | Bounded document class enum, not document name or type text supplied by callers. |
| `operation_lane` | Bounded lane such as `upload`, `restore`, `repair`, `prewarm`, `scrub`, or `rewrap`. |
| `backend_provider` | Bounded provider enum such as `s3`, `gcs`, `azure`, or `filesystem`. |
| `backend_profile` | Deployment-owned backend capacity profile name. |
| `source` | Bounded source enum such as `local`, `peer`, `backend`, or `openbao`. |
| `result`, `error_class`, `reason`, `decision` | Package-owned enums. Do not use raw error text. |
| `raft_role` | Bounded role enum. |
| `openbao_state` | Bounded state enum such as `healthy`, `degraded`, or `unavailable`. |
| `operation_type`, `admin_action`, `audit_action` | Bounded operation/action enums. |
| `alert_class`, `alert_state`, `severity` | Bounded alert routing enums. |

These identifiers are forbidden as metric labels in the release dashboards and
alert rules:

- `tenant_id`, `transaction_id`, `document_id`, `document_name`;
- `request_id`, `correlation_id`, `trace_id`, `operation_id`;
- `write_attempt_id`, raw `client_idempotency_key`, actor-supplied reason text;
- `block_id`, `frame_index`, `range_start`, `range_length`;
- backend object key, backend prefix, provider request ID;
- OpenBao token, key material, wrapped DEK, plaintext DEK, plaintext secret;
- `shard_id` unless an approved bounded-shard dashboard plan is attached to the
  release evidence.

High-cardinality identifiers belong in structured logs, traces, durable
operation state, audit events, or sampled diagnostic artifacts. Dashboards may
link to those records by time range and bounded labels, but must not promote the
identifiers into metric labels.

## Dashboard Contracts

Each production dashboard must show the target `cell_id`,
`deployment_profile`, and current service version or release SHA. Dashboards
must separate normal, degraded, blocked, and unknown states instead of showing
only aggregate success rates.

### Write Admission And ACK

Purpose: show whether writes are accepted, throttled, rejected, or waiting on
the durable ACK boundary.

Required semantic signals:

- `scrap_write_admission_requests_total{decision,reason,priority_class,document_class}`;
- `scrap_write_admission_inflight{priority_class,document_class}`;
- `scrap_write_admission_latency_seconds{result,priority_class,document_class}`;
- `scrap_write_ack_latency_seconds{result,priority_class,document_class}`;
- `scrap_write_rejections_total{reason,priority_class,document_class}`;
- `scrap_write_retry_outcomes_total{result,reason}`;
- `scrap_write_idempotency_conflicts_total{result,reason}`;
- `scrap_production_write_ack_gate_state{decision,reason}`.

Required panels:

- admission decision rate split by admitted, throttled, rejected, and blocked;
- ACK latency percentiles for successful writes;
- rejection reasons and retry outcomes;
- production write ACK gate state.

### Disk Runway

Purpose: show local durability capacity before hard write rejection.

Required semantic signals:

- `scrap_disk_used_bytes{member_id}`;
- `scrap_disk_free_bytes{member_id}`;
- `scrap_disk_reserved_bytes{member_id}`;
- `scrap_disk_runway_seconds{member_id}`;
- `scrap_disk_critical_reserve_usage_ratio{member_id}`;
- `scrap_open_block_bytes{member_id}`;
- `scrap_prepare_log_bytes{member_id}`;
- `scrap_local_durability_window_seconds{member_id}`;
- `scrap_disk_admission_state{member_id,decision,reason}`.

Required panels:

- runway time and reserve usage per member;
- open block and prepare log growth;
- normal, reserve, and blocked admission states;
- top bounded reasons for disk-based rejection.

### Backend Lag

Purpose: show whether backend upload, verify, restore, and provider retry lag
threaten local durability or restore expectations.

Required semantic signals:

- `scrap_backend_upload_backlog_bytes{backend_provider,backend_profile,operation_lane}`;
- `scrap_backend_oldest_upload_age_seconds{backend_provider,backend_profile,operation_lane}`;
- `scrap_backend_lane_queue_depth{backend_provider,backend_profile,operation_lane}`;
- `scrap_backend_lane_oldest_age_seconds{backend_provider,backend_profile,operation_lane}`;
- `scrap_backend_provider_retry_delay_seconds{backend_provider,backend_profile,error_class}`;
- `scrap_backend_provider_errors_total{backend_provider,backend_profile,error_class}`;
- `scrap_backend_verification_lag_seconds{backend_provider,backend_profile}`;
- `scrap_backend_token_saturation_ratio{backend_provider,backend_profile,operation_lane}`.

Required panels:

- upload backlog bytes and oldest upload age;
- lane queue depth and oldest age for upload, restore, prewarm, repair, and scrub;
- provider errors and retry delay by bounded error class;
- verification lag and token saturation.

### Repair Lag

Purpose: show whether repair can keep up with corruption, peer catch-up, and
missing local byte recovery.

Required semantic signals:

- `scrap_repair_queue_depth{operation_lane,reason}`;
- `scrap_repair_oldest_age_seconds{operation_lane,reason}`;
- `scrap_repair_attempts_total{result,reason}`;
- `scrap_repair_quarantined_sources{source,reason}`;
- `scrap_repair_verified_source_failures_total{source,error_class}`;
- `scrap_peer_catchup_lag_seconds{member_id}`;

Required panels:

- repair queue depth and oldest age;
- repair success and failure rate;
- quarantined source count by source type;
- peer catch-up lag.

### Restore Lag

Purpose: show whether restore and prewarm work is delayed and whether users are
seeing restore-pending responses.

Required semantic signals:

- `scrap_restore_queue_depth{operation_lane,reason}`;
- `scrap_restore_oldest_age_seconds{operation_lane,reason}`;
- `scrap_restore_attempts_total{result,reason}`;
- `scrap_restore_pending_responses_total{reason,document_class}`;
- `scrap_restore_archive_budget_usage_ratio{operation_lane}`;
- `scrap_restore_crypto_unavailable_total{reason}`.

Required panels:

- restore and prewarm queue depth and age;
- restore success and failure rate;
- restore-pending response rate;
- archive restore budget and crypto-unavailable responses.

### Corruption Incidents

Purpose: show detected integrity failures, quarantine decisions, and cases where
no verified source remains.

Required semantic signals:

- `scrap_corruption_checksum_mismatches_total{source,error_class}`;
- `scrap_corruption_all_sources_corrupt_total{document_class}`;
- `scrap_corruption_source_quarantines_total{source,reason}`;
- `scrap_corruption_integrity_failure_responses_total{reason,document_class}`;
- `scrap_corruption_evidence_records_total{reason}`;
- `scrap_corruption_repair_blocked_total{reason}`.

Required panels:

- checksum mismatch rate by source type;
- all-sources-corrupt and integrity failure responses;
- quarantine decisions and repair-blocked counts;
- links to sanitized evidence records, logs, traces, and audit events.

### Raft Health

Purpose: show whether authoritative metadata can elect, commit, apply, compact,
and serve fresh reads.

Required semantic signals:

- `scrap_raft_leader_available{cell_id}`;
- `scrap_raft_quorum_available{cell_id}`;
- `scrap_raft_role{raft_role}`;
- `scrap_raft_term_changes_total{reason}`;
- `scrap_raft_commit_index_lag{member_id}`;
- `scrap_raft_apply_index_lag{member_id}`;
- `scrap_raft_proposal_failures_total{error_class}`;
- `scrap_raft_read_index_failures_total{error_class}`;
- `scrap_raft_snapshot_lag_seconds{member_id}`;
- `scrap_raft_compaction_lag_seconds{member_id}`;
- `scrap_raft_membership_change_failures_total{reason}`.

Required panels:

- leader and quorum availability;
- commit and apply lag per member;
- proposal and ReadIndex failures;
- snapshot, compaction, and membership-change health.

### OpenBao Health

Purpose: show whether envelope encryption can wrap, unwrap, rewrap, and audit
without exposing secret material.

Required semantic signals:

- `scrap_openbao_transit_request_latency_seconds{operation_lane,openbao_state}`;
- `scrap_openbao_transit_errors_total{operation_lane,error_class}`;
- `scrap_openbao_availability_state{openbao_state}`;
- `scrap_openbao_dek_cache_requests_total{result}`;
- `scrap_openbao_key_version_lookup_failures_total{error_class}`;
- `scrap_openbao_rewrap_lag_seconds{operation_lane}`;
- `scrap_openbao_audit_device_healthy{openbao_state}`.

Required panels:

- Transit latency and errors by operation lane;
- availability and audit-device health;
- DEK cache hit and miss rate;
- key-version lookup failures and rewrap lag.

### Placement And Byte-Serving Eligibility

Purpose: show whether replicas remain placed on distinct eligible storage nodes
and whether peer bytes are eligible to serve reads or repairs.

Required semantic signals:

- `scrap_replica_placement_unhealthy{reason}`;
- `scrap_replica_placement_rejections_total{reason}`;
- `scrap_replica_distinct_storage_nodes{decision}`;
- `scrap_peer_byte_serving_eligible{member_id,decision,reason}`;
- `scrap_peer_prepare_quorum_failures_total{reason}`.

Required panels:

- placement health and rejection reasons;
- distinct storage node count against policy;
- byte-serving eligibility per bounded member;
- peer prepare quorum failures.

### Operation Backlog

Purpose: show whether durable admin and background operations are progressing
or stuck.

Required semantic signals:

- `scrap_operation_queue_depth{operation_type,operation_lane}`;
- `scrap_operation_oldest_age_seconds{operation_type,operation_lane}`;
- `scrap_operation_running{operation_type}`;
- `scrap_operation_terminal_total{operation_type,result,reason}`;
- `scrap_operation_retries_total{operation_type,reason}`;
- `scrap_operation_stuck_total{operation_type,reason}`.

Required panels:

- queued, running, terminal, and stuck operations;
- oldest operation age by lane;
- retry rate and failure reasons;
- links to durable operation status by operation ID outside metric labels.

## Alert Classes

Every alert must set exactly one `alert_class`:

| Alert class | Meaning | Default routing |
| --- | --- | --- |
| `customer_impacting` | Customers are seeing failed or degraded API behavior, or a required serving dependency is unavailable for current traffic. | Page immediately. |
| `durability_risk` | The production ACK guarantee is threatened before or without immediate customer-visible failure. | Page for critical risk; ticket for warning risk. |
| `operator_action` | Human action is required to clear backlog, approve evidence, adjust capacity, or investigate a degraded state that is not yet customer-impacting or durability-risking. | Ticket or business-hours page according to target profile. |

Alert rules must include:

- `alert_state` with bounded values such as `warning`, `critical`,
  `blocked`, or `firing`;
- a bounded `reason` or `error_class`;
- a target-profile threshold reference;
- an owner or rotation;
- a runbook link once #47 lands;
- a suppression policy for planned maintenance and release drills;
- the semantic signals used to evaluate the alert.

Threshold values are deployment-profile inputs. They must come from the target
profile and release evidence, not from hard-coded examples in this document.

## Required Alert Examples

The release artifact must prove that these example alerts exist and classify
the state correctly.

| Alert | Required class | Example condition | Required operator context |
| --- | --- | --- | --- |
| `SCRAPDiskRunwayCritical` | `durability_risk`, or `customer_impacting` when writes are blocked | `scrap_disk_runway_seconds` is below the profile critical threshold or `scrap_disk_admission_state{decision="blocked"} == 1`. | Member, profile, runway, reserve usage, admission decision, capacity runbook. |
| `SCRAPPlacementUnhealthy` | `durability_risk` | `scrap_replica_placement_unhealthy > 0` or placement rejections exceed the profile threshold. | Placement reason, distinct-node count, affected member set by logs/traces, replacement workflow. |
| `SCRAPRaftQuorumUnavailable` | `customer_impacting` | `scrap_raft_quorum_available == 0` or leader availability is lost past the profile election window. | Cell, leader state, quorum state, proposal/ReadIndex failures, rollback or fence action. |
| `SCRAPBackendLagCritical` | `durability_risk` | `scrap_backend_oldest_upload_age_seconds` exceeds the local durability window or backlog consumes the profile runway budget. | Backend provider/profile, lane, oldest age, retry delay, provider error class. |
| `SCRAPOpenBaoUnavailable` | `customer_impacting` when encrypted reads/writes/restores fail; otherwise `durability_risk` | `scrap_openbao_availability_state{openbao_state="unavailable"} == 1` or Transit error rate exceeds the profile threshold. | OpenBao state, operation lane, audit-device state, crypto-unavailable response rate. |
| `SCRAPCorruptionIncidentCritical` | `durability_risk`, or `customer_impacting` when integrity failures are returned | Any all-sources-corrupt event, integrity failure response, or quarantine spike above the profile threshold. | Source type, reason, evidence record link, repair state, support escalation path. |
| `SCRAPOperationBacklogStuck` | `operator_action`, escalating to `durability_risk` for repair/restore safety lanes | Oldest operation age or stuck count exceeds the profile threshold. | Operation type, lane, retry count, durable operation status link. |

## Review Checklist

Before production write ACK mode is allowed for a target deployment profile,
reviewers should verify:

- every dashboard above is present or explicitly mapped in release evidence;
- all alert examples exist and fire into the required `alert_class`;
- customer-impacting, durability-risk, and operator-action states route
  differently;
- no forbidden high-cardinality identifier appears as a metric label;
- dashboard links use logs, traces, audit events, or operation records for
  high-cardinality drilldown;
- alert thresholds reference the approved target deployment profile;
- runbook links are filled in after #47.
