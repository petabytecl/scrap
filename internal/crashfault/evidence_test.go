package crashfault

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/config"
	"github.com/petabytecl/scrap/internal/testutil"
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
	testutil.RequireNoErrorf(t, ValidateCatalog(scenarios), "catalog invalid")
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
	testutil.RequireNoErrorf(t, err, "run evidence")
	requirePassingReportAndEvidence(t, report, runner)
	data, err := MarshalReport(report)
	testutil.RequireNoErrorf(t, err, "marshal report")
	testutil.RequireTruef(t, strings.Contains(string(data), `"schema_version": "scrap.crashfault.v1"`), "report json = %s", data)
}

func requirePassingReportAndEvidence(t *testing.T, report Report, runner *recordingRunner) {
	t.Helper()
	testutil.RequireTruef(t, report.Ready, "report ready = false")
	testutil.RequireEqualf(t, report.Summary.Failed, 0, "failed scenario count")
	testutil.RequireEqualf(t, report.Summary.Passed, len(Catalog()), "passed scenario count")
	testutil.RequireEqualf(t, report.CommitSHA, "abc123", "commit sha")
	testutil.RequireFalsef(t, report.DirtyTree, "dirty tree = true")
	testutil.RequireEqualf(t, report.Runner.Profile, "dedicated-linux", "runner profile")
	testutil.RequireEqualf(t, report.Runner.FilesystemProfile, "ext4-local-pv", "filesystem profile")
	testutil.RequireEqualf(t, report.Config.GoTestCount, 2, "go test count")
	testutil.RequireEqualf(t, len(report.ReleaseGateEvidence), 3, "release evidence count")
	testutil.RequireTruef(t, len(runner.commands) > 0, "commands = %#v, want commands", runner.commands)
	testutil.RequireTruef(t, slices.Contains(runner.commands[0].Args, "-count=2"), "commands = %#v, want go test count flag", runner.commands)
}

func TestRunRequiresArtifactURIForReleaseEvidence(t *testing.T) {
	report, err := Run(context.Background(), Options{
		GeneratedAt: time.Unix(100, 0).UTC(),
		CommitSHA:   "abc123",
		Runner:      &recordingRunner{},
	})
	testutil.RequireNoErrorf(t, err, "run evidence")
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
	testutil.RequireNoErrorf(t, err, "run evidence")
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
	testutil.RequireNoErrorf(t, err, "write output")
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
