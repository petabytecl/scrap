package scripts_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRealS3IAMGateScriptRejectsMissingEvidence(t *testing.T) {
	output, err := runRealS3IAMGateCheck(t, filepath.Join(t.TempDir(), "missing.md"), "")
	if err == nil {
		t.Fatalf("check unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(output, "missing non-empty evidence artifact") {
		t.Fatalf("output = %q, want missing evidence failure", output)
	}
}

func TestRealS3IAMGateScriptAcceptsMissingProofFailEvidence(t *testing.T) {
	evidence := filepath.Join(t.TempDir(), "evidence.md")
	writeEvidence(t, evidence, validRealS3IAMMissingProofEvidence())

	output, err := runRealS3IAMGateCheck(t, evidence, "")
	if err != nil {
		t.Fatalf("check failed: %v\n%s", err, output)
	}
}

func TestRealS3IAMGateScriptAcceptsCompletePassEvidence(t *testing.T) {
	tempDir := t.TempDir()
	evidence := filepath.Join(tempDir, "evidence.md")
	report := filepath.Join(tempDir, "report.json")
	writeEvidence(t, evidence, validRealS3IAMPassEvidence(report))
	writeRealS3IAMReport(t, report, validRealS3IAMReport(report))

	output, err := runRealS3IAMGateCheck(t, evidence, report)
	if err != nil {
		t.Fatalf("check failed: %v\n%s", err, output)
	}
}

func TestRealS3IAMGateScriptRejectsWeakPassEvidence(t *testing.T) {
	tempDir := t.TempDir()
	evidence := filepath.Join(tempDir, "evidence.md")
	report := filepath.Join(tempDir, "report.json")
	content := strings.Replace(validRealS3IAMPassEvidence(report), "real non-local AWS S3 IAM", "LocalStack-only output", 1)
	writeEvidence(t, evidence, content)
	writeRealS3IAMReport(t, report, validRealS3IAMReport(report))

	output, err := runRealS3IAMGateCheck(t, evidence, report)
	if err == nil {
		t.Fatalf("check unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(output, "Real S3/IAM PASS from weak evidence") {
		t.Fatalf("output = %q, want weak PASS failure", output)
	}
}

func TestRealS3IAMGateScriptRejectsRowPassWhenReleaseGateNotPass(t *testing.T) {
	tempDir := t.TempDir()
	evidence := filepath.Join(tempDir, "evidence.md")
	report := filepath.Join(tempDir, "report.json")
	content := strings.Replace(validRealS3IAMPassEvidence(report), "Release gate status: PASS", "Release gate status: FAIL", 1)
	writeEvidence(t, evidence, content)
	writeRealS3IAMReport(t, report, validRealS3IAMReport(report))

	output, err := runRealS3IAMGateCheck(t, evidence, report)
	if err == nil {
		t.Fatalf("check unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(output, "Release gate PASS required when any Real S3/IAM row is PASS") {
		t.Fatalf("output = %q, want row PASS consistency failure", output)
	}
}

func TestRealS3IAMGateScriptRejectsLocalOverridePassReport(t *testing.T) {
	tempDir := t.TempDir()
	evidence := filepath.Join(tempDir, "evidence.md")
	report := filepath.Join(tempDir, "report.json")
	writeEvidence(t, evidence, validRealS3IAMPassEvidence(report))
	writeRealS3IAMReport(t, report, strings.Replace(validRealS3IAMReport(report), `"local_s3_endpoint_allowed": false`, `"local_s3_endpoint_allowed": true`, 1))

	output, err := runRealS3IAMGateCheck(t, evidence, report)
	if err == nil {
		t.Fatalf("check unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(output, "release PASS report does not prove real S3/IAM") {
		t.Fatalf("output = %q, want real S3/IAM report failure", output)
	}
}

func TestRealS3IAMGateScriptRejectsZeroUploadCountPassReport(t *testing.T) {
	tempDir := t.TempDir()
	evidence := filepath.Join(tempDir, "evidence.md")
	report := filepath.Join(tempDir, "report.json")
	writeEvidence(t, evidence, validRealS3IAMPassEvidence(report))
	writeRealS3IAMReport(t, report, strings.Replace(validRealS3IAMReport(report), `"confirmed_upload_count": 1`, `"confirmed_upload_count": 0`, 1))

	output, err := runRealS3IAMGateCheck(t, evidence, report)
	if err == nil {
		t.Fatalf("check unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(output, "release PASS report does not prove real S3/IAM") {
		t.Fatalf("output = %q, want upload count failure", output)
	}
}

func TestRealS3IAMGateScriptRejectsBooleanUploadCountPassReport(t *testing.T) {
	tempDir := t.TempDir()
	evidence := filepath.Join(tempDir, "evidence.md")
	report := filepath.Join(tempDir, "report.json")
	writeEvidence(t, evidence, validRealS3IAMPassEvidence(report))
	writeRealS3IAMReport(t, report, strings.Replace(validRealS3IAMReport(report), `"confirmed_upload_count": 1`, `"confirmed_upload_count": true`, 1))

	output, err := runRealS3IAMGateCheck(t, evidence, report)
	if err == nil {
		t.Fatalf("check unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(output, "release PASS report does not prove real S3/IAM") {
		t.Fatalf("output = %q, want upload count type failure", output)
	}
}

func TestRealS3IAMGateScriptRejectsReportPathMismatch(t *testing.T) {
	tempDir := t.TempDir()
	evidence := filepath.Join(tempDir, "evidence.md")
	report := filepath.Join(tempDir, "report.json")
	writeEvidence(t, evidence, validRealS3IAMPassEvidence(report))
	writeRealS3IAMReport(t, report, validRealS3IAMReport("artifacts/production-rehearsal/report.json"))

	output, err := runRealS3IAMGateCheck(t, evidence, report)
	if err == nil {
		t.Fatalf("check unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(output, "release PASS report does not prove real S3/IAM") {
		t.Fatalf("output = %q, want report path consistency failure", output)
	}
}

func TestRealS3IAMGateScriptRejectsMissingReportProvenance(t *testing.T) {
	tempDir := t.TempDir()
	evidence := filepath.Join(tempDir, "evidence.md")
	report := filepath.Join(tempDir, "report.json")
	writeEvidence(t, evidence, validRealS3IAMPassEvidence(report))
	content := strings.Replace(validRealS3IAMReport(report), `"commit_ref": "abc1234"`, `"commit_ref": ""`, 1)
	writeRealS3IAMReport(t, report, content)

	output, err := runRealS3IAMGateCheck(t, evidence, report)
	if err == nil {
		t.Fatalf("check unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(output, "release PASS report does not prove real S3/IAM") {
		t.Fatalf("output = %q, want report provenance failure", output)
	}
}

func TestRealS3IAMGateScriptRejectsFailedRedactionProof(t *testing.T) {
	tempDir := t.TempDir()
	evidence := filepath.Join(tempDir, "evidence.md")
	report := filepath.Join(tempDir, "report.json")
	writeEvidence(t, evidence, validRealS3IAMPassEvidence(report))
	content := strings.Replace(validRealS3IAMReport(report), `"report_excludes_secret_material": true`, `"report_excludes_secret_material": false`, 1)
	writeRealS3IAMReport(t, report, content)

	output, err := runRealS3IAMGateCheck(t, evidence, report)
	if err == nil {
		t.Fatalf("check unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(output, "release PASS report does not prove real S3/IAM") {
		t.Fatalf("output = %q, want redaction proof failure", output)
	}
}

func TestRealS3IAMGateScriptRejectsLeakedReportFields(t *testing.T) {
	tempDir := t.TempDir()
	evidence := filepath.Join(tempDir, "evidence.md")
	report := filepath.Join(tempDir, "report.json")
	writeEvidence(t, evidence, validRealS3IAMPassEvidence(report))
	content := strings.Replace(validRealS3IAMReport(report), `"backend": "s3"`, `"backend": "s3", "raw_backend_object_key": "shards/7/blocks/example"`, 1)
	writeRealS3IAMReport(t, report, content)

	output, err := runRealS3IAMGateCheck(t, evidence, report)
	if err == nil {
		t.Fatalf("check unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(output, "release PASS report does not prove real S3/IAM") {
		t.Fatalf("output = %q, want report leak failure", output)
	}
}

func TestRealS3IAMGateScriptRejectsOpenIssuePassEvidence(t *testing.T) {
	tempDir := t.TempDir()
	evidence := filepath.Join(tempDir, "evidence.md")
	report := filepath.Join(tempDir, "report.json")
	content := strings.Replace(validRealS3IAMPassEvidence(report), "issue `#429` closed", "issue `#429` open", 1)
	writeEvidence(t, evidence, content)
	writeRealS3IAMReport(t, report, validRealS3IAMReport(report))

	output, err := runRealS3IAMGateCheck(t, evidence, report)
	if err == nil {
		t.Fatalf("check unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(output, "Release PASS cannot cite issue #429 as open") {
		t.Fatalf("output = %q, want open issue failure", output)
	}
}

func TestRealS3IAMGateScriptAcceptsCodeSpanPipesInEvidenceRows(t *testing.T) {
	evidence := filepath.Join(t.TempDir(), "evidence.md")
	content := strings.Replace(
		validRealS3IAMMissingProofEvidence(),
		"Requires `status=passed`",
		"Requires `s3:GetObject|s3:PutObject` and `status=passed`",
		1,
	)
	writeEvidence(t, evidence, content)

	output, err := runRealS3IAMGateCheck(t, evidence, "")
	if err != nil {
		t.Fatalf("check failed: %v\n%s", err, output)
	}
}

func TestRealS3IAMGateScriptRejectsFieldsOnlyInProse(t *testing.T) {
	evidence := filepath.Join(t.TempDir(), "evidence.md")
	writeEvidence(t, evidence, proseOnlyRealS3IAMEvidence())

	output, err := runRealS3IAMGateCheck(t, evidence, "")
	if err == nil {
		t.Fatalf("check unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(output, "missing real S3/IAM full evidence row") {
		t.Fatalf("output = %q, want missing full row failure", output)
	}
}

func runRealS3IAMGateCheck(t *testing.T, evidencePath, reportPath string) (string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	repoRoot := repoRoot(t)
	//nolint:gosec // The test executes this repo's validation script against test-owned files.
	cmd := exec.CommandContext(ctx, "bash", filepath.Join(repoRoot, "scripts/check-real-s3-iam-gate.sh"))
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "REAL_S3_IAM_EVIDENCE="+evidencePath)
	if reportPath != "" {
		cmd.Env = append(cmd.Env, "REAL_S3_IAM_REPORT="+reportPath)
	}
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func writeRealS3IAMReport(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write report: %v", err)
	}
}

func validRealS3IAMMissingProofEvidence() string {
	return `# SCRAP Real S3/IAM Production Rehearsal Evidence

Artifact status: complete for Story 6.6 validation
Release gate status: FAIL

Story: 6.6 - Real S3/IAM Production Rehearsal Closure

## Gate Summary

| Gate | Status | Freshness | Evidence |
| --- | --- | --- | --- |
| Real S3/IAM production rehearsal | FAIL | missing real non-local S3/IAM report | issue ` + "`#429`" + ` open; command ` + "`env GOFLAGS=-buildvcs=false make production-rehearsal`" + `; report path ` + "`artifacts/production-rehearsal/report.json`" + `; owner Release owner. |

## Full Evidence Rows

| Requirement | Command | Commit/ref | Environment | Expected result | Actual result | Artifact path | Issue | Report fields | Redaction proof | Freshness | Status | Owner | Mitigation |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| AC-6.6 Real S3/IAM production rehearsal | ` + "`env GOFLAGS=-buildvcs=false make production-rehearsal`" + ` | current implementation commit ` + "`abc1234`" + ` | Real non-local S3/IAM; ` + "`SCRAP_S3_BUCKET`" + ` and ` + "`SCRAP_S3_REGION`" + ` required; credentials from default provider chain/profile/workload identity; ` + "`SCRAP_S3_ENDPOINT`" + ` unset or real non-local; ` + "`SCRAP_PROD_REHEARSAL_ALLOW_LOCAL_S3`" + ` not used | S3 Backend report proves production mode, real OpenBao Transit, encrypted write/read, committed Backend upload confirmation, and redacted artifacts. | FAIL: real non-local S3/IAM run is not available yet. | Expected sanitized ` + "`artifacts/production-rehearsal/report.json`" + `. | issue ` + "`#429`" + ` open | Requires ` + "`status=passed`" + `, ` + "`command=make production-rehearsal`" + `, ` + "`evidence_tier=real-s3-iam`" + `, ` + "`backend=s3`" + `, ` + "`local_overrides.real_s3_iam=true`" + `, ` + "`local_overrides.local_s3_endpoint_allowed=false`" + `, and ` + "`confirmed_upload_count >= 1`" + `. | Redaction proof must exclude secrets, raw Backend keys, validation tokens, raw logs, Document payloads, and private material. | Missing current real S3/IAM run. | FAIL | Release owner | Run real non-local S3/IAM rehearsal and link sanitized report before final release PASS. |

Hard pass/fail criteria reject vague, screenshot-only, localhost-only, LocalStack-only, local-only, stale, unlinked, or missing IAM provenance.
`
}

func validRealS3IAMPassEvidence(reportPath string) string {
	return `# SCRAP Real S3/IAM Production Rehearsal Evidence

Artifact status: complete for Story 6.6 validation
Release gate status: PASS

Story: 6.6 - Real S3/IAM Production Rehearsal Closure

## Gate Summary

| Gate | Status | Freshness | Evidence |
| --- | --- | --- | --- |
| Real S3/IAM production rehearsal | PASS | current real provider report | issue ` + "`#429`" + ` closed; command ` + "`env GOFLAGS=-buildvcs=false make production-rehearsal`" + `; sanitized report ` + "`" + reportPath + "`" + `; real non-local AWS S3 IAM environment. |

## Full Evidence Rows

| Requirement | Command | Commit/ref | Environment | Expected result | Actual result | Artifact path | Issue | Report fields | Redaction proof | Freshness | Status | Owner | Mitigation |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| AC-6.6 Real S3/IAM production rehearsal | ` + "`env GOFLAGS=-buildvcs=false make production-rehearsal`" + ` | current implementation commit ` + "`abc1234`" + ` | Real non-local AWS S3 IAM; ` + "`SCRAP_S3_BUCKET`" + ` and ` + "`SCRAP_S3_REGION`" + ` present; credentials from default provider chain/profile/workload identity; ` + "`SCRAP_S3_ENDPOINT`" + ` unset or real non-local; ` + "`SCRAP_PROD_REHEARSAL_ALLOW_LOCAL_S3`" + ` not used | S3 Backend report proves production mode, real OpenBao Transit, encrypted write/read, committed Backend upload confirmation, and redacted artifacts. | PASS: sanitized report proves real S3/IAM production rehearsal. | ` + "`" + reportPath + "`" + ` | issue ` + "`#429`" + ` closed | Report contains ` + "`status=passed`" + `, ` + "`command=make production-rehearsal`" + `, ` + "`evidence_tier=real-s3-iam`" + `, ` + "`backend=s3`" + `, ` + "`local_overrides.real_s3_iam=true`" + `, ` + "`local_overrides.local_s3_endpoint_allowed=false`" + `, and ` + "`confirmed_upload_count >= 1`" + `. | Redaction proof passed and excludes secrets, raw Backend keys, validation tokens, raw logs, Document payloads, and private material. | Current real provider report. | PASS | Release owner | None. |

Hard pass/fail criteria reject vague, screenshot-only, localhost-only, LocalStack-only, local-only, stale, unlinked, or missing IAM provenance.
`
}

func proseOnlyRealS3IAMEvidence() string {
	return `# SCRAP Real S3/IAM Production Rehearsal Evidence

Artifact status: incomplete validation fixture
Release gate status: FAIL

Story: 6.6 - Real S3/IAM Production Rehearsal Closure

This prose mentions issue #429, make production-rehearsal, SCRAP_S3_BUCKET,
SCRAP_S3_REGION, default provider chain, configured profile, workload identity,
SCRAP_S3_ENDPOINT, SCRAP_PROD_REHEARSAL_ALLOW_LOCAL_S3,
artifacts/production-rehearsal/report.json, status=passed,
command=make production-rehearsal, evidence_tier=real-s3-iam, backend=s3,
local_overrides.real_s3_iam=true, local_overrides.local_s3_endpoint_allowed=false,
confirmed_upload_count >= 1, redaction proof, secrets, tokens, raw Backend keys,
Document payloads, private material, raw logs, vague, screenshot-only,
localhost-only, LocalStack-only, local-only, stale, unlinked, and missing IAM
provenance, but it intentionally omits the full evidence table.

| Gate | Status | Freshness | Evidence |
| --- | --- | --- | --- |
| Real S3/IAM production rehearsal | FAIL | missing real non-local S3/IAM report | issue ` + "`#429`" + ` open. |
`
}

func validRealS3IAMReport(reportPath string) string {
	return `{
  "status": "passed",
  "command": "make production-rehearsal",
  "commit_ref": "abc1234",
  "git_worktree_state": "clean",
  "git_diff_sha256": "",
  "timestamp": "2026-06-12T20:38:29Z",
  "environment": "production-rehearsal",
  "evidence_tier": "real-s3-iam",
  "expected_result": "production mode with real OpenBao Transit and S3 Backend upload confirmation passes",
  "actual_result": "production security rehearsal passed",
  "artifact_path": "` + reportPath + `",
  "report_path": "` + reportPath + `",
  "security_mode": "production",
  "production_readiness_status": "ready",
  "backend": "s3",
  "local_overrides": {
    "filesystem_backend": false,
    "local_s3_endpoint_allowed": false,
    "real_s3_iam": true
  },
  "openbao_transit": "real",
  "test_hooks_enabled": false,
  "pprof_enabled": false,
  "encrypted_write_read_ok": true,
  "plaintext_leak_scan_ok": true,
  "backend_upload_confirmed": true,
  "confirmed_upload_count": 1,
  "redaction_proof": {
    "status": "passed",
    "plaintext_leak_scan_ok": true,
    "report_excludes_secret_material": true,
    "tracker_ready_evidence_excludes_raw_logs": true,
    "scan_artifact_path": "artifacts/production-rehearsal/redaction-scan.json"
  }
}
`
}
