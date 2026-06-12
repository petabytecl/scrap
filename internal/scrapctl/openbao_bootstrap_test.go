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

func TestOpenBaoBootstrapCompatibleRerunDoesNotMutateMountOrKey(t *testing.T) {
	client := newFakeOpenBaoBootstrapClient()
	client.initialized = true
	client.seal = openBaoSealStatus{Initialized: true, Sealed: false}
	client.mounts["transit/"] = openBaoMount{Type: "transit"}
	client.key = &openBaoTransitKeyStatus{
		Type:           "aes256-gcm96",
		TypePresent:    true,
		Derived:        true,
		DerivedPresent: true,
		LatestVersion:  1,
	}
	t.Setenv("BAO_TOKEN", "bao-token-secret")
	evidencePath := filepath.Join(t.TempDir(), "bootstrap-evidence.json")

	var out bytes.Buffer
	var stderr bytes.Buffer
	err := Run([]string{
		"openbao", "bootstrap",
		"--address=http://127.0.0.1:8200",
		"--evidence-path=" + evidencePath,
		"--output=json",
	}, &out, &stderr, Deps{OpenBaoClientFactory: client.factory})
	if err != nil {
		t.Fatalf("openbao bootstrap compatible rerun: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), stderr.String())
	}

	report := readOpenBaoBootstrapReport(t, evidencePath)
	if report.Status != "ok" {
		t.Fatalf("status = %q, want ok", report.Status)
	}
	assertOpenBaoPhase(t, report, "mount", "ok", "transit mount verified")
	assertOpenBaoPhase(t, report, "key", "ok", "transit key verified")
	assertReportHasRedactionPasses(t, report, "stdout", "stderr", "report", "artifact", "logs")
	if client.mountTransitCalls != 0 {
		t.Fatalf("mount transit calls = %d, want 0", client.mountTransitCalls)
	}
	if client.createTransitKeyCalls != 0 {
		t.Fatalf("create transit key calls = %d, want 0", client.createTransitKeyCalls)
	}
	if client.listMountsCalls != 1 {
		t.Fatalf("list mounts calls = %d, want 1", client.listMountsCalls)
	}
	if client.readTransitKeyCalls != 1 {
		t.Fatalf("read transit key calls = %d, want 1", client.readTransitKeyCalls)
	}
	for _, forbidden := range []string{"bao-token-secret"} {
		assertTextNotContains(t, out.String(), forbidden)
		assertTextNotContains(t, stderr.String(), forbidden)
		assertTextNotContains(t, string(mustReadFile(t, evidencePath)), forbidden)
	}
}

func TestOpenBaoBootstrapRejectsIncompatibleMountWithoutMutation(t *testing.T) {
	client := newFakeOpenBaoBootstrapClient()
	client.initialized = true
	client.seal = openBaoSealStatus{Initialized: true, Sealed: false}
	client.mounts["transit/"] = openBaoMount{Type: "kv"}
	t.Setenv("BAO_TOKEN", "bao-token-secret")
	evidencePath := filepath.Join(t.TempDir(), "bootstrap-evidence.json")

	var stderr bytes.Buffer
	err := Run([]string{
		"openbao", "bootstrap",
		"--address=http://127.0.0.1:8200",
		"--evidence-path=" + evidencePath,
	}, io.Discard, &stderr, Deps{OpenBaoClientFactory: client.factory})
	if err == nil || !strings.Contains(err.Error(), "existing mount is not transit") {
		t.Fatalf("error = %v, want incompatible mount", err)
	}

	report := readOpenBaoBootstrapReport(t, evidencePath)
	if report.Status != "fail" {
		t.Fatalf("status = %q, want fail", report.Status)
	}
	assertOpenBaoPhase(t, report, "mount", "fail", "existing mount is not transit")
	if client.mountTransitCalls != 0 {
		t.Fatalf("mount transit calls = %d, want 0", client.mountTransitCalls)
	}
	if client.readTransitKeyCalls != 0 {
		t.Fatalf("read transit key calls = %d, want 0", client.readTransitKeyCalls)
	}
	if client.createTransitKeyCalls != 0 {
		t.Fatalf("create transit key calls = %d, want 0", client.createTransitKeyCalls)
	}
	assertReportHasRedactionPasses(t, report, "stdout", "stderr", "report", "artifact", "logs")
	assertTextNotContains(t, stderr.String(), "bao-token-secret")
}

func TestOpenBaoBootstrapRejectsIncompatibleExistingKeyWithoutRepair(t *testing.T) {
	tests := []struct {
		name       string
		key        openBaoTransitKeyStatus
		wantReason string
	}{
		{
			name: "wrong type",
			key: openBaoTransitKeyStatus{
				Type:           "chacha20-poly1305",
				TypePresent:    true,
				Derived:        true,
				DerivedPresent: true,
				LatestVersion:  1,
			},
			wantReason: "existing transit key type is incompatible",
		},
		{
			name: "derived mismatch",
			key: openBaoTransitKeyStatus{
				Type:           "aes256-gcm96",
				TypePresent:    true,
				Derived:        false,
				DerivedPresent: true,
				LatestVersion:  1,
			},
			wantReason: "existing transit key derivation setting is incompatible",
		},
		{
			name: "missing type metadata",
			key: openBaoTransitKeyStatus{
				Derived:        true,
				DerivedPresent: true,
				LatestVersion:  1,
			},
			wantReason: "existing transit key type is incompatible",
		},
		{
			name: "missing derived metadata",
			key: openBaoTransitKeyStatus{
				Type:          "aes256-gcm96",
				TypePresent:   true,
				LatestVersion: 1,
			},
			wantReason: "existing transit key derivation setting is incompatible",
		},
		{
			name: "invalid latest version",
			key: openBaoTransitKeyStatus{
				Type:           "aes256-gcm96",
				TypePresent:    true,
				Derived:        true,
				DerivedPresent: true,
			},
			wantReason: "transit key latest version is invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertOpenBaoBootstrapRejectsExistingKey(t, tt.key, tt.wantReason)
		})
	}
}

