//go:build integration

package integration_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	baoapi "github.com/openbao/openbao/api"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/petabytecl/scrap/internal/scrapctl"
	"github.com/petabytecl/scrap/test/integration/testinfra"
	scrapopenbao "github.com/petabytecl/scrap/test/integration/testinfra/openbao"
)

//goland:noinspection ALL
func TestIntegrationScrapctlOpenBaoBootstrapFreshSetup(t *testing.T) {
	address := startUninitializedOpenBaoServer(t)
	outputDir := t.TempDir()
	evidencePath := filepath.Join(outputDir, "openbao-bootstrap-evidence.json")
	initSecretsPath := filepath.Join(outputDir, "openbao-init-secrets.json")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := scrapctl.Run([]string{
		"openbao", "bootstrap",
		"--address=" + address,
		"--init",
		"--init-secrets-path=" + initSecretsPath,
		"--evidence-path=" + evidencePath,
		"--output=json",
	}, &stdout, &stderr, scrapctl.Deps{})
	if err != nil {
		t.Fatalf("scrapctl openbao bootstrap: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	rootToken, forbiddenValues := readIntegrationInitSecrets(t, initSecretsPath)
	assertBootstrapOutputRedacted(t, stdout.String(), stderr.String(), evidencePath, forbiddenValues...)
	report := readIntegrationBootstrapReport(t, evidencePath)
	if report.Status != "ok" || !report.InitPerformed || report.TransitMount != "transit" || report.TransitKey != "scrap-documents" || report.EvidencePath != evidencePath {
		t.Fatalf("report = %+v", report)
	}
	assertOpenBaoBootstrapCreatedTransitKey(t, address, rootToken)
}

func TestIntegrationScrapctlOpenBaoBootstrapCompatibleRerun(t *testing.T) {
	address := startUninitializedOpenBaoServer(t)
	outputDir := t.TempDir()
	firstEvidencePath := filepath.Join(outputDir, "openbao-bootstrap-first.json")
	initSecretsPath := filepath.Join(outputDir, "openbao-init-secrets.json")

	var firstStdout bytes.Buffer
	var firstStderr bytes.Buffer
	err := scrapctl.Run([]string{
		"openbao", "bootstrap",
		"--address=" + address,
		"--init",
		"--init-secrets-path=" + initSecretsPath,
		"--evidence-path=" + firstEvidencePath,
		"--output=json",
	}, &firstStdout, &firstStderr, scrapctl.Deps{})
	if err != nil {
		t.Fatalf("first scrapctl openbao bootstrap: %v\nstdout:\n%s\nstderr:\n%s", err, firstStdout.String(), firstStderr.String())
	}
	rootToken, forbiddenValues := readIntegrationInitSecrets(t, initSecretsPath)
	before := readOpenBaoTransitMetadata(t, address, rootToken)

	t.Setenv("BAO_TOKEN", rootToken)
	secondEvidencePath := filepath.Join(outputDir, "openbao-bootstrap-rerun.json")
	var secondStdout bytes.Buffer
	var secondStderr bytes.Buffer
	err = scrapctl.Run([]string{
		"openbao", "bootstrap",
		"--address=" + address,
		"--evidence-path=" + secondEvidencePath,
		"--output=json",
	}, &secondStdout, &secondStderr, scrapctl.Deps{})
	if err != nil {
		t.Fatalf("second scrapctl openbao bootstrap: %v\nstdout:\n%s\nstderr:\n%s", err, secondStdout.String(), secondStderr.String())
	}

	after := readOpenBaoTransitMetadata(t, address, rootToken)
	if after != before {
		t.Fatalf("transit metadata changed after compatible rerun: before=%+v after=%+v", before, after)
	}
	report := readIntegrationBootstrapReport(t, secondEvidencePath)
	if report.Status != "ok" || report.InitPerformed {
		t.Fatalf("second report = %+v, want ok without init", report)
	}
	assertIntegrationPhase(t, report, "mount", "ok", "transit mount verified")
	assertIntegrationPhase(t, report, "key", "ok", "transit key verified")
	assertBootstrapOutputRedacted(t, secondStdout.String(), secondStderr.String(), secondEvidencePath, forbiddenValues...)
}

func TestIntegrationScrapctlOpenBaoBootstrapIncompatibleStateDoesNotMutate(t *testing.T) {
	address := startUninitializedOpenBaoServer(t)
	outputDir := t.TempDir()
	seedEvidencePath := filepath.Join(outputDir, "openbao-bootstrap-seed.json")
	initSecretsPath := filepath.Join(outputDir, "openbao-init-secrets.json")

	var seedStdout bytes.Buffer
	var seedStderr bytes.Buffer
	err := scrapctl.Run([]string{
		"openbao", "bootstrap",
		"--address=" + address,
		"--init",
		"--init-secrets-path=" + initSecretsPath,
		"--evidence-path=" + seedEvidencePath,
		"--key-type=chacha20-poly1305",
		"--output=json",
	}, &seedStdout, &seedStderr, scrapctl.Deps{})
	if err != nil {
		t.Fatalf("seed scrapctl openbao bootstrap: %v\nstdout:\n%s\nstderr:\n%s", err, seedStdout.String(), seedStderr.String())
	}
	rootToken, forbiddenValues := readIntegrationInitSecrets(t, initSecretsPath)
	before := readOpenBaoTransitMetadata(t, address, rootToken)
	if before.KeyType != "chacha20-poly1305" || !before.KeyDerived {
		t.Fatalf("seed metadata = %+v, want incompatible chacha20-poly1305 derived key", before)
	}

	t.Setenv("BAO_TOKEN", rootToken)
	incompatibleEvidencePath := filepath.Join(outputDir, "openbao-bootstrap-incompatible.json")
	var incompatibleStdout bytes.Buffer
	var incompatibleStderr bytes.Buffer
	err = scrapctl.Run([]string{
		"openbao", "bootstrap",
		"--address=" + address,
		"--evidence-path=" + incompatibleEvidencePath,
		"--output=json",
	}, &incompatibleStdout, &incompatibleStderr, scrapctl.Deps{})
	if err == nil || !strings.Contains(err.Error(), "existing transit key type is incompatible") {
		t.Fatalf("incompatible bootstrap error = %v, want key type incompatibility\nstdout:\n%s\nstderr:\n%s", err, incompatibleStdout.String(), incompatibleStderr.String())
	}

	after := readOpenBaoTransitMetadata(t, address, rootToken)
	if after != before {
		t.Fatalf("transit metadata changed after incompatible rerun: before=%+v after=%+v", before, after)
	}
	report := readIntegrationBootstrapReport(t, incompatibleEvidencePath)
	if report.Status != "fail" {
		t.Fatalf("incompatible report status = %q, want fail", report.Status)
	}
	assertIntegrationPhase(t, report, "mount", "ok", "transit mount verified")
	assertIntegrationPhase(t, report, "key", "fail", "existing transit key type is incompatible")
	assertBootstrapOutputRedacted(t, incompatibleStdout.String(), incompatibleStderr.String(), incompatibleEvidencePath, forbiddenValues...)
}

type integrationBootstrapReport struct {
	Status          string `json:"status"`
	InitPerformed   bool   `json:"init_performed"`
	TransitMount    string `json:"transit_mount"`
	TransitKey      string `json:"transit_key"`
	EvidencePath    string `json:"evidence_path"`
	RedactionChecks []struct {
		Surface string `json:"surface"`
		Status  string `json:"status"`
	} `json:"redaction_checks"`
	Phases []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
		Reason string `json:"reason"`
	} `json:"phases"`
}

type integrationTransitMetadata struct {
	MountType     string
	KeyType       string
	KeyDerived    bool
	LatestVersion int
}

func startUninitializedOpenBaoServer(t *testing.T) string {
	t.Helper()

	ctx := integrationTestContext(t)
	config := `
storage "file" {
  path = "/tmp/openbao-data"
}

listener "tcp" {
  address = "0.0.0.0:8200"
  tls_disable = true
}

disable_mlock = true
`
	openBao, err := testcontainers.Run(ctx, scrapopenbao.DefaultImage,
		testcontainers.WithExposedPorts("8200/tcp"),
		testcontainers.WithCmd("server", "-config=/tmp/openbao.hcl"),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			Reader:            strings.NewReader(config),
			ContainerFilePath: "/tmp/openbao.hcl",
			FileMode:          0o644,
		}),
		testcontainers.WithWaitStrategy(
			wait.ForHTTP("/v1/sys/health").
				WithPort("8200/tcp").
				WithStatusCodeMatcher(func(status int) bool {
					return status == http.StatusOK || status == http.StatusNotImplemented
				}).
				WithStartupTimeout(2*time.Minute),
		),
	)
	if openBao != nil {
		testinfra.CleanupContainer(t, openBao)
	}
	if err != nil {
		t.Fatalf("start OpenBao testcontainer: %v", err)
	}
	address, err := openBao.PortEndpoint(ctx, "8200", "http")
	if err != nil {
		t.Fatalf("OpenBao endpoint: %v", err)
	}
	return address
}

