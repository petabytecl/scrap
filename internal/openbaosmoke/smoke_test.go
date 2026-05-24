package openbaosmoke

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/testutil"
)

func TestRunRecordsRedactedKubernetesTransitEvidence(t *testing.T) {
	server := newFakeOpenBao(t, false)
	outage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(outage.Close)

	report, err := Run(context.Background(), testOptions(server.URL, outage.URL))
	requireRunReport(t, report, err)
	for _, outcome := range report.CryptoUnavailableOutcomes {
		testutil.RequireEqualf(t, outcome.Status, "crypto-unavailable", "crypto outcome status")
	}
	testutil.RequireTruef(t, len(report.RedactedRequestIDs) > 0, "report did not include redacted request ids")
	assertReportDoesNotLeak(t, report, "root-token", "kubernetes-jwt", "client-token", "vault:v1:secret-ciphertext", "plaintext-secret")
}

func requireRunReport(t *testing.T, report Report, err error) {
	t.Helper()
	testutil.RequireNoErrorf(t, err, "run smoke; report=%#v", report)
	testutil.RequireEqualf(t, report.Status, "passed", "report status")
	testutil.RequireEqualf(t, report.AuditDeviceStatus, "enabled:file:/tmp/openbao-audit.log", "audit device status")
	testutil.RequireEqualf(t, report.KubernetesAuth.Role, DefaultKubernetesRole, "kubernetes role")
	testutil.RequireFalsef(t, report.KubernetesAuth.BroadKeyAdminPermissions, "broad key admin permissions = true")
	testutil.RequireTruef(t, report.KubernetesAuth.DataKeyRequestWithoutKeyAdmin, "data key request without key admin = false")
	testutil.RequireEqualf(t, report.Transit.KeyName, "scrap-backend", "transit key name")
	testutil.RequireEqualf(t, report.Transit.KeyVersionBefore, uint32(1), "transit key version before")
	testutil.RequireEqualf(t, report.Transit.DataKeyVersion, uint32(1), "transit data key version")
	testutil.RequireEqualf(t, report.Transit.RewrapVersion, uint32(2), "transit rewrap version")
	testutil.RequireEqualf(t, report.Transit.KeyVersionAfter, uint32(2), "transit key version after")
	testutil.RequireTruef(t, report.Transit.UnwrapAADMatched, "transit unwrap AAD did not match")
	testutil.RequireTruef(t, report.Transit.RewrapAADMatched, "transit rewrap AAD did not match")
	testutil.RequireEqualf(t, len(report.CryptoUnavailableOutcomes), 2, "crypto unavailable outcome count")
}

func TestRunFailsWhenKubernetesClientHasKeyAdminCapability(t *testing.T) {
	server := newFakeOpenBao(t, true)
	outage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(outage.Close)

	report, err := Run(context.Background(), testOptions(server.URL, outage.URL))
	if err == nil || report.Status != "failed" {
		t.Fatalf("run smoke err=%v status=%s, want failed broad key-admin evidence", err, report.Status)
	}
	if !report.KubernetesAuth.BroadKeyAdminPermissions {
		t.Fatalf("kubernetes auth evidence = %#v, want broad key admin detected", report.KubernetesAuth)
	}
}

func TestAuditStatusDoesNotMatchNonFileAuditPathSubstring(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v1/sys/audit" {
			http.NotFound(w, r)
			return
		}
		writeJSON(t, w, map[string]any{"profile/": map[string]string{"type": "socket"}})
	}))
	t.Cleanup(server.Close)

	report := Report{}
	status := auditStatus(context.Background(), openBaoClient{
		baseURL: server.URL,
		token:   "root-token",
		http:    server.Client(),
	}, DefaultAuditDevice, &report)
	if status != "missing:"+DefaultAuditDevice {
		t.Fatalf("audit status = %q, want missing file device", status)
	}
}

func TestAuditStatusReadsWrappedOpenBaoAuditResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v1/sys/audit" {
			http.NotFound(w, r)
			return
		}
		writeJSON(t, w, map[string]any{
			"request_id": "request-from-body",
			"data": map[string]any{
				"file/": map[string]string{
					"type": "file",
				},
			},
		})
	}))
	t.Cleanup(server.Close)

	report := Report{}
	status := auditStatus(context.Background(), openBaoClient{
		baseURL: server.URL,
		token:   "root-token",
		http:    server.Client(),
	}, DefaultAuditDevice, &report)
	if status != "enabled:"+DefaultAuditDevice {
		t.Fatalf("audit status = %q, want enabled file device", status)
	}
	if len(report.RedactedRequestIDs) != 1 || strings.Contains(report.RedactedRequestIDs[0], "request-from-body") {
		t.Fatalf("redacted request ids = %#v, want body request id hashed", report.RedactedRequestIDs)
	}
}

func TestRedactedRequestIDUsesOpenBaoResponseBody(t *testing.T) {
	got := redactedRequestID(http.Header{}, []byte(`{"request_id":"body-request-id"}`))
	if got == "" || strings.Contains(got, "body-request-id") {
		t.Fatalf("redacted request id = %q, want hashed body request id", got)
	}
}

func newFakeOpenBao(t *testing.T, broadKeyAdmin bool) *httptest.Server {
	t.Helper()
	version := uint32(1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Vault-Request", "request-"+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		handleFakeOpenBaoRequest(t, w, r, &version, broadKeyAdmin)
	}))
	t.Cleanup(server.Close)
	return server
}

