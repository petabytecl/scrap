package crashfault

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/config"
)

type recordingRunner struct {
	failPackage string
	commands    []CommandInvocation
}

func (r *recordingRunner) Run(_ context.Context, invocation CommandInvocation) CommandOutput {
	r.commands = append(r.commands, invocation)
	if r.failPackage != "" && slices.Contains(invocation.Args, r.failPackage) {
		return CommandOutput{ExitCode: 1, Stdout: "failed PlaintextDEK output"}
	}
	return CommandOutput{Stdout: "ok"}
}

func TestCatalogDeclaresRequiredCoverage(t *testing.T) {
	scenarios := Catalog()
	if err := ValidateCatalog(scenarios); err != nil {
		t.Fatalf("catalog invalid: %v", err)
	}
	gates := ReleaseGateIDs(scenarios)
	for _, gate := range []string{"backend-restore", "crash-fault", "peer-byte-durability"} {
		if !slices.Contains(gates, gate) {
			t.Fatalf("release gates = %#v, want %s", gates, gate)
		}
	}
	invariants := strings.Join(allInvariants(scenarios), "\n")
	for _, want := range []string{
		"acknowledged-write",
		"visible-read",
		"corrupt-source-quarantine",
		"durable-operation-replay",
		"verified-backend-restore",
		"crypto-unavailable-fail-closed",
	} {
		if !strings.Contains(invariants, want) {
			t.Fatalf("catalog invariants missing %s in %q", want, invariants)
		}
	}
	readiness := strings.Join(allReadinessGates(scenarios), "\n")
	for _, want := range []string{
		config.ReadinessGateRaftMetadataDurability,
		config.ReadinessGatePeerByteDurability,
		config.ReadinessGateBackendRestoreWorkflow,
		config.ReadinessGateOpenBaoEnvelopeWorkflow,
	} {
		if !strings.Contains(readiness, want) {
			t.Fatalf("catalog readiness gates missing %s in %q", want, readiness)
		}
	}
}

func TestRunBuildsPassingReportAndReleaseEvidence(t *testing.T) {
	runner := &recordingRunner{}
	report, err := Run(context.Background(), Options{
		WorkDir:           "/repo",
		GoCommand:         "go",
		Count:             2,
		RunnerProfile:     "dedicated-linux",
		FilesystemProfile: "ext4-local-pv",
		Seed:              "deterministic",
		ArtifactURI:       "file:///tmp/evidence.json",
		GeneratedAt:       time.Unix(100, 0).UTC(),
		CommandLine:       []string{"scrap-crash-fault-evidence"},
		CommitSHA:         "abc123",
		DirtyTree:         false,
		Runner:            runner,
	})
	if err != nil {
		t.Fatalf("run evidence: %v", err)
	}
	if !report.Ready || report.Summary.Failed != 0 || report.Summary.Passed != len(Catalog()) {
		t.Fatalf("report summary = %#v ready=%t, want all scenarios passed", report.Summary, report.Ready)
	}
	if report.CommitSHA != "abc123" || report.DirtyTree || report.Runner.Profile != "dedicated-linux" ||
		report.Runner.FilesystemProfile != "ext4-local-pv" || report.Config.GoTestCount != 2 {
		t.Fatalf("report metadata = %#v", report)
	}
	if len(report.ReleaseGateEvidence) != 3 {
		t.Fatalf("release evidence = %#v, want three gate entries", report.ReleaseGateEvidence)
	}
	if len(runner.commands) == 0 || !slices.Contains(runner.commands[0].Args, "-count=2") {
		t.Fatalf("commands = %#v, want go test count flag", runner.commands)
	}
	data, err := MarshalReport(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if !strings.Contains(string(data), `"schema_version": "scrap.crashfault.v1"`) {
		t.Fatalf("report json = %s", data)
	}
}

func TestRunRequiresArtifactURIForReleaseEvidence(t *testing.T) {
	report, err := Run(context.Background(), Options{
		GeneratedAt: time.Unix(100, 0).UTC(),
		CommitSHA:   "abc123",
		Runner:      &recordingRunner{},
	})
	if err != nil {
		t.Fatalf("run evidence: %v", err)
	}
	if !report.Ready {
		t.Fatalf("report = %#v, want ready", report)
	}
	if len(report.ReleaseGateEvidence) != 0 {
		t.Fatalf("release evidence = %#v, want none without artifact URI", report.ReleaseGateEvidence)
	}
}

func TestRunMarksFailuresAndSuppressesReleaseEvidence(t *testing.T) {
	report, err := Run(context.Background(), Options{
		GeneratedAt: time.Unix(100, 0).UTC(),
		CommitSHA:   "abc123",
		Runner:      &recordingRunner{failPackage: "./internal/backendupload"},
	})
	if err != nil {
		t.Fatalf("run evidence: %v", err)
	}
	if report.Ready || report.Summary.Failed == 0 || len(report.ReleaseGateEvidence) != 0 {
		t.Fatalf("report = %#v, want failed report without release evidence", report)
	}
	var foundRedaction bool
	for _, scenario := range report.Scenarios {
		for _, command := range scenario.Commands {
			if strings.Contains(command.Output, "[redacted-field]") {
				foundRedaction = true
			}
			if strings.Contains(command.Output, "PlaintextDEK") {
				t.Fatalf("command output was not sanitized: %q", command.Output)
			}
		}
	}
	if !foundRedaction {
		t.Fatal("expected redacted failed command output")
	}
}

func TestLimitedOutputBoundsCapture(t *testing.T) {
	output := newLimitedOutput(5)
	written, err := output.Write([]byte("123456789"))
	if err != nil {
		t.Fatalf("write output: %v", err)
	}
	if written != 9 {
		t.Fatalf("written = %d, want caller byte count", written)
	}
	got := output.String()
	if !strings.Contains(got, "12345") || !strings.Contains(got, "[truncated]") || strings.Contains(got, "6789") {
		t.Fatalf("limited output = %q, want bounded truncated output", got)
	}
}

func allInvariants(scenarios []Scenario) []string {
	var out []string
	for _, scenario := range scenarios {
		out = append(out, scenario.Invariants...)
	}
	return out
}

func allReadinessGates(scenarios []Scenario) []string {
	var out []string
	for _, scenario := range scenarios {
		out = append(out, scenario.ReadinessGates...)
	}
	return out
}
