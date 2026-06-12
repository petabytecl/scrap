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

	outputDir := t.TempDir()
	evidencePath := filepath.Join(outputDir, "openbao-bootstrap-evidence.json")
	initSecretsPath := filepath.Join(outputDir, "openbao-init-secrets.json")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = scrapctl.Run([]string{
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
	assertOpenBaoBootstrapCreatedTransitKey(t, address, rootToken)
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
	var report struct {
		Status          string `json:"status"`
		InitPerformed   bool   `json:"init_performed"`
		TransitMount    string `json:"transit_mount"`
		TransitKey      string `json:"transit_key"`
		EvidencePath    string `json:"evidence_path"`
		RedactionChecks []struct {
			Surface string `json:"surface"`
			Status  string `json:"status"`
		} `json:"redaction_checks"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("decode bootstrap stdout: %v\n%s", err, stdout)
	}
	if report.Status != "ok" || !report.InitPerformed || report.TransitMount != "transit" || report.TransitKey != "scrap-documents" || report.EvidencePath != evidencePath {
		t.Fatalf("report = %+v", report)
	}
	for _, check := range report.RedactionChecks {
		if check.Status != "pass" {
			t.Fatalf("redaction check failed: %+v", check)
		}
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
