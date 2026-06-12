package scrapctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenBaoBootstrapFreshInitializedTargetWritesEvidence(t *testing.T) {
	client := newFakeOpenBaoBootstrapClient()
	client.initialized = true
	client.seal = openBaoSealStatus{Initialized: true, Sealed: false}
	t.Setenv("BAO_TOKEN", "bao-token-secret")
	evidencePath := filepath.Join(t.TempDir(), "bootstrap-evidence.json")

	var out bytes.Buffer
	err := Run([]string{
		"openbao", "bootstrap",
		"--address=http://127.0.0.1:8200/v1?ignored=true",
		"--evidence-path=" + evidencePath,
	}, &out, io.Discard, Deps{OpenBaoClientFactory: client.factory})
	if err != nil {
		t.Fatalf("openbao bootstrap: %v\n%s", err, out.String())
	}

	if !client.mounted || !client.createdKey {
		t.Fatalf("mounted=%t createdKey=%t, want both true", client.mounted, client.createdKey)
	}
	assertTextContains(t, out.String(),
		"status: ok",
		"openbao_endpoint: http://127.0.0.1:8200",
		"transit_mount: transit",
		"transit_key: scrap-documents",
		"evidence_path: "+evidencePath,
	)
	assertTextNotContains(t, out.String(), "bao-token-secret")

	report := readOpenBaoBootstrapReport(t, evidencePath)
	if report.Status != "ok" || report.EvidencePath != evidencePath {
		t.Fatalf("report = %+v", report)
	}
	for _, forbidden := range []string{"bao-token-secret", "ignored=true"} {
		assertTextNotContains(t, string(mustReadFile(t, evidencePath)), forbidden)
	}
}

func TestOpenBaoBootstrapInitializesAndWritesSecretFile0600(t *testing.T) {
	client := newFakeOpenBaoBootstrapClient()
	client.initialized = false
	client.seal = openBaoSealStatus{Initialized: true, Sealed: true}
	rootToken := "root-token-secret" //nolint:gosec // fixture secret used to prove redaction.
	unsealKey := "unseal-key-secret"
	client.initResult = openBaoInitResult{
		RootToken: rootToken,
		KeysB64:   []string{unsealKey},
	}
	evidencePath := filepath.Join(t.TempDir(), "bootstrap-evidence.json")
	secretsPath := filepath.Join(t.TempDir(), "init-secrets.json")

	var out bytes.Buffer
	err := Run([]string{
		"openbao", "bootstrap",
		"--address=http://127.0.0.1:8200",
		"--init",
		"--init-secrets-path=" + secretsPath,
		"--evidence-path=" + evidencePath,
	}, &out, io.Discard, Deps{OpenBaoClientFactory: client.factory})
	if err != nil {
		t.Fatalf("openbao bootstrap init: %v\n%s", err, out.String())
	}

	data := string(mustReadFile(t, secretsPath))
	assertTextContains(t, data, "root-token-secret", "unseal-key-secret")
	info, err := os.Stat(secretsPath)
	if err != nil {
		t.Fatalf("stat secrets file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("secret file mode = %o, want 0600", got)
	}
	if len(client.tokens) == 0 || client.tokens[len(client.tokens)-1] != rootToken {
		t.Fatalf("client tokens = %+v, want root token set after init", client.tokens)
	}
	if got, want := client.unsealKeys, []string{unsealKey}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("unseal keys = %+v, want %+v", got, want)
	}
	if !client.mounted || !client.createdKey {
		t.Fatalf("mounted=%t createdKey=%t, want both true", client.mounted, client.createdKey)
	}
	for _, forbidden := range []string{rootToken, unsealKey} {
		assertTextNotContains(t, out.String(), forbidden)
		assertTextNotContains(t, string(mustReadFile(t, evidencePath)), forbidden)
	}
}

