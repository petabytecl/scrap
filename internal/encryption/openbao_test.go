package encryption_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/encryption"
)

func TestOpenBaoTransitMapsTransitOperations(t *testing.T) {
	const token = "test-token"
	srv := newOpenBaoTransitTestServer(t, token)
	defer srv.Close()

	transit, err := encryption.NewOpenBaoTransit(encryption.OpenBaoConfig{
		Address:   srv.URL(),
		MountPath: "transit",
		KeyName:   "scrap-documents",
		Token:     token,
	})
	if err != nil {
		t.Fatalf("NewOpenBaoTransit: %v", err)
	}
	if !encryption.ProductionCapable(transit) {
		t.Fatal("OpenBao Transit should be production capable")
	}

	assertOpenBaoReadiness(t, transit)
	dataKey := assertOpenBaoDataKey(t, transit)
	assertOpenBaoUnwrap(t, transit, dataKey.WrappedKey)
	assertOpenBaoRewrap(t, transit, dataKey.WrappedKey)
	assertOpenBaoPaths(t, srv.Paths())
}

func TestOpenBaoTransitReadinessAcceptsLargeSuccessfulPayload(t *testing.T) {
	const token = "test-token"
	keyVersions := map[string]any{}
	for i := 1; i <= 400; i++ {
		keyVersions[strconv.Itoa(i)] = map[string]any{"creation_time": "2026-06-08T00:00:00Z"}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		data := validOpenBaoKeyMetadata()
		data["latest_version"] = 400
		data["min_decryption_version"] = 2
		data["keys"] = keyVersions
		writeOpenBaoData(t, w, data)
	}))
	defer srv.Close()

	transit, err := encryption.NewOpenBaoTransit(encryption.OpenBaoConfig{
		Address:   srv.URL,
		MountPath: "transit",
		KeyName:   "scrap-documents",
		Token:     token,
	})
	if err != nil {
		t.Fatalf("NewOpenBaoTransit: %v", err)
	}

	ready, err := transit.Readiness(context.Background())
	if err != nil {
		t.Fatalf("Readiness: %v", err)
	}
	if !ready.Ready || ready.LatestVersion != 400 || ready.MinimumDecryptionVersion != 2 {
		t.Fatalf("Readiness = %+v, want large-key metadata", ready)
	}
}

func TestOpenBaoTransitReadinessRejectsUnusableKeys(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "soft deleted",
			mutate: func(data map[string]any) {
				data["soft_deleted"] = true
			},
		},
		{
			name: "no encryption support",
			mutate: func(data map[string]any) {
				data["supports_encryption"] = false
			},
		},
		{
			name: "no decryption support",
			mutate: func(data map[string]any) {
				data["supports_decryption"] = false
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				data := validOpenBaoKeyMetadata()
				tt.mutate(data)
				writeOpenBaoData(t, w, data)
			}))
			defer srv.Close()

			transit, err := encryption.NewOpenBaoTransit(encryption.OpenBaoConfig{
				Address:   srv.URL,
				MountPath: "transit",
				KeyName:   "scrap-documents",
				Token:     "test-token",
			})
			if err != nil {
				t.Fatalf("NewOpenBaoTransit: %v", err)
			}
			_, err = transit.Readiness(context.Background())
			if !errors.Is(err, encryption.ErrUnavailable) {
				t.Fatalf("Readiness error = %v, want unavailable", err)
			}
		})
	}
}

func assertOpenBaoReadiness(t *testing.T, transit encryption.Transit) {
	t.Helper()
	ready, err := transit.Readiness(context.Background())
	if err != nil {
		t.Fatalf("Readiness: %v", err)
	}
	if !ready.Ready || ready.LatestVersion != 2 || ready.MinimumDecryptionVersion != 1 {
		t.Fatalf("Readiness = %+v, want ready versions", ready)
	}
}

func assertOpenBaoDataKey(t *testing.T, transit encryption.Transit) encryption.DataKey {
	t.Helper()
	dataKey, err := transit.GenerateDataKey(context.Background(), encryption.GenerateDataKeyRequest{
		Context: []byte("tx/doc"),
		Bits:    256,
	})
	if err != nil {
		t.Fatalf("GenerateDataKey: %v", err)
	}
	if string(dataKey.Plaintext) != "01234567890123456789012345678901" || dataKey.WrappedKey != "vault:v2:wrapped-key" {
		t.Fatalf("dataKey = %+v", dataKey)
	}
	return dataKey
}

