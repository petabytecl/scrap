# Bounded peer transfer and process-wide byte admission

Status: Accepted

Date: 2026-07-09

## Context

Findings `H-13` and `H-15` show that authorized peers can exhaust disk/heap via
unbounded Block ID progression or whole-Document/Block buffers, and that
supported 128 MiB Documents can OOM the shipped 512 MiB pod through stacked
replication, encryption, and S3 buffers.

## Decision

1. **Protocol-valid Block transitions.** Peer replication and transfer admit only
   current/next Block ID transitions required by the protocol. Intermediate
   Block creation is bounded and validated before fsync side effects.

2. **Streaming transfers.** `TransferBlock` streams into bounded staging files
   with format-derived size limits. Whole-component 4 GiB heap accumulation is
   rejected.

3. **Process-wide byte admission.** Replication, encryption, Backend upload, and
   restore share a process-wide byte-weighted concurrency budget sized for the
   shipped memory limit. Whole-Document and whole-Block buffers are removed from
   production paths in favor of streaming or private spools.