func TestOpenBaoBootstrapUnsealsFromEnvironmentWithoutLeakingShares(t *testing.T) {
	client := newFakeOpenBaoBootstrapClient()
	client.initialized = true
	client.seal = openBaoSealStatus{Initialized: true, Sealed: true}
	client.unsealStatuses = []openBaoSealStatus{
		{Initialized: true, Sealed: true, Progress: 1},
		{Initialized: true, Sealed: false, Progress: 2},
	}
	t.Setenv("BAO_TOKEN", "bao-token-secret")
	t.Setenv("UNSEAL_ONE", "share-one-secret")
	t.Setenv("UNSEAL_TWO", "share-two-secret")
	evidencePath := filepath.Join(t.TempDir(), "bootstrap-evidence.json")

	var out bytes.Buffer
	err := Run([]string{
		"openbao", "bootstrap",
		"--address=http://127.0.0.1:8200",
		"--unseal-key-env=UNSEAL_ONE",
		"--unseal-key-env=UNSEAL_TWO",
		"--evidence-path=" + evidencePath,
	}, &out, io.Discard, Deps{OpenBaoClientFactory: client.factory})
	if err != nil {
		t.Fatalf("openbao bootstrap unseal: %v\n%s", err, out.String())
	}

	if got, want := client.unsealKeys, []string{"share-one-secret", "share-two-secret"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("unseal keys = %+v, want %+v", got, want)
	}
	for _, forbidden := range []string{"share-one-secret", "share-two-secret", "bao-token-secret"} {
		assertTextNotContains(t, out.String(), forbidden)
		assertTextNotContains(t, string(mustReadFile(t, evidencePath)), forbidden)
	}
}

func TestOpenBaoBootstrapRejectsMissingTokenForExistingTarget(t *testing.T) {
	client := newFakeOpenBaoBootstrapClient()
	client.initialized = true
	client.seal = openBaoSealStatus{Initialized: true, Sealed: false}
	evidencePath := filepath.Join(t.TempDir(), "bootstrap-evidence.json")

	var stderr bytes.Buffer
	err := Run([]string{
		"openbao", "bootstrap",
		"--address=http://127.0.0.1:8200",
		"--evidence-path=" + evidencePath,
	}, io.Discard, &stderr, Deps{OpenBaoClientFactory: client.factory})
	if err == nil || !strings.Contains(err.Error(), "token env BAO_TOKEN is required") {
		t.Fatalf("error = %v, want missing token env", err)
	}
	assertTextContains(t, stderr.String(), "token env BAO_TOKEN is required")
	report := readOpenBaoBootstrapReport(t, evidencePath)
	assertReportHasRedactionPasses(t, report, "stdout", "stderr", "report", "artifact", "logs")
	assertTextContains(t, strings.Join(report.SanitizedArgs, "\n"), "--token-env=BAO_TOKEN")
	assertTextContains(t, strings.Join(report.EnvVarsUsed, "\n"), "BAO_TOKEN")
	if report.Dependency.Version == "" {
		t.Fatal("dependency version is empty")
	}
}

func TestOpenBaoBootstrapRejectsRawSecretFlagsWithoutLeakingValue(t *testing.T) {
	var out bytes.Buffer
	var stderr bytes.Buffer
	err := Run([]string{
		"openbao", "bootstrap",
		"--token=raw-secret-value",
	}, &out, &stderr, Deps{})
	if err == nil {
		t.Fatal("openbao bootstrap should reject raw token flag")
	}
	for _, stream := range []string{out.String(), stderr.String(), err.Error()} {
		assertTextNotContains(t, stream, "raw-secret-value")
	}
}

func TestOpenBaoBootstrapRejectsAddressUserinfoWithoutLeakingValue(t *testing.T) {
	var out bytes.Buffer
	var stderr bytes.Buffer
	err := Run([]string{
		"openbao", "bootstrap",
		"--address=http://operator:raw-secret-value@127.0.0.1:8200",
		"--evidence-path=" + filepath.Join(t.TempDir(), "evidence.json"),
	}, &out, &stderr, Deps{})
	if err == nil || !strings.Contains(err.Error(), "openbao address is invalid") {
		t.Fatalf("error = %v, want invalid address", err)
	}
	for _, stream := range []string{out.String(), stderr.String(), err.Error()} {
		assertTextNotContains(t, stream, "raw-secret-value")
	}
}

