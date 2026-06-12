# Epic 4 OpenBao Bootstrap Idempotency Evidence

Status: done
Story: 4.6 - `scrapctl openbao bootstrap` Idempotency and Incompatible State
Baseline commit: `c2bedebcfd01e4f3dc4bad88f8027986d03dc4fd`
Started: 2026-06-12T03:51:27-04:00

## Scope

This artifact records evidence for FR-14 / Story 4.6:

- compatible existing Transit mount and S.C.R.A.P. key reruns succeed without unsafe mutation;
- incompatible OpenBao state fails closed with actionable redacted reasons;
- incompatible state is not mutated into an unsafe configuration;
- bootstrap slice closure links fresh setup, idempotency, incompatible-state failure, and redaction evidence.

Out of scope:

- production OpenBao deployment, HA topology, secret custody, storage backend setup, and lifecycle;
- Story 4.7 production security rehearsal closure;
- Shard, Backend, storage format, wire protocol, public/peer/admin API, or envelope metadata changes.

## Source References

- `_bmad-output/implementation-artifacts/4-6-scrapctl-openbao-bootstrap-idempotency-and-incompatible-state.md`
- `_bmad-output/implementation-artifacts/4-5-scrapctl-openbao-bootstrap-fresh-setup.md`
- `_bmad-output/implementation-artifacts/epic-4-openbao-bootstrap-fresh-setup-evidence.md`
- `_bmad-output/planning-artifacts/epics.md`
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` DG-4
- `docs/adr/0023-openbao-api-client.md`
- OpenBao `/sys/mounts`: https://openbao.org/api-docs/system/mounts/
- OpenBao Transit API: https://openbao.org/api-docs/secret/transit/
- OpenBao Go API client: https://pkg.go.dev/github.com/openbao/openbao/api

## Acceptance Criteria Matrix

| AC | Evidence | Result | Notes |
| --- | --- | --- | --- |
| AC-4.6.1 compatible rerun succeeds without unsafe mutation | `TestOpenBaoBootstrapCompatibleRerunDoesNotMutateMountOrKey`; `TestIntegrationScrapctlOpenBaoBootstrapCompatibleRerun`; focused integration command; `make check`. | PASS | Unit fake proves no `MountTransit` or `CreateTransitKey` call. Integration bootstraps real uninitialized OpenBao once, reruns without `--init`, and proves mount/key metadata unchanged. |
| AC-4.6.2 incompatible state fails closed and is not repaired in place | `TestOpenBaoBootstrapRejectsIncompatibleMountWithoutMutation`; `TestOpenBaoBootstrapRejectsIncompatibleExistingKeyWithoutRepair`; `TestIntegrationScrapctlOpenBaoBootstrapIncompatibleStateDoesNotMutate`; focused integration command; `make check`. | PASS | Unit coverage includes non-Transit mount, wrong type, derived mismatch, missing metadata, and invalid latest version. Integration seeds a real incompatible Transit key type and proves metadata unchanged after fail-closed rerun. |
| AC-4.6.3 bootstrap slice closure links fresh setup, idempotency, incompatible-state failure, and redaction evidence | This artifact, Story 4.5 fresh setup evidence, focused unit/integration commands, leak scan counts, and `make check`. | PASS | Fresh setup evidence remains in `_bmad-output/implementation-artifacts/epic-4-openbao-bootstrap-fresh-setup-evidence.md`; this artifact closes idempotency/incompatible state only. Story 4.7 remains responsible for production security rehearsal closure. |

## Command Evidence

- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl -run 'OpenBao.*(Compatible|Incompatible)' -count=1 -v` - RED: failed on missing fake-client call counters and phase assertion helper.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl -run 'OpenBao.*(Compatible|Incompatible)' -count=1 -v` - PASS after adding fake-client operation counters and compatible/incompatible unit coverage.
- `env GOCACHE=/tmp/scrap-v2-go-build go test -tags integration ./test/integration -run 'TestIntegrationScrapctlOpenBaoBootstrap(FreshSetup|CompatibleRerun|IncompatibleState)' -count=1 -v` - RED: new integration tests reached expected OpenBao states, but the shared redaction helper incorrectly required every run to be a successful init run.
- `env GOCACHE=/tmp/scrap-v2-go-build go test -tags integration ./test/integration -run 'TestIntegrationScrapctlOpenBaoBootstrap(FreshSetup|CompatibleRerun|IncompatibleState)' -count=1 -v` - PASS after narrowing the helper to redaction checks and asserting status/init per test.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl -run 'OpenBao|Bootstrap|Idempot|Incompatible|Redact|Evidence' -count=1 -v` - PASS.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./cmd/scrapctl ./internal/scrapctl -count=1` - PASS.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl ./cmd/scrapctl ./internal/encryption -count=1` - PASS.
- `go tool -modfile=tools.go.mod golangci-lint run --timeout=5m ./internal/scrapctl ./cmd/scrapctl ./test/integration` - initially failed on `gocognit` for `TestOpenBaoBootstrapRejectsIncompatibleExistingKeyWithoutRepair`; PASS after extracting the per-case helper.
- `git diff --check` - PASS.
- `env GOCACHE=/tmp/scrap-v2-go-build make check` - PASS. Includes formatting diff check, package-boundary check, Buf lint/generate diff check, golangci-lint, `go test ./...`, `go test -race ./...`, integration tests including LocalStack and OpenBao, and `scrapd`/`scrapctl` builds.
- BMAD code-review subagents failed with usage-limit errors before returning findings. Local fallback review of the Story 4.6 diff found no actionable patch, decision, or deferred findings.

## Redaction Evidence

Final redaction scans will cover:

- `_bmad-output/implementation-artifacts/4-6-scrapctl-openbao-bootstrap-idempotency-and-incompatible-state.md`
- `_bmad-output/implementation-artifacts/epic-4-openbao-bootstrap-idempotency-evidence.md`
- `internal/scrapctl`
- `cmd/scrapctl`
- `test/integration/openbao_bootstrap_scrapctl_test.go`

Final scan results:

- Broad credential-pattern matches: 230. Reviewed as expected command/story/test references, safe env var names, and redaction fixtures.
- Broad identifier/privacy-pattern matches: 68. Reviewed as expected story/test references and redaction scan text.
- Strict shaped-value raw matches: 7. All were code identifier false positives from the broad `[sb].<long>` token shape.
- Strict shaped-value filtered matches: 0.
- No root token, unseal key, Transit token, private key, client cert material, wrapped key, or shaped secret value was found in story, evidence, touched `scrapctl` code, or OpenBao bootstrap integration test surfaces.

## Changed Boundaries

Expected:

- `_bmad-output/implementation-artifacts`
- `internal/scrapctl`
- `test/integration`

Actual:

- `_bmad-output/implementation-artifacts/4-6-scrapctl-openbao-bootstrap-idempotency-and-incompatible-state.md`
- `_bmad-output/implementation-artifacts/epic-4-openbao-bootstrap-idempotency-evidence.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `internal/scrapctl/openbao_bootstrap_test.go`
- `test/integration/openbao_bootstrap_scrapctl_test.go`

No changes to:

- Shard, Backend, server, peer, admin, storage format, wire protocol, protobuf, generated files, deployment overlays, or production OpenBao lifecycle ownership.