func assertBootstrapOutputRedacted(t *testing.T, stdout, stderr, evidencePath string, forbiddenValues ...string) {
	t.Helper()

	for _, stream := range []string{stdout, stderr, string(mustReadIntegrationFile(t, evidencePath))} {
		for _, value := range forbiddenValues {
			if strings.Contains(stream, value) {
				t.Fatalf("bootstrap output leaked secret material in:\n%s", stream)
			}
		}
	}
	var report integrationBootstrapReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("decode bootstrap stdout: %v\n%s", err, stdout)
	}
	for _, check := range report.RedactionChecks {
		if check.Status != "pass" {
			t.Fatalf("redaction check failed: %+v", check)
		}
	}
}

func readIntegrationBootstrapReport(t *testing.T, path string) integrationBootstrapReport {
	t.Helper()

	var report integrationBootstrapReport
	if err := json.Unmarshal(mustReadIntegrationFile(t, path), &report); err != nil {
		t.Fatalf("decode bootstrap report: %v", err)
	}
	return report
}

func assertIntegrationPhase(t *testing.T, report integrationBootstrapReport, name, status, reason string) {
	t.Helper()

	for _, phase := range report.Phases {
		if phase.Name != name {
			continue
		}
		if phase.Status != status || phase.Reason != reason {
			t.Fatalf("phase %s = status %q reason %q, want status %q reason %q", name, phase.Status, phase.Reason, status, reason)
		}
		return
	}
	t.Fatalf("phase %s not found in %+v", name, report.Phases)
}

