# PRD closure policy

PRDs that are gated by production-readiness work stay open until their closing
evidence is current, linked, and reviewable.

## Cilium-backed Tier 2 guard

PRDs #312 and #337 must not be closed until a green Tier 2 prod-like E2E run has
been produced on the Cilium-backed Kind Cell.

The closure comment for each PRD must include:

- the GitHub Actions run link for `.github/workflows/prodlike-e2e.yml`;
- the commit SHA or branch ref tested by that run;
- the run result showing `make tier2-e2e-up` passed;
- the uploaded Tier 2 artifact name that contains the Kind diagnostics and E2E
  log.

Manual local Tier 2 output is useful while developing, but it is not enough to
close #312 or #337. The durable proof is a green Tier 2 GitHub Actions run link.

## Tier 3 evidence path

Tier 3 evidence can be produced from GitHub Actions:

```sh
gh workflow run evidence-gate.yml --ref v2 -f scenario=throughput
```

The workflow uploads the evidence bundle directory and writes the selected bundle
path to `artifacts/tier3-bundle-path.txt`.

The equivalent local command path is:

```sh
make tier3-evidence-up STRESS_SCENARIO=throughput
```

The local command writes a timestamped bundle under `evidence/`.
