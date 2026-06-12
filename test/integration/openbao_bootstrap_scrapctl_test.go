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
	rootToken := "scrap-bootstrap-integration-token"
	openBao, err := testcontainers.Run(ctx, scrapopenbao.DefaultImage,
		testcontainers.WithExposedPorts("8200/tcp"),
		testcontainers.WithEnv(map[string]string{
			"BAO_DEV_LISTEN_ADDRESS": "0.0.0.0:8200",
			"BAO_DEV_ROOT_TOKEN_ID":  rootToken,
			"BAO_LOCAL_CONFIG":       `{"disable_mlock":true}`,
		}),
		testcontainers.WithWaitStrategy(
			wait.ForHTTP("/v1/sys/health").
				WithPort("8200/tcp").
				WithStatusCodeMatcher(func(status int) bool { return status == http.StatusOK }).
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

	t.Setenv("OPENBAO_BOOTSTRAP_TOKEN", rootToken)
	evidencePath := filepath.Join(t.TempDir(), "openbao-bootstrap-evidence.json")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = scrapctl.Run([]string{
		"openbao", "bootstrap",
		"--address=" + address,
		"--token-env=OPENBAO_BOOTSTRAP_TOKEN",
		"--evidence-path=" + evidencePath,
		"--output=json",
	}, &stdout, &stderr, scrapctl.Deps{})
	if err != nil {
		t.Fatalf("scrapctl openbao bootstrap: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	assertBootstrapOutputRedacted(t, stdout.String(), stderr.String(), evidencePath, rootToken)
	assertOpenBaoBootstrapCreatedTransitKey(t, address, rootToken)
}

func assertBootstrapOutputRedacted(t *testing.T, stdout, stderr, evidencePath, token string) {
	t.Helper()

	for _, stream := range []string{stdout, stderr, string(mustReadIntegrationFile(t, evidencePath))} {
		if strings.Contains(stream, token) {
			t.Fatalf("bootstrap output leaked token in:\n%s", stream)
		}
	}
	var report struct {
		Status          string `json:"status"`
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
	if report.Status != "ok" || report.TransitMount != "transit" || report.TransitKey != "scrap-documents" || report.EvidencePath != evidencePath {
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