func assertOpenBaoUnwrap(t *testing.T, transit encryption.Transit, wrappedKey string) {
	t.Helper()
	unwrapped, err := transit.UnwrapDataKey(context.Background(), encryption.UnwrapDataKeyRequest{
		WrappedKey: wrappedKey,
		Context:    []byte("tx/doc"),
	})
	if err != nil {
		t.Fatalf("UnwrapDataKey: %v", err)
	}
	if string(unwrapped.Plaintext) != "01234567890123456789012345678901" {
		t.Fatalf("unwrapped plaintext = %q", unwrapped.Plaintext)
	}
}

func assertOpenBaoRewrap(t *testing.T, transit encryption.Transit, wrappedKey string) {
	t.Helper()
	rewrapped, err := transit.RewrapDataKey(context.Background(), encryption.RewrapDataKeyRequest{
		WrappedKey: wrappedKey,
		Context:    []byte("tx/doc"),
	})
	if err != nil {
		t.Fatalf("RewrapDataKey: %v", err)
	}
	if rewrapped.WrappedKey != "vault:v3:rewrapped-key" || !rewrapped.Changed {
		t.Fatalf("rewrapped = %+v", rewrapped)
	}
}

func assertOpenBaoPaths(t *testing.T, paths []string) {
	t.Helper()
	wantPaths := []string{
		"GET /v1/transit/keys/scrap-documents",
		"PUT /v1/transit/datakey/plaintext/scrap-documents",
		"PUT /v1/transit/decrypt/scrap-documents",
		"PUT /v1/transit/rewrap/scrap-documents",
	}
	if got := strings.Join(paths, "\n"); got != strings.Join(wantPaths, "\n") {
		t.Fatalf("paths:\n%s\nwant:\n%s", got, strings.Join(wantPaths, "\n"))
	}
}

type openBaoTransitTestServer struct {
	t      *testing.T
	token  string
	server *httptest.Server
	paths  []string
}

func newOpenBaoTransitTestServer(t *testing.T, token string) *openBaoTransitTestServer {
	t.Helper()
	srv := &openBaoTransitTestServer{t: t, token: token}
	routes := map[string]http.HandlerFunc{
		"GET /v1/transit/keys/scrap-documents":              srv.handleKeys,
		"PUT /v1/transit/datakey/plaintext/scrap-documents": srv.handleDataKey,
		"PUT /v1/transit/decrypt/scrap-documents":           srv.handleDecrypt,
		"PUT /v1/transit/rewrap/scrap-documents":            srv.handleRewrap,
	}
	srv.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := r.Method + " " + r.URL.Path
		srv.paths = append(srv.paths, route)
		if got := r.Header.Get("X-Vault-Token"); got != srv.token {
			t.Fatalf("X-Vault-Token = %q, want token", got)
		}
		handler, ok := routes[route]
		if !ok {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		handler(w, r)
	}))
	return srv
}

func (s *openBaoTransitTestServer) URL() string {
	return s.server.URL
}

func (s *openBaoTransitTestServer) Close() {
	s.server.Close()
}

func (s *openBaoTransitTestServer) Paths() []string {
	paths := make([]string, len(s.paths))
	copy(paths, s.paths)
	return paths
}

func (s *openBaoTransitTestServer) handleKeys(w http.ResponseWriter, _ *http.Request) {
	writeOpenBaoData(s.t, w, validOpenBaoKeyMetadata())
}

func (s *openBaoTransitTestServer) handleDataKey(w http.ResponseWriter, r *http.Request) {
	body := readJSONBody(s.t, r)
	if body["context"] != base64.StdEncoding.EncodeToString([]byte("tx/doc")) {
		s.t.Fatalf("datakey context = %v, want encoded context", body["context"])
	}
	if body["bits"] != float64(256) {
		s.t.Fatalf("datakey bits = %v, want 256", body["bits"])
	}
	writeOpenBaoData(s.t, w, map[string]any{
		"plaintext":  base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901")),
		"ciphertext": "vault:v2:wrapped-key",
	})
}

func (s *openBaoTransitTestServer) handleDecrypt(w http.ResponseWriter, r *http.Request) {
	body := readJSONBody(s.t, r)
	if body["ciphertext"] != "vault:v2:wrapped-key" {
		s.t.Fatalf("decrypt ciphertext = %v, want wrapped key", body["ciphertext"])
	}
	writeOpenBaoData(s.t, w, map[string]any{
		"plaintext": base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901")),
	})
}

func (s *openBaoTransitTestServer) handleRewrap(w http.ResponseWriter, _ *http.Request) {
	writeOpenBaoData(s.t, w, map[string]any{
		"ciphertext": "vault:v3:rewrapped-key",
	})
}

