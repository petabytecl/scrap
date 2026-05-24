# Raft Leadership Loss Runbook

Status: production-readiness runbook for issue `#145`
Owner: `@cotocisternas`
Last verified: not yet independently drilled

Use this runbook when a S.C.R.A.P. cell cannot prove authoritative metadata
freshness because the Raft leader is unavailable, stale, or unable to satisfy
ReadIndex/commit barriers.

This focused runbook supplements the shared operator workflows in
[Storage Gateway Operator Runbooks](../storage-gateway-operator-runbooks.md).

## Signals

- public reads or writes return metadata freshness, quorum, or not-leader
  errors;
- `SCRAPRaftQuorumUnavailable` is firing;
- admin `inspect summary` shows no healthy leader or rising Raft queue depth;
- operations remain queued or running because metadata commits do not apply.

## Preconditions

- Do not enable production write ACK while leadership is unstable.
- Do not mutate Pebble, Raft log, or snapshot files by hand.
- Preserve pod logs, audit events, and storage volume snapshots before
  destructive recovery.
- Use the admin API or `scrapctl`; ad hoc storage mutation scripts are out of
  policy.

## Diagnosis

```sh
scrapctl --admin-addr "$ADMIN_ADDR" inspect summary
scrapctl --admin-addr "$ADMIN_ADDR" operations list --state queued --state running
scrapctl --admin-addr "$ADMIN_ADDR" inspect recovery-readiness
```

Check the deployment layer for:

- member pod restarts, pending pods, or volume attach failures;
- NetworkPolicy or service changes blocking member/admin traffic;
- storage node pressure or read-only filesystem remounts;
- clock skew or certificate expiry if mTLS is enabled.

## Recovery

1. Keep production traffic admission closed or degraded until metadata freshness
   is proven.
2. Restore the last healthy member process and volume first. Prefer restarting a
   failed process over replacing storage state.
3. If the leader is alive but not serving fresh reads, collect logs and restart
   only that member after preserving evidence.
4. If the member volume is lost, follow the disaster-recovery rebuild workflow
   in the shared operator runbook. Do not fabricate metadata entries.
5. After a leader is available, wait for queued metadata proposals to drain
   before uncordoning members or resuming write traffic.

## Verification

```sh
scrapctl --admin-addr "$ADMIN_ADDR" inspect summary
scrapctl --admin-addr "$ADMIN_ADDR" operations list --state queued --state running
scrapctl --admin-addr "$ADMIN_ADDR" inspect recovery-readiness
```

Expected:

- one leader is reported for the authoritative shard;
- fresh reads succeed without stale-leader or ReadIndex failures;
- Raft queue depth returns to the normal profile range;
- no unexpected repair, restore, or DR operation remains running;
- audit evidence links the incident, operations, and final verification.

## Escalation

Escalate before making destructive changes if:

- no member can prove metadata freshness;
- committed data visibility differs between members;
- repair or restore requires manual backend artifact selection;
- forensic evidence may be needed for billing or compliance review.

## Drill Record

Independent validation is required before this runbook can satisfy release
evidence. Record the drill operator, date, commit SHA, environment, commands
run, observed states, and any corrections made to this document.
