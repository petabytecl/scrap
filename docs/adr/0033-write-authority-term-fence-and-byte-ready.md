# Write authority: proposal correlation, term fencing, and byte-ready eligibility

Status: Accepted

Date: 2026-07-09

## Context

Thermo-nuclear findings `H-01`, `H-02`, `H-04`, and `H-14` show that the write
state machine can ACK unsafe state: stale leaders can replicate after a term
change, canceled proposals can wake later waiters, byte-incomplete voters can
become leaders, and sequential peer fan-out can stall writes behind a blackholed
Member.

ADR 0001 separates Document bytes from Raft metadata. That separation remains
correct, but it requires an explicit write-authority contract so byte
replication and metadata commit stay term-fenced and quorum-aligned.

## Decision

1. **Proposal correlation.** Every `CommitDocument` carries a unique
   `proposal_id`. Waiters are keyed by `proposal_id`, not only by Document
   identity. Client cancellation retains an uncertain per-identity reservation
   until the original proposal resolves or is superseded by an explicit conflict
   policy.

2. **Term/epoch-fenced replication.** `ReplicateDocument` carries authenticated
   leader ID and term (or write epoch). Followers validate before side effects.
   Follower proposal forwarding is disabled; only the current leader proposes.

3. **Byte-ready voter eligibility.** Each voter tracks verified local byte
   readiness for committed Document Frames. A Member missing required bytes is
   ineligible to lead or serve those Documents until repair/fetch completes.

4. **Concurrent quorum replication.** Leaders fan out replication concurrently
   with bounded per-peer deadlines, return when quorum is durable or impossible,
   and cancel surplus attempts.

Wire changes are additive per CONTEXT schema-evolution rules. Reads accept older
messages during rollout; production Cells must negotiate the fenced path before
write admission.
