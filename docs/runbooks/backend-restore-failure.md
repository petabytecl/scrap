# Backend Restore Failure Runbook

Status: production-readiness runbook for issue `#145`
Owner: `@cotocisternas`
Last verified: not yet independently drilled

Use this runbook when a cold read, restore, prewarm, repair, or DR rebuild
cannot retrieve and verify required backend block, index, envelope, or metadata
artifacts.

This focused runbook supplements the restore, prewarm, and disaster-recovery
workflows in
[Storage Gateway Operator Runbooks](../storage-gateway-operator-runbooks.md).

## Signals

- reads return restore-pending, crypto-unavailable, backend-not-found, or
  integrity-failure details;
- restore or prewarm operations repeatedly retry or fail;
- backend lag, restore lag, OpenBao health, or backend probe alerts fire;
- DR drill evidence reports missing or corrupt recovery artifacts.

## Preconditions

- Do not mark cold data hot until the backend bytes, index, envelope, and
  metadata references verify.
- Do not disable encryption or accept algorithm `none` in production mode.
- Preserve backend object metadata, operation history, and audit events.
- Keep production write ACK blocked if restore evidence is part of release
  readiness and the target profile has not passed.

## Diagnosis

```sh
scrapctl --admin-addr "$ADMIN_ADDR" inspect recovery-readiness
scrapctl --admin-addr "$ADMIN_ADDR" operations list --state queued --state running
scrapctl --admin-addr "$ADMIN_ADDR" status --operation-id "$OPERATION_ID"
```

Check:

- backend object existence, size, checksum, and restore tier state;
- OpenBao Transit availability and key material for envelope validation;
- upload intent state for the affected block;
- metadata checkpoint and current-pointer consistency if this is a DR path.

## Recovery

1. If the backend reports archive restore pending, keep the operation retryable
   and preserve the restore-pending response to callers.
2. If backend objects are missing or corrupt, verify whether another source can
   satisfy repair. Use only verified sources.
3. If envelope validation fails because key material is unavailable, keep the
   document in crypto-unavailable state and restore key access before retry.
4. If DR metadata artifacts are missing or corrupt, stop the rebuild and
   preserve the failed drill evidence. Do not import partial metadata.
5. Retry restore or prewarm only after the failed dependency has a concrete
   recovery action and audit trail.

## Verification

```sh
scrapctl --admin-addr "$ADMIN_ADDR" inspect recovery-readiness
scrapctl --admin-addr "$ADMIN_ADDR" status --operation-id "$OPERATION_ID"
scrapctl --admin-addr "$ADMIN_ADDR" operations list --state queued --state running
```

Expected:

- backend artifacts verify by checksum and required metadata;
- envelope validation succeeds or returns typed crypto-unavailable evidence;
- restore/prewarm/DR operation reaches a durable terminal state;
- cold reads either serve verified bytes after restore or continue returning
  typed restore details;
- dashboard and audit evidence link the failure, dependency recovery, retry,
  and final verification.

## Escalation

Escalate if:

- required backend objects are missing from every configured backend;
- OpenBao key material cannot be restored;
- backend checksums disagree with published metadata;
- DR rebuild would require manual mutation of authoritative metadata.

## Drill Record

Independent validation is required before this runbook can satisfy release
evidence. Record the drill operator, date, commit SHA, backend profile, injected
failure, commands run, observed terminal state, and any corrections made to this
document.
