package e2e_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const scrapctlFaultTimeout = 8 * time.Minute

func runScrapctlBackendFault(t *testing.T, action string) {
	t.Helper()
	cellID := e2eCellID()
	args := []string{
		"fault", "backend", action,
		"--namespace", namespace(),
		"--kubectl", kubectlBin(),
		"--timeout", uploadE2ETimeout.String(),
		"--cell-id", cellID,
		"--environment", e2eEnvironment(cellID),
		"--confirm", cellID,
		"--run-id", uniqueName("e2e-backend-" + action),
		"--output", "json",
	}
	if kubeContext := e2eKubeContext(); kubeContext != "" {
		args = append(args, "--context", kubeContext)
	}
	if kubeconfig := e2eKubeconfig(); kubeconfig != "" {
		args = append(args, "--kubeconfig", kubeconfig)
	}
	runScrapctl(t, scrapctlFaultTimeout, args...)
}

func runScrapctl(t *testing.T, timeout time.Duration, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var cmd *exec.Cmd
	if command := scrapctlCommand(); command != "" {
		//nolint:gosec // E2E tests intentionally execute the configured scrapctl binary against the test cluster.
		cmd = exec.CommandContext(ctx, command, args...)
	} else {
		goArgs := append([]string{"run", "../../cmd/scrapctl"}, args...)
		//nolint:gosec // E2E fallback intentionally runs the local Go tool for repo-owned cmd/scrapctl.
		cmd = exec.CommandContext(ctx, "go", goArgs...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("scrapctl %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func scrapctlCommand() string {
	if command := os.Getenv("SCRAP_E2E_SCRAPCTL"); command != "" {
		return command
	}
	return os.Getenv("SCRAPCTL")
}

func e2eCellID() string {
	return envOrDefault("SCRAP_E2E_CELL_ID", "kind-dev")
}

func e2eKubeContext() string {
	if kubeContext := os.Getenv("SCRAP_E2E_KUBE_CONTEXT"); kubeContext != "" {
		return kubeContext
	}
	if cluster := os.Getenv("KIND_CLUSTER"); cluster != "" {
		return "kind-" + cluster
	}
	return ""
}

func e2eKubeconfig() string {
	return os.Getenv("SCRAP_E2E_KUBECONFIG")
}

func e2eEnvironment(cellID string) string {
	if environment := os.Getenv("SCRAP_E2E_ENVIRONMENT"); environment != "" {
		return environment
	}
	if strings.Contains(strings.ToLower(cellID), "prodlike") {
		return "prodlike"
	}
	return "dev"
}