func readOpenBaoTransitMetadata(t *testing.T, address, token string) integrationTransitMetadata {
	t.Helper()

	cfg := baoapi.DefaultConfig()
	cfg.Address = address
	cfg.Timeout = 10 * time.Second
	cfg.MaxRetries = 0
	client, err := baoapi.NewClient(cfg)
	if err != nil {
		t.Fatalf("OpenBao client: %v", err)
	}
	client.SetToken(token)
	mounts, err := client.Sys().ListMounts()
	if err != nil {
		t.Fatalf("list mounts: %v", err)
	}
	mount := mounts["transit/"]
	if mount == nil {
		t.Fatal("transit mount missing")
	}
	secret, err := client.Logical().Read("transit/keys/scrap-documents")
	if err != nil {
		t.Fatalf("read transit key: %v", err)
	}
	if secret == nil || secret.Data == nil {
		t.Fatal("transit key response missing data")
	}
	keyType, _ := secret.Data["type"].(string)
	keyDerived, _ := secret.Data["derived"].(bool)
	return integrationTransitMetadata{
		MountType:     mount.Type,
		KeyType:       keyType,
		KeyDerived:    keyDerived,
		LatestVersion: integrationIntValue(secret.Data["latest_version"]),
	}
}

func integrationIntValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case json.Number:
		out, _ := typed.Int64()
		return int(out)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func assertOpenBaoBootstrapCreatedTransitKey(t *testing.T, address, token string) {
	t.Helper()

	cfg := baoapi.DefaultConfig()
	cfg.Address = address
	cfg.Timeout = 10 * time.Second
	cfg.MaxRetries = 0
	client, err := baoapi.NewClient(cfg)
	if err != nil {
		t.Fatalf("OpenBao client: %v", err)
	}
	client.SetToken(token)
	mounts, err := client.Sys().ListMounts()
	if err != nil {
		t.Fatalf("list mounts: %v", err)
	}
	if mounts["transit/"] == nil || mounts["transit/"].Type != "transit" {
		t.Fatalf("transit mount = %+v", mounts["transit/"])
	}
	secret, err := client.Logical().Read("transit/keys/scrap-documents")
	if err != nil {
		t.Fatalf("read transit key: %v", err)
	}
	if secret == nil || secret.Data == nil {
		t.Fatal("transit key response missing data")
	}
	if got := secret.Data["type"]; got != "aes256-gcm96" {
		t.Fatalf("key type = %v, want aes256-gcm96", got)
	}
	if got := secret.Data["derived"]; got != true {
		t.Fatalf("key derived = %v, want true", got)
	}
}

func mustReadIntegrationFile(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // tests read files created under t.TempDir.
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func readIntegrationInitSecrets(t *testing.T, path string) (string, []string) {
	t.Helper()

	var secrets struct {
		RootToken string   `json:"root_token"`
		KeysB64   []string `json:"keys_base64"`
	}
	if err := json.Unmarshal(mustReadIntegrationFile(t, path), &secrets); err != nil {
		t.Fatalf("decode init secrets: %v", err)
	}
	if secrets.RootToken == "" || len(secrets.KeysB64) == 0 {
		t.Fatalf("init secrets missing root token or unseal keys: %+v", secrets)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat init secrets: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("init secrets mode = %o, want 0600", got)
	}
	forbiddenValues := append([]string{secrets.RootToken}, secrets.KeysB64...)
	return secrets.RootToken, forbiddenValues
}
