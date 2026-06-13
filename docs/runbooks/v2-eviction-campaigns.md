# V2 Eviction Campaigns Runbook

## Purpose

Use this runbook to plan, apply, or inspect policy-gated local Block eviction
campaigns.

## Owning Feature Epic or Release Gate

Epic 3 owns Local Block Lifecycle, eviction campaigns, and restore interaction.
FR-7 and FR-16 apply.

## Symptoms

- A Member needs local disk relief.
- An eviction campaign is stale, rejected, or incomplete.
- Reads after eviction need restore-first behavior verification.

## Normal Path

Plan first:

```sh
scrapctl eviction plan --admin-url <admin-url> \
  --member-hostname <member-hostname> \
  --shard-id <shard-id> --max-blocks <count> \
  --reason maintenance --output=json
```

Apply only after reviewing the plan:

```sh
scrapctl eviction apply --admin-url <admin-url> \
  --plan-id <plan-id> --confirm --output=json
scrapctl eviction status --admin-url <admin-url> \
  --plan-id <plan-id> --output=json
```

## Failure Path

1. If planning fails, check Member hostname, Shard ID, policy, and current
   lifecycle state.
2. If apply fails, do not manually delete Block files; inspect plan status and
   Shard health.
3. If reads fail after eviction, use the restore failure runbook.

## Rollback or Escalation

Eviction does not make Document deletion authority. If apply fails or restore
cannot satisfy reads after eviction, stop additional campaigns and escalate to
the storage owner.

## Expected Outputs

- Plan output names selected Blocks through operator-safe fields.
- Apply output reports accepted/completed or a bounded failure reason.
- Status output reports campaign progress without raw local paths or sensitive
  identifiers.

## Evidence Collection

Record plan, apply, status, commit/ref, environment, expected and actual
outcomes, plan ID, redaction result, and whether restore proof is package,
local, Tier 2, or final release evidence.

## Redaction Requirements

Do not paste local filesystem paths, Document names, Backend object names,
credential values, unredacted log output, trace IDs, request IDs, or auth
claims.

## Authority Boundary

Local Block Lifecycle is per-Member filesystem evidence. It does not decide
Document visibility, Shard membership, durable upload authority, or read
availability policy.

## References

- `CONTEXT.md`
- `docs/adr/0016-phase-4-partial-eviction-boundary.md`
- `docs/adr/0017-local-block-lifecycle-module.md`
- `docs/adr/0018-eviction-campaign-module.md`
- `internal/scrapctl/eviction.go`
