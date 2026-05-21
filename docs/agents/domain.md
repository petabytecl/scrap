# Domain Docs

How engineering skills should consume this repo's domain documentation.

## Layout

This is a single-context repo.

Expected domain docs, when present:

- `CONTEXT.md` at the repo root
- `docs/adr/` at the repo root

## Consumer rules

Before exploring or changing code, read `CONTEXT.md` if it exists.

Before making architectural or cross-module changes, read relevant ADRs under `docs/adr/` if they exist.

If these files do not exist, proceed silently. Do not suggest creating them upfront. The producer skill (`grill-with-docs`) creates them lazily when project language or architectural decisions get resolved.

When output names a domain concept, use the term as defined in `CONTEXT.md`. If an output contradicts an existing ADR, surface the conflict explicitly.
