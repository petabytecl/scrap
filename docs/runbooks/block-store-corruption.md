# Block Store Corruption Runbook

Status: production-readiness runbook for issue `#145`
Owner: `@cotocisternas`
Last verified: not yet independently drilled

Use this runbook when local block bytes, indexes, open logs, or verification
frames fail checksum, range, or replay validation.

This focused runbook supplements the corruption and repair workflows in
[Storage Gateway Operator Runbooks](../storage-gateway-operator-runbooks.md).

## Signals

- read paths fail with integrity-failure details instead of streaming bytes;
- repair queue contains quarantined local refs;
- corruption dashboards or alerts report checksum mismatches or all-sources
  corrupt evidence;
- startup byte-serving verification records missing or corrupt local refs.

## Preconditions

- Do not delete or overwrite block files before a replacement has been written,
  verified, and atomically renamed.
- Do not mark a suspect local source healthy until byte verification passes.
- Preserve the corrupted block, index, logs, repair state, and audit evidence
  for root-cause review.
- Keep reads fail-closed; do not bypass checksum verification to recover data.

## Diagnosis

```sh
scrapctl --admin-addr "$ADMIN_ADDR" inspect summary
scrapctl --admin-addr "$ADMIN_ADDR" inspect repair-queue --page-size 50
scrapctl --admin-addr "$ADMIN_ADDR" operations list --state queued --state running
```

Identify:

- affected `TENANT_ID`, `TRANSACTION_ID`, `DOCUMENT_NAME`, and `BLOCK_ID`;
- whether backend or peer verified copies exist;
- whether the incident is isolated to one member, one block, or a wider storage
  node problem;
- whether all known sources are corrupt.

## Recovery

1. Cordon the affected member if corruption may affect serving safety.
2. Queue or run the documented repair operation for the affected document,
   range, or block.
3. Repair only from verified peer or backend sources.
4. If no verified source exists, keep the document unavailable and preserve
   all-sources-corrupt evidence for support and compliance handling.
5. Keep the incident open until the repair state, dashboard signal, and audit
   record agree.

## Verification

```sh
scrapctl --admin-addr "$ADMIN_ADDR" inspect repair-queue --page-size 50
scrapctl --admin-addr "$ADMIN_ADDR" inspect summary
scrapctl --admin-addr "$ADMIN_ADDR" status --operation-id "$OPERATION_ID"
```

Expected:

- repaired refs pass checksum and frame verification;
- suspect local refs remain quarantined until replacement verification passes;
- `ReadDocument` either returns verified bytes or a typed integrity failure;
- corruption metrics stop increasing for the incident after repair;
- audit evidence links quarantine, repair, verification, and final status.

## Escalation

Escalate if:

- all sources are corrupt;
- block corruption spans multiple members or blocks;
- the same block fails verification after repair;
- backend object checksums disagree with metadata or envelope records.

## Drill Record

Independent validation is required before this runbook can satisfy release
evidence. Record the drill operator, date, commit SHA, environment, commands
run, injected corruption method, observed failure mode, and any corrections made
to this document.
