# Scan Artifact Path Conventions

Shared path conventions for security scan phases. All skills resolve their artifact paths from this file.

## Repository Name

Derive `repo_name` from the repository root directory name or the git remote origin.

## Directory Layout

```
security-scans/
└── <repo_name>/
    ├── threat-model.md                    ← repository-scoped threat model (persistent across scans)
    └── scans/
        └── <scan_id>/                     ← per-scan directory
            ├── threat-model.md            ← per-scan copy of the threat model (frozen at scan time)
            ├── findings.md                ← finding discovery report
            ├── validation.md              ← validation report and closure table
            ├── attack-path.md             ← attack-path analysis report
            ├── report.md                  ← final assembled scan report
            ├── fix-report.md             ← fix verification report (when fix-finding is used)
            ├── runtime_inventory.md       ← runtime inventory (repository-wide scans)
            ├── exhaustive-file-checklist.md ← file checklist (repository-wide scans)
            ├── seed_research.md           ← advisory seed research (when applicable)
            └── artifacts/                 ← validation PoCs, logs, and generated files
```

## Path Definitions

- `security_scans_dir`: `security-scans/<repo_name>/`
- `repo_threat_model_path`: `security-scans/<repo_name>/threat-model.md`
- `scan_dir`: `security-scans/<repo_name>/scans/<scan_id>/`
- `artifacts_dir`: `security-scans/<repo_name>/scans/<scan_id>/artifacts/`

### Per-Scan Report Paths

- Per-scan threat model: `<scan_dir>/threat-model.md`
- Finding discovery report: `<scan_dir>/findings.md`
- Validation report: `<scan_dir>/validation.md`
- Attack-path analysis report: `<scan_dir>/attack-path.md`
- Final scan report: `<scan_dir>/report.md`
- Fix report: `<scan_dir>/fix-report.md`
- Runtime inventory: `<scan_dir>/runtime_inventory.md`
- File checklist: `<scan_dir>/exhaustive-file-checklist.md`
- Advisory seed research: `<scan_dir>/seed_research.md`
- Validation artifacts: `<artifacts_dir>/`

## Scan ID

Generate `scan_id` from the scan target:

- PR scan: `pr-<number>`
- Commit scan: `commit-<short-sha>`
- Branch diff: `branch-<branch-name>`
- Working-tree patch: `local-<timestamp>`
- Repository-wide: `repo-<timestamp>`

## Resolution Rules

1. If the user provides an explicit output path, use it instead of these defaults.
2. Create directories as needed when writing artifacts.
3. All paths are relative to the working directory unless the user specifies an absolute path.
