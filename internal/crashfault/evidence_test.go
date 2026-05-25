package crashfault

import (
	"context"
	"os"
	"os/exec"
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

func TestRunCollectsGitStateWhenCommitIsNotProvided(t *testing.T) {
	dir := initCrashFaultGitRepo(t)
	sha, dirty, err := collectGitState(context.Background(), dir)
	testutil.RequireNoErrorf(t, err, "collect clean git state")
	testutil.RequireFalsef(t, dirty, "clean repository marked dirty")

	report, err := Run(context.Background(), Options{
		WorkDir:     dir,
		GeneratedAt: time.Unix(100, 0).UTC(),
		Runner:      &recordingRunner{},
	})
	testutil.RequireNoErrorf(t, err, "run evidence")
	testutil.RequireEqualf(t, report.CommitSHA, sha, "collected commit sha")
	testutil.RequireFalsef(t, report.DirtyTree, "run marked clean repository dirty")

	if err := os.WriteFile(dir+"/tracked.txt", []byte("dirty"), 0o600); err != nil {
		t.Fatalf("dirty tracked file: %v", err)
	}
	_, dirty, err = collectGitState(context.Background(), dir)
	testutil.RequireNoErrorf(t, err, "collect dirty git state")
	testutil.RequireTruef(t, dirty, "dirty repository marked clean")
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

func TestExecRunnerAndSanitizersBoundProcessOutput(t *testing.T) {
	output := ExecRunner{}.Run(context.Background(), CommandInvocation{
		Name:             "git",
		Args:             []string{"--version"},
		WorkDir:          t.TempDir(),
		OutputLimitBytes: 8,
	})
	testutil.RequireEqualf(t, output.ExitCode, 0, "git version exit code")
	testutil.RequireTruef(t, strings.Contains(output.Stdout, "[truncated]"), "stdout = %q", output.Stdout)

	output = ExecRunner{}.Run(context.Background(), CommandInvocation{
		Name:    "git",
		Args:    []string{"not-a-real-git-subcommand"},
		WorkDir: t.TempDir(),
	})
	testutil.RequireTruef(t, output.ExitCode != 0, "invalid git subcommand exit code")
	testutil.RequireTruef(t, output.Error != "", "invalid git subcommand error is empty")

	sanitized := sanitizeOutput("SCRAP_OPENBAO_TOKEN PlaintextDEK wrapped_dek 123456789", 40)
	for _, leaked := range []string{"SCRAP_OPENBAO_TOKEN", "PlaintextDEK", "wrapped_dek", "123456789"} {
		testutil.RequireFalsef(t, strings.Contains(sanitized, leaked), "sanitized output leaked %s: %q", leaked, sanitized)
	}
	testutil.RequireTruef(t, strings.Contains(sanitized, "[truncated]"), "sanitized output was not truncated: %q", sanitized)
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

	defaulted := newLimitedOutput(0)
	_, err = defaulted.Write([]byte("ok"))
	testutil.RequireNoErrorf(t, err, "write default-limited output")
	testutil.RequireEqualf(t, defaulted.String(), "ok", "default-limited output")
}

func TestValidateCatalogRejectsMalformedScenarios(t *testing.T) {
	valid := Catalog()[0]
	tests := map[string]Scenario{
		"duplicate id":        valid,
		"missing identity":    {Commands: valid.Commands, Invariants: valid.Invariants, ReadinessGates: valid.ReadinessGates, ReleaseGateIDs: valid.ReleaseGateIDs},
		"missing gates":       {ID: "missing-gates", Name: "missing gates", Commands: valid.Commands, Invariants: valid.Invariants},
		"incomplete command":  {ID: "bad-command", Name: "bad command", Commands: []GoTestCommand{{Package: valid.Commands[0].Package}}, Invariants: valid.Invariants, ReadinessGates: valid.ReadinessGates, ReleaseGateIDs: valid.ReleaseGateIDs},
		"missing invariants":  {ID: "missing-invariants", Name: "missing invariants", Commands: valid.Commands, ReadinessGates: valid.ReadinessGates, ReleaseGateIDs: valid.ReleaseGateIDs},
		"missing release ids": {ID: "missing-release", Name: "missing release", Commands: valid.Commands, Invariants: valid.Invariants, ReadinessGates: valid.ReadinessGates},
	}

	for name, scenario := range tests {
		t.Run(name, func(t *testing.T) {
			scenarios := []Scenario{valid, scenario}
			if name != "duplicate id" {
				scenarios = []Scenario{scenario}
			}
			err := ValidateCatalog(scenarios)
			if err == nil {
				t.Fatal("ValidateCatalog accepted malformed scenario")
			}
		})
	}
}

func initCrashFaultGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runCrashFaultGit(t, dir, "init")
	runCrashFaultGit(t, dir, "config", "user.email", "test@example.com")
	runCrashFaultGit(t, dir, "config", "user.name", "Test User")
	runCrashFaultGit(t, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(dir+"/tracked.txt", []byte("clean"), 0o600); err != nil {
		t.Fatalf("write tracked file: %v", err)
	}
	runCrashFaultGit(t, dir, "add", "tracked.txt")
	runCrashFaultGit(t, dir, "commit", "-m", "initial")
	return dir
}

func runCrashFaultGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// #nosec G204 -- tests invoke fixed git setup commands against a temp repo.
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
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