func TestOpenBaoBootstrapRejectsInitEvidencePathCollision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bootstrap.json")
	err := Run([]string{
		"openbao", "bootstrap",
		"--address=http://127.0.0.1:8200",
		"--init",
		"--init-secrets-path=" + path,
		"--evidence-path=" + path,
	}, io.Discard, io.Discard, Deps{})
	if err == nil || !strings.Contains(err.Error(), "evidence-path and init-secrets-path must be different") {
		t.Fatalf("error = %v, want path collision", err)
	}
}

func TestOpenBaoBootstrapRejectsControlCharacterEvidencePath(t *testing.T) {
	err := Run([]string{
		"openbao", "bootstrap",
		"--address=http://127.0.0.1:8200",
		"--evidence-path=bootstrap\nevidence.json",
	}, io.Discard, io.Discard, Deps{})
	if err == nil || !strings.Contains(err.Error(), "evidence-path is invalid") {
		t.Fatalf("error = %v, want invalid evidence path", err)
	}
}

func TestOpenBaoBootstrapRejectsAdminTLSFlags(t *testing.T) {
	err := Run([]string{
		"openbao", "bootstrap",
		"--address=http://127.0.0.1:8200",
		"--evidence-path=" + filepath.Join(t.TempDir(), "evidence.json"),
		"--tls-cert=client.pem",
	}, io.Discard, io.Discard, Deps{})
	if err == nil || !strings.Contains(err.Error(), "admin/public TLS flags do not configure OpenBao") {
		t.Fatalf("error = %v, want TLS boundary rejection", err)
	}
}

