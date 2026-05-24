//go:build integration

package crashfault

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const catalogPatternTimeout = 30 * time.Second

func TestCatalogPatternsMatchRealTestFunctions(t *testing.T) {
	repoRoot := repositoryRoot(t)
	goCommand := goTestCommand()
	for _, scenario := range Catalog() {
		for _, command := range scenario.Commands {
			command := command
			t.Run(scenario.ID+"/"+command.Package, func(t *testing.T) {
				matches := listMatchingTests(t, repoRoot, goCommand, command)
				if len(matches) == 0 {
					t.Fatalf("catalog pattern %q in package %s matched no real test functions", command.Pattern, command.Package)
				}
			})
		}
	}
}

func listMatchingTests(t *testing.T, repoRoot, goCommand string, command GoTestCommand) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), catalogPatternTimeout)
	defer cancel()
	args := []string{"test", "-list", command.Pattern, command.Package}
	// #nosec G204 -- command and package patterns come from the repo-owned crash/fault catalog.
	cmd := exec.CommandContext(ctx, goCommand, args...)
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("%s timed out: %v", strings.Join(append([]string{goCommand}, args...), " "), ctx.Err())
	}
	if err != nil {
		t.Fatalf("%s failed: %v\n%s", strings.Join(append([]string{goCommand}, args...), " "), err, output)
	}
	return listedTestNames(string(output))
}

func listedTestNames(output string) []string {
	var matches []string
	for _, line := range strings.Split(output, "\n") {
		name := strings.TrimSpace(line)
		if strings.HasPrefix(name, "Test") {
			matches = append(matches, name)
		}
	}
	return matches
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func goTestCommand() string {
	if value := strings.TrimSpace(os.Getenv("GO")); value != "" {
		return value
	}
	return "go"
}

func TestListedTestNamesIgnoresPackageStatusLines(t *testing.T) {
	output := fmt.Sprintf("%s\n%s\n%s\n", "TestCrashBoundary", "ok  \tgithub.com/petabytecl/scrap/internal/localstorage\t0.004s", "")
	matches := listedTestNames(output)
	if len(matches) != 1 || matches[0] != "TestCrashBoundary" {
		t.Fatalf("matches = %#v, want TestCrashBoundary only", matches)
	}
}