func assertOpenBaoBootstrapRejectsExistingKey(t *testing.T, key openBaoTransitKeyStatus, wantReason string) {
	t.Helper()

	client := newFakeOpenBaoBootstrapClient()
	client.initialized = true
	client.seal = openBaoSealStatus{Initialized: true, Sealed: false}
	client.mounts["transit/"] = openBaoMount{Type: "transit"}
	client.key = &key
	t.Setenv("BAO_TOKEN", "bao-token-secret")
	evidencePath := filepath.Join(t.TempDir(), "bootstrap-evidence.json")

	var stderr bytes.Buffer
	err := Run([]string{
		"openbao", "bootstrap",
		"--address=http://127.0.0.1:8200",
		"--evidence-path=" + evidencePath,
	}, io.Discard, &stderr, Deps{OpenBaoClientFactory: client.factory})
	if err == nil || !strings.Contains(err.Error(), wantReason) {
		t.Fatalf("error = %v, want %q", err, wantReason)
	}

	report := readOpenBaoBootstrapReport(t, evidencePath)
	if report.Status != "fail" {
		t.Fatalf("status = %q, want fail", report.Status)
	}
	assertOpenBaoPhase(t, report, "mount", "ok", "transit mount verified")
	assertOpenBaoPhase(t, report, "key", "fail", wantReason)
	if client.mountTransitCalls != 0 {
		t.Fatalf("mount transit calls = %d, want 0", client.mountTransitCalls)
	}
	if client.createTransitKeyCalls != 0 {
		t.Fatalf("create transit key calls = %d, want 0", client.createTransitKeyCalls)
	}
	if client.readTransitKeyCalls != 1 {
		t.Fatalf("read transit key calls = %d, want 1", client.readTransitKeyCalls)
	}
	assertReportHasRedactionPasses(t, report, "stdout", "stderr", "report", "artifact", "logs")
	assertTextNotContains(t, stderr.String(), "bao-token-secret")
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
	config                openBaoClientConfig
	initialized           bool
	seal                  openBaoSealStatus
	initResult            openBaoInitResult
	unsealStatuses        []openBaoSealStatus
	mounts                map[string]openBaoMount
	key                   *openBaoTransitKeyStatus
	tokens                []string
	unsealKeys            []string
	mounted               bool
	createdKey            bool
	listMountsCalls       int
	mountTransitCalls     int
	readTransitKeyCalls   int
	createTransitKeyCalls int
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
	f.listMountsCalls++
	return f.mounts, nil
}

func (f *fakeOpenBaoBootstrapClient) MountTransit(_ context.Context, mountPath string) error {
	f.mountTransitCalls++
	f.mounted = true
	f.mounts[mountPath+"/"] = openBaoMount{Type: "transit"}
	return nil
}

func (f *fakeOpenBaoBootstrapClient) ReadTransitKey(context.Context, string, string) (openBaoTransitKeyStatus, error) {
	f.readTransitKeyCalls++
	if f.key == nil {
		return openBaoTransitKeyStatus{}, errOpenBaoKeyMissing
	}
	return *f.key, nil
}

func (f *fakeOpenBaoBootstrapClient) CreateTransitKey(_ context.Context, _, _, keyType string, derived bool) error {
	f.createTransitKeyCalls++
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

func assertOpenBaoPhase(t *testing.T, report openBaoBootstrapReport, name, status, reason string) {
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