func TestOpenBaoBootstrapRewritesExistingEvidenceFile0600(t *testing.T) {
	client := newFakeOpenBaoBootstrapClient()
	client.initialized = true
	client.seal = openBaoSealStatus{Initialized: true, Sealed: false}
	t.Setenv("BAO_TOKEN", "bao-token-secret")
	evidencePath := filepath.Join(t.TempDir(), "bootstrap-evidence.json")
	if err := os.WriteFile(evidencePath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("seed evidence file: %v", err)
	}
	if err := os.Chmod(evidencePath, 0o666); err != nil { //nolint:gosec // test seeds a permissive existing evidence file to prove the command tightens it.
		t.Fatalf("chmod evidence file: %v", err)
	}

	err := Run([]string{
		"openbao", "bootstrap",
		"--address=http://127.0.0.1:8200",
		"--evidence-path=" + evidencePath,
	}, io.Discard, io.Discard, Deps{OpenBaoClientFactory: client.factory})
	if err != nil {
		t.Fatalf("openbao bootstrap: %v", err)
	}
	info, err := os.Stat(evidencePath)
	if err != nil {
		t.Fatalf("stat evidence: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("evidence file mode = %o, want 0600", got)
	}
}

func TestOpenBaoBootstrapRejectsExistingKeyWithMissingMetadata(t *testing.T) {
	client := newFakeOpenBaoBootstrapClient()
	client.initialized = true
	client.seal = openBaoSealStatus{Initialized: true, Sealed: false}
	client.key = &openBaoTransitKeyStatus{
		Derived:        true,
		DerivedPresent: true,
		LatestVersion:  1,
	}
	t.Setenv("BAO_TOKEN", "bao-token-secret")
	err := Run([]string{
		"openbao", "bootstrap",
		"--address=http://127.0.0.1:8200",
		"--evidence-path=" + filepath.Join(t.TempDir(), "evidence.json"),
	}, io.Discard, io.Discard, Deps{OpenBaoClientFactory: client.factory})
	if err == nil || !strings.Contains(err.Error(), "existing transit key type is incompatible") {
		t.Fatalf("error = %v, want incompatible key metadata", err)
	}
}

type fakeOpenBaoBootstrapClient struct {
	config         openBaoClientConfig
	initialized    bool
	seal           openBaoSealStatus
	initResult     openBaoInitResult
	unsealStatuses []openBaoSealStatus
	mounts         map[string]openBaoMount
	key            *openBaoTransitKeyStatus
	tokens         []string
	unsealKeys     []string
	mounted        bool
	createdKey     bool
}

func newFakeOpenBaoBootstrapClient() *fakeOpenBaoBootstrapClient {
	return &fakeOpenBaoBootstrapClient{
		mounts: map[string]openBaoMount{},
		key:    nil,
	}
}

func (f *fakeOpenBaoBootstrapClient) factory(cfg openBaoClientConfig) (openBaoBootstrapClient, error) {
	f.config = cfg
	if cfg.Token != "" {
		f.tokens = append(f.tokens, cfg.Token)
	}
	return f, nil
}

func (f *fakeOpenBaoBootstrapClient) SetToken(token string) {
	f.tokens = append(f.tokens, token)
}

func (f *fakeOpenBaoBootstrapClient) InitStatus(context.Context) (bool, error) {
	return f.initialized, nil
}

func (f *fakeOpenBaoBootstrapClient) Init(context.Context, openBaoInitRequest) (openBaoInitResult, error) {
	if f.initialized {
		return openBaoInitResult{}, errors.New("already initialized")
	}
	f.initialized = true
	return f.initResult, nil
}

func (f *fakeOpenBaoBootstrapClient) SealStatus(context.Context) (openBaoSealStatus, error) {
	return f.seal, nil
}

func (f *fakeOpenBaoBootstrapClient) Unseal(_ context.Context, key string) (openBaoSealStatus, error) {
	f.unsealKeys = append(f.unsealKeys, key)
	if len(f.unsealStatuses) == 0 {
		f.seal.Sealed = false
		return f.seal, nil
	}
	status := f.unsealStatuses[0]
	f.unsealStatuses = f.unsealStatuses[1:]
	f.seal = status
	return status, nil
}

func (f *fakeOpenBaoBootstrapClient) ListMounts(context.Context) (map[string]openBaoMount, error) {
	return f.mounts, nil
}

func (f *fakeOpenBaoBootstrapClient) MountTransit(_ context.Context, mountPath string) error {
	f.mounted = true
	f.mounts[mountPath+"/"] = openBaoMount{Type: "transit"}
	return nil
}

func (f *fakeOpenBaoBootstrapClient) ReadTransitKey(context.Context, string, string) (openBaoTransitKeyStatus, error) {
	if f.key == nil {
		return openBaoTransitKeyStatus{}, errOpenBaoKeyMissing
	}
	return *f.key, nil
}

func (f *fakeOpenBaoBootstrapClient) CreateTransitKey(_ context.Context, _, _, keyType string, derived bool) error {
	f.createdKey = true
	f.key = &openBaoTransitKeyStatus{
		Type:           keyType,
		TypePresent:    true,
		Derived:        derived,
		DerivedPresent: true,
		LatestVersion:  1,
	}
	return nil
}

func readOpenBaoBootstrapReport(t *testing.T, path string) openBaoBootstrapReport {
	t.Helper()

	var report openBaoBootstrapReport
	if err := json.Unmarshal(mustReadFile(t, path), &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	return report
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // tests read files created under t.TempDir.
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func assertReportHasRedactionPasses(t *testing.T, report openBaoBootstrapReport, surfaces ...string) {
	t.Helper()

	seen := map[string]string{}
	for _, check := range report.RedactionChecks {
		seen[check.Surface] = check.Status
	}
	for _, surface := range surfaces {
		if seen[surface] != "pass" {
			t.Fatalf("redaction check %s = %q, want pass in %+v", surface, seen[surface], report.RedactionChecks)
		}
	}
}
