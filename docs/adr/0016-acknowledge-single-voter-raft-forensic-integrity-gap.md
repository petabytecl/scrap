# Acknowledge single-voter Raft forensic integrity gap

Status: accepted

## Context

S.C.R.A.P. uses Raft-backed metadata as the authority for document visibility,
transaction state, upload outboxes, repair state, and recovery state. The
current implementation and local evidence still allow single-voter,
single-volume metadata operation for development and pre-production workflows.

Single-voter Raft can provide deterministic ordering and crash-recovery
semantics, but it is not an independent forensic witness. An operator with
filesystem access to the metadata volume can delete, modify, or fabricate log,
snapshot, or Pebble state without another voter disagreeing. Checksums can
detect accidental corruption and some partial writes; they cannot prove that a
privileged actor did not rewrite all local state consistently.

Billing documents may be retained for years and may be subject to audit,
dispute, or subpoena. Production readiness must not imply subpoena-grade
forensic integrity from a single local metadata copy.

## Decision

Document the single-voter forensic limitation now and keep production write ACK
promotion blocked until a target deployment has independent integrity evidence.

For the current repo-owned milestone, S.C.R.A.P. chooses the minimum viable
mitigation:

- single-voter mode is non-production evidence unless the release owner records
  an explicit exception;
- production documentation must state that single-voter Raft is crash-consistent
  but not independently tamper-evident;
- operator access to metadata volumes must be minimized through non-root
  containers, read-only root filesystems, Kubernetes RBAC, restricted volume
  attachment, audited admin operations, and retained deployment logs;
- release evidence must preserve metadata snapshots, operation/audit IDs, and
  sanitized support bundles, but those artifacts are compensating controls, not
  proof against a privileged storage rewrite;
- multi-voter Raft remains the production topology target.

If production promotion is needed before multi-voter metadata is available, the
next acceptable mitigation is an external append-only commit witness. After each
metadata commit, the shard records an HMAC-SHA256 or signature over the commit
identity, index, term, payload digest, previous witness digest, and timestamp to
an independently controlled append-only system. The witness key must be outside
the storage member's writable filesystem, and the witness record must be
validated during recovery drills.

The preferred long-term mitigation is multi-voter Raft across independently
scheduled members with tested membership changes, quorum loss behavior,
snapshot transfer, ReadIndex, and rolling upgrades. The external witness is a
forensic supplement, not a replacement for quorum metadata durability.

## Consequences

- Production readiness reports must not treat single-voter Raft as sufficient
  forensic integrity evidence for billing records.
- The production write ACK gate remains blocked for forensic integrity until a
  target profile has multi-voter evidence or an accepted external witness
  exception.
- Option B, if implemented, needs a test proving that each committed metadata
  payload produces an external witness record within one second and that witness
  verification fails closed when the chain is missing or tampered.
- Option C requires deployment and upgrade evidence for moving from
  single-voter development cells to multi-voter production cells without data
  loss or split-brain metadata visibility.
- This decision does not weaken the current crash-consistency, corruption, or
  authorization gates; it prevents those gates from being overstated as
  independent forensic non-repudiation.

## Follow-Up

- Add release-gate evidence for an external append-only commit witness if the
  project chooses Option B before multi-voter production.
- Add deployment and recovery evidence for multi-voter Raft before claiming
  production forensic integrity through quorum replication.
