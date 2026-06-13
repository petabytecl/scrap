# S.C.R.A.P. — Project Rules

## Context

Read `CONTEXT.md` at the repo root before working on the codebase. It defines
the domain vocabulary (Document, Transaction, Block, Frame, Shard) and
architectural constraints. Use the glossary terms exactly — do not drift to
synonyms the glossary explicitly avoids.

### General Guidelines

Behavioral guidelines to reduce common LLM coding mistakes

**Tradeoff:** These guidelines bias toward caution over speed. For trivial tasks, use judgment.

#### 1. Think Before Coding

**Don't assume. Don't hide confusion. Surface tradeoffs.**

Before implementing:

- State your assumptions explicitly. If uncertain, ask.
- If multiple interpretations exist, present them - don't pick silently.
- If a simpler approach exists, say so. Push back when warranted.
- If something is unclear, stop. Name what's confusing. Ask.

#### 2. Simplicity First

**Minimum code that solves the problem. Nothing speculative.**

- No features beyond what was asked.
- No abstractions for single-use code.
- No "flexibility" or "configurability" that wasn't requested.
- No error handling for impossible scenarios.
- If you write 200 lines and it could be 50, rewrite it.

Ask yourself: "Would a senior engineer say this is overcomplicated?" If yes, simplify.

#### 3. Surgical Changes

**Touch only what you must. Clean up only your own mess.**

When editing existing code:

- Don't "improve" adjacent code, comments, or formatting.
- Don't refactor things that aren't broken.
- Match existing style, even if you'd do it differently.
- If you notice unrelated dead code, mention it - don't delete it.

When your changes create orphans:

- Remove imports/variables/functions that YOUR changes made unused.
- Don't remove pre-existing dead code unless asked.

The test: Every changed line should trace directly to the user's request.

#### 4. Goal-Driven Execution

**Define success criteria. Loop until verified.**

Transform tasks into verifiable goals:

- "Add validation" → "Write tests for invalid inputs, then make them pass"
- "Fix the bug" → "Write a test that reproduces it, then make it pass"
- "Refactor X" → "Ensure tests pass before and after"

For multi-step tasks, state a brief plan:

```text
1. [Step] → verify: [check]
2. [Step] → verify: [check]
3. [Step] → verify: [check]
```

Strong success criteria let you loop independently. Weak criteria ("make it work") require constant clarification.

### Code style

All Go code must follow `docs/go-style-guide.md`. The guide covers design
decisions, naming, error handling, concurrency, testing, performance, metrics,
and documentation conventions. Mechanical formatting is enforced by
`.golangci.yml`.

### ADR conventions

Architecture Decision Records live in `docs/adr/`. Use the format:

- **File name:** `NNNN-slug.md` (zero-padded, sequential)
- **Sections:** `# Title`, `Status: Accepted|Superseded`, `Date: YYYY-MM-DD`,
  `## Context`, `## Decision`

Create an ADR when a decision changes the storage format, wire protocol,
dependency choices, or cross-package boundaries. Do not create ADRs for
implementation details that are local to a single package.

## Agent skills

### Issue tracker

Issues are tracked in GitHub Issues on `petabytecl/scrap` via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default label vocabulary (needs-triage, needs-info, ready-for-agent, ready-for-human, wontfix). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context repo — one `CONTEXT.md` at the root, ADRs in `docs/adr/`. See `docs/agents/domain.md`.