func handleFakeOpenBaoRequest(t *testing.T, w http.ResponseWriter, r *http.Request, version *uint32, broadKeyAdmin bool) {
	t.Helper()
	switch r.URL.Path {
	case "/v1/sys/audit":
		handleFakeAuditStatus(t, w, r)
	case "/v1/transit/keys/scrap-backend":
		handleFakeTransitKeyStatus(t, w, r, version)
	case "/v1/transit/keys/scrap-backend/rotate":
		handleFakeTransitKeyRotate(t, w, r, version)
	case "/v1/auth/kubernetes/login":
		handleFakeKubernetesLogin(t, w, r)
	case "/v1/sys/capabilities-self":
		handleFakeCapabilities(t, w, r, broadKeyAdmin)
	case "/v1/transit/datakey/plaintext/scrap-backend":
		handleFakePlaintextDataKey(t, w, r)
	case "/v1/transit/decrypt/scrap-backend":
		handleFakeDecrypt(t, w, r)
	case "/v1/transit/rewrap/scrap-backend":
		handleFakeRewrap(t, w, r)
	default:
		http.NotFound(w, r)
	}
}

func handleFakeAuditStatus(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	requireMethod(t, r, http.MethodGet)
	requireToken(t, r.Header.Get("X-Vault-Token"), "root-token")
	writeJSON(t, w, map[string]any{"file/": map[string]string{"type": "file"}})
}

func handleFakeTransitKeyStatus(t *testing.T, w http.ResponseWriter, r *http.Request, version *uint32) {
	t.Helper()
	requireMethod(t, r, http.MethodGet)
	requireToken(t, r.Header.Get("X-Vault-Token"), "root-token")
	writeJSON(t, w, map[string]any{"data": map[string]uint32{"latest_version": *version}})
}

func handleFakeTransitKeyRotate(t *testing.T, w http.ResponseWriter, r *http.Request, version *uint32) {
	t.Helper()
	requireMethod(t, r, http.MethodPost)
	requireToken(t, r.Header.Get("X-Vault-Token"), "root-token")
	(*version)++
	writeJSON(t, w, map[string]any{"data": map[string]any{}})
}

func handleFakePlaintextDataKey(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	requireMethod(t, r, http.MethodPost)
	requireToken(t, r.Header.Get("X-Vault-Token"), "client-token")
	writeJSON(t, w, map[string]any{"data": map[string]string{
		"plaintext":  "plaintext-secret",
		"ciphertext": "vault:v1:secret-ciphertext",
	}})
}

func handleFakeRewrap(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	requireMethod(t, r, http.MethodPost)
	requireToken(t, r.Header.Get("X-Vault-Token"), "client-token")
	writeJSON(t, w, map[string]any{"data": map[string]string{"ciphertext": "vault:v2:secret-ciphertext"}})
}

func handleFakeKubernetesLogin(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	requireToken(t, r.Header.Get("X-Vault-Token"), "root-token")
	var req map[string]string
	decodeJSON(t, r, &req)
	testutil.RequireEqualf(t, req["role"], DefaultKubernetesRole, "kubernetes login role")
	testutil.RequireEqualf(t, req["jwt"], "kubernetes-jwt", "kubernetes login jwt")
	writeJSON(t, w, map[string]any{"auth": map[string]any{
		"client_token": "client-token",
		"policies":     []string{"default", "scrap-transit-client"},
	}})
}

func handleFakeCapabilities(t *testing.T, w http.ResponseWriter, r *http.Request, broadKeyAdmin bool) {
	t.Helper()
	requireToken(t, r.Header.Get("X-Vault-Token"), "client-token")
	var req map[string]string
	decodeJSON(t, r, &req)
	writeJSON(t, w, map[string]any{"capabilities": fakeCapabilities(req["path"], broadKeyAdmin)})
}

func fakeCapabilities(path string, broadKeyAdmin bool) []string {
	if strings.Contains(path, "keys/") && broadKeyAdmin {
		return []string{"read", "update", "delete"}
	}
	if strings.Contains(path, "datakey/plaintext") {
		return []string{"update"}
	}
	return []string{"deny"}
}

func handleFakeDecrypt(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	requireToken(t, r.Header.Get("X-Vault-Token"), "client-token")
	var req map[string]string
	decodeJSON(t, r, &req)
	if strings.Contains(req["ciphertext"], "v4294967295") {
		http.Error(w, "missing key version", http.StatusBadRequest)
		return
	}
	if req["context"] == "" {
		http.Error(w, "missing context", http.StatusBadRequest)
		return
	}
	writeJSON(t, w, map[string]any{"data": map[string]string{"plaintext": "plaintext-secret"}})
}

func testOptions(address, outageAddress string) Options {
	return Options{
		ReleaseSHA:    "abc123",
		DirtyTree:     "clean",
		Address:       address,
		OutageAddress: outageAddress,
		AdminToken:    "root-token",
		KubernetesJWT: "kubernetes-jwt",
		OperationID:   "openbao-smoke-test",
		Now:           func() time.Time { return time.Unix(100, 0).UTC() },
	}
}

func requireToken(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("token = %q, want %q", got, want)
	}
}

func requireMethod(t *testing.T, r *http.Request, want string) {
	t.Helper()
	testutil.RequireEqualf(t, want, r.Method, "method for %s", r.URL.Path)
}

func decodeJSON(t *testing.T, r *http.Request, out any) {
	t.Helper()
	testutil.RequireNoErrorf(t, json.NewDecoder(r.Body).Decode(out), "decode request JSON")
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	testutil.RequireNoErrorf(t, json.NewEncoder(w).Encode(value), "write response JSON")
}

func assertReportDoesNotLeak(t *testing.T, report Report, forbidden ...string) {
	t.Helper()
	encoded, err := json.Marshal(report)
	testutil.RequireNoErrorf(t, err, "marshal report")
	output := string(encoded)
	for _, value := range forbidden {
		if strings.Contains(output, value) {
			t.Fatalf("report leaked %q: %s", value, output)
		}
	}
}
