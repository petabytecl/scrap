package openbaosmoke

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRunRecordsRedactedKubernetesTransitEvidence(t *testing.T) {
	server := newFakeOpenBao(t, false)
	outage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(outage.Close)

	report, err := Run(context.Background(), testOptions(server.URL, outage.URL))
	if err != nil {
		t.Fatalf("run smoke: %v; report=%#v", err, report)
	}
	if report.Status != "passed" || report.AuditDeviceStatus != "enabled:file:/tmp/openbao-audit.log" {
		t.Fatalf("report status/audit = %s/%s", report.Status, report.AuditDeviceStatus)
	}
	if report.KubernetesAuth.Role != DefaultKubernetesRole ||
		report.KubernetesAuth.BroadKeyAdminPermissions ||
		!report.KubernetesAuth.DataKeyRequestWithoutKeyAdmin {
		t.Fatalf("kubernetes auth evidence = %#v", report.KubernetesAuth)
	}
	if report.Transit.KeyName != "scrap-backend" ||
		report.Transit.KeyVersionBefore != 1 ||
		report.Transit.DataKeyVersion != 1 ||
		report.Transit.RewrapVersion != 2 ||
		report.Transit.KeyVersionAfter != 2 ||
		!report.Transit.UnwrapAADMatched ||
		!report.Transit.RewrapAADMatched {
		t.Fatalf("transit evidence = %#v", report.Transit)
	}
	if len(report.CryptoUnavailableOutcomes) != 2 {
		t.Fatalf("crypto outcomes = %#v, want missing-version and outage", report.CryptoUnavailableOutcomes)
	}
	for _, outcome := range report.CryptoUnavailableOutcomes {
		if outcome.Status != "crypto-unavailable" {
			t.Fatalf("outcome = %#v, want crypto-unavailable", outcome)
		}
	}
	if len(report.RedactedRequestIDs) == 0 {
		t.Fatal("report did not include redacted request ids")
	}
	assertReportDoesNotLeak(t, report, "root-token", "kubernetes-jwt", "client-token", "vault:v1:secret-ciphertext", "plaintext-secret")
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

func newFakeOpenBao(t *testing.T, broadKeyAdmin bool) *httptest.Server {
	t.Helper()
	version := uint32(1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Vault-Request", "request-"+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		token := r.Header.Get("X-Vault-Token")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sys/audit":
			requireToken(t, token, "root-token")
			writeJSON(t, w, map[string]any{"file/": map[string]string{"type": "file"}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/transit/keys/scrap-backend":
			requireToken(t, token, "root-token")
			writeJSON(t, w, map[string]any{"data": map[string]uint32{"latest_version": version}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/transit/keys/scrap-backend/rotate":
			requireToken(t, token, "root-token")
			version++
			writeJSON(t, w, map[string]any{"data": map[string]any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/kubernetes/login":
			requireToken(t, token, "root-token")
			var req map[string]string
			decodeJSON(t, r, &req)
			if req["role"] != DefaultKubernetesRole || req["jwt"] != "kubernetes-jwt" {
				t.Fatalf("kubernetes login request = %#v", req)
			}
			writeJSON(t, w, map[string]any{"auth": map[string]any{
				"client_token": "client-token",
				"policies":     []string{"default", "scrap-transit-client"},
			}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sys/capabilities-self":
			requireToken(t, token, "client-token")
			var req map[string]string
			decodeJSON(t, r, &req)
			caps := []string{"deny"}
			if strings.Contains(req["path"], "datakey/plaintext") {
				caps = []string{"update"}
			}
			if strings.Contains(req["path"], "keys/") && broadKeyAdmin {
				caps = []string{"read", "update", "delete"}
			}
			writeJSON(t, w, map[string]any{"capabilities": caps})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/transit/datakey/plaintext/scrap-backend":
			requireToken(t, token, "client-token")
			writeJSON(t, w, map[string]any{"data": map[string]string{
				"plaintext":  "plaintext-secret",
				"ciphertext": "vault:v1:secret-ciphertext",
			}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/transit/decrypt/scrap-backend":
			requireToken(t, token, "client-token")
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
		case r.Method == http.MethodPost && r.URL.Path == "/v1/transit/rewrap/scrap-backend":
			requireToken(t, token, "client-token")
			writeJSON(t, w, map[string]any{"data": map[string]string{"ciphertext": "vault:v2:secret-ciphertext"}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
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

func decodeJSON(t *testing.T, r *http.Request, out any) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		t.Fatalf("decode request JSON: %v", err)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("write response JSON: %v", err)
	}
}

func assertReportDoesNotLeak(t *testing.T, report Report, forbidden ...string) {
	t.Helper()
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	output := string(encoded)
	for _, value := range forbidden {
		if strings.Contains(output, value) {
			t.Fatalf("report leaked %q: %s", value, output)
		}
	}
}
