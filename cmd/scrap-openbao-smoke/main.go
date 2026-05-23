package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/petabytecl/scrap/internal/openbaosmoke"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scrap-openbao-smoke", flag.ContinueOnError)
	fs.SetOutput(stderr)
	outPath := fs.String("out", "", "write JSON evidence report to this path; defaults to stdout")
	releaseSHA := fs.String("release-sha", "", "release SHA recorded in evidence")
	dirtyTree := fs.String("dirty-tree", "", "dirty-tree state recorded in evidence")
	profileID := fs.String("profile-id", openbaosmoke.DefaultProfileID, "release profile id")
	environmentID := fs.String("environment-id", openbaosmoke.DefaultEnvironmentID, "local environment id")
	namespace := fs.String("namespace", openbaosmoke.DefaultNamespace, "local Kubernetes namespace")
	deployment := fs.String("deployment", openbaosmoke.DefaultDeployment, "local OpenBao deployment name")
	address := fs.String("openbao-addr", openbaosmoke.DefaultAddress, "OpenBao HTTP address")
	outageAddress := fs.String("outage-addr", openbaosmoke.DefaultOutageAddress, "unreachable OpenBao address used for outage classification")
	kubernetesRole := fs.String("kubernetes-role", openbaosmoke.DefaultKubernetesRole, "OpenBao Kubernetes auth role")
	kubernetesClient := fs.String("kubernetes-client", openbaosmoke.DefaultKubernetesClient, "Kubernetes-authenticated local client identity")
	transitKeyPath := fs.String("transit-key-path", openbaosmoke.DefaultTransitKeyPath, "OpenBao Transit key path")
	auditDevice := fs.String("audit-device", openbaosmoke.DefaultAuditDevice, "expected OpenBao audit device")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "unexpected argument %q\n", fs.Arg(0))
		return 2
	}

	report, err := openbaosmoke.Run(context.Background(), openbaosmoke.Options{
		ReleaseSHA:       releaseSHAValue(*releaseSHA),
		DirtyTree:        dirtyTreeValue(*dirtyTree),
		ProfileID:        *profileID,
		EnvironmentID:    *environmentID,
		Namespace:        *namespace,
		Deployment:       *deployment,
		Address:          *address,
		OutageAddress:    *outageAddress,
		AdminToken:       strings.TrimSpace(os.Getenv("BAO_TOKEN")),
		KubernetesJWT:    strings.TrimSpace(os.Getenv("BAO_KUBERNETES_JWT")),
		KubernetesRole:   *kubernetesRole,
		KubernetesClient: *kubernetesClient,
		TransitKeyPath:   *transitKeyPath,
		AuditDevice:      *auditDevice,
	})
	if err != nil && report.ReportKind == "" {
		_, _ = fmt.Fprintf(stderr, "run OpenBao smoke: %v\n", err)
		return 1
	}
	if writeErr := writeReport(*outPath, stdout, report); writeErr != nil {
		_, _ = fmt.Fprintf(stderr, "write OpenBao smoke evidence: %v\n", writeErr)
		return 1
	}
	if err != nil {
		if errors.Is(err, openbaosmoke.ErrSmokeFailed) {
			_, _ = fmt.Fprintf(stderr, "OpenBao smoke failed; evidence report was written\n")
			return 1
		}
		_, _ = fmt.Fprintf(stderr, "run OpenBao smoke: %v\n", err)
		return 1
	}
	return 0
}

func writeReport(path string, stdout io.Writer, report openbaosmoke.Report) error {
	out := stdout
	var file *os.File
	if path != "" {
		// #nosec G304 -- --out is an explicit operator-selected evidence output path.
		created, err := os.Create(path)
		if err != nil {
			return err
		}
		file = created
		out = created
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	err := encoder.Encode(report)
	if closeErr := closeFile(file); err == nil {
		err = closeErr
	}
	return err
}

func closeFile(file *os.File) error {
	if file == nil {
		return nil
	}
	return file.Close()
}

func releaseSHAValue(flagValue string) string {
	if value := strings.TrimSpace(flagValue); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("SCRAP_RELEASE_SHA")); value != "" {
		return value
	}
	return gitOutput("rev-parse", "HEAD")
}

func dirtyTreeValue(flagValue string) string {
	if value := strings.TrimSpace(flagValue); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("SCRAP_DIRTY_TREE")); value != "" {
		return value
	}
	if gitOutput("rev-parse", "--is-inside-work-tree") == "unknown" {
		return "unknown"
	}
	if runGit("diff", "--quiet") == nil && runGit("diff", "--cached", "--quiet") == nil {
		return "clean"
	}
	return "dirty"
}

func gitOutput(args ...string) string {
	// #nosec G204 -- command and arguments are fixed by callers in this package.
	out, err := exec.CommandContext(context.Background(), "git", args...).Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func runGit(args ...string) error {
	// #nosec G204 -- command and arguments are fixed by callers in this package.
	return exec.CommandContext(context.Background(), "git", args...).Run()
}