func TestOpenBaoTransitClassifiesAndRedactsProviderFailures(t *testing.T) {
	const token = "super-secret-token"
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       error
	}{
		{name: "auth denied", statusCode: http.StatusForbidden, body: `{"errors":["denied ` + token + `"]}`, want: encryption.ErrAuthDenied},
		{name: "missing key", statusCode: http.StatusNotFound, body: `{"errors":["no key named scrap-documents"]}`, want: encryption.ErrMissingKey},
		{name: "minimum version", statusCode: http.StatusBadRequest, body: `{"errors":["ciphertext below minimum decryption version"]}`, want: encryption.ErrMinimumVersion},
		{name: "too old version", statusCode: http.StatusBadRequest, body: `{"errors":["ciphertext or signature version is disallowed by policy (too old)"]}`, want: encryption.ErrMinimumVersion},
		{name: "outage", statusCode: http.StatusServiceUnavailable, body: `{"errors":["sealed"]}`, want: encryption.ErrUnavailable},
		// A 5xx whose body coincidentally mentions minimum+version is a transient
		// outage, not the terminal min-version condition: the body heuristic only
		// applies on a 400.
		{name: "outage mentioning minimum version", statusCode: http.StatusServiceUnavailable, body: `{"errors":["minimum decryption version cache unavailable"]}`, want: encryption.ErrUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			transit, err := encryption.NewOpenBaoTransit(encryption.OpenBaoConfig{
				Address:   srv.URL,
				MountPath: "transit",
				KeyName:   "scrap-documents",
				Token:     token,
			})
			if err != nil {
				t.Fatalf("NewOpenBaoTransit: %v", err)
			}
			_, err = transit.Readiness(context.Background())
			if !errors.Is(err, tt.want) {
				t.Fatalf("Readiness error = %v, want %v", err, tt.want)
			}
			if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), tt.body) {
				t.Fatalf("error leaked provider detail: %q", err)
			}
		})
	}
}

func TestOpenBaoTransitPreservesContextFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "canceled", err: context.Canceled, want: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded, want: context.DeadlineExceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transit, err := encryption.NewOpenBaoTransit(encryption.OpenBaoConfig{
				Address:   "http://127.0.0.1:8200",
				MountPath: "transit",
				KeyName:   "scrap-documents",
				Token:     "test-token",
				HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return nil, tt.err
				})},
			})
			if err != nil {
				t.Fatalf("NewOpenBaoTransit: %v", err)
			}
			_, err = transit.Readiness(context.Background())
			if !errors.Is(err, tt.want) {
				t.Fatalf("Readiness error = %v, want %v", err, tt.want)
			}
			if errors.Is(err, encryption.ErrUnavailable) {
				t.Fatalf("Readiness error = %v, want context error without unavailable classification", err)
			}
		})
	}
}

func TestOpenBaoTransitRejectsInsecureSkipVerifyEnv(t *testing.T) {
	for _, key := range []string{"VAULT_SKIP_VERIFY", "BAO_SKIP_VERIFY"} {
		t.Run(key+" truthy", func(t *testing.T) {
			t.Setenv(key, "true")
			_, err := encryption.NewOpenBaoTransit(encryption.OpenBaoConfig{
				Address:   "https://vault.example:8200",
				MountPath: "transit",
				KeyName:   "scrap-documents",
				Token:     "test-token",
			})
			if !errors.Is(err, encryption.ErrInvalidConfig) {
				t.Fatalf("NewOpenBaoTransit with %s=true = %v, want ErrInvalidConfig", key, err)
			}
		})
		t.Run(key+" malformed", func(t *testing.T) {
			t.Setenv(key, "not-a-bool")
			_, err := encryption.NewOpenBaoTransit(encryption.OpenBaoConfig{
				Address:   "https://vault.example:8200",
				MountPath: "transit",
				KeyName:   "scrap-documents",
				Token:     "test-token",
			})
			if !errors.Is(err, encryption.ErrInvalidConfig) {
				t.Fatalf("NewOpenBaoTransit with malformed %s = %v, want ErrInvalidConfig", key, err)
			}
		})
	}
}

func TestOpenBaoTransitAcceptsExplicitlyDisabledSkipVerify(t *testing.T) {
	t.Setenv("VAULT_SKIP_VERIFY", "false")
	if _, err := encryption.NewOpenBaoTransit(encryption.OpenBaoConfig{
		Address:   "https://vault.example:8200",
		MountPath: "transit",
		KeyName:   "scrap-documents",
		Token:     "test-token",
	}); err != nil {
		t.Fatalf("NewOpenBaoTransit with VAULT_SKIP_VERIFY=false = %v, want nil", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func readJSONBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	defer func() { _ = r.Body.Close() }()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	return body
}

func writeOpenBaoData(t *testing.T, w http.ResponseWriter, data map[string]any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"data": data}); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func validOpenBaoKeyMetadata() map[string]any {
	return map[string]any{
		"latest_version":         2,
		"min_decryption_version": 1,
		"supports_encryption":    true,
		"supports_decryption":    true,
		"soft_deleted":           false,
	}
}
