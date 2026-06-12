package scrapctl

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/security"
	securityfixture "github.com/petabytecl/scrap/test/fixtures/security"
)

func TestStatusUsesMTLSClientCredentials(t *testing.T) {
	bundle := securityfixture.WriteCertBundle(t, t.TempDir(), securityfixture.CertOptions{
		ServerName: "admin.scrap.local",
	})
	serverTLS, err := security.BuildMTLSServerConfig("SCRAP_TLS_ADMIN", security.TLSFiles{
		ServerCertPath: bundle.ServerCertPath,
		ServerKeyPath:  bundle.ServerKeyPath,
		ClientCAPath:   bundle.CACertPath,
		ServerName:     "admin.scrap.local",
	})
	if err != nil {
		t.Fatalf("BuildMTLSServerConfig: %v", err)
	}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) != 1 {
			t.Fatalf("request missing client certificate")
		}
		_, _ = io.WriteString(w, `{"status":"ok","upload_pressure":"ok","upload_pressure_level":0,"upload_pending_bytes":0,"upload_pending_blocks":0}`)
	}))
	srv.TLS = serverTLS
	srv.StartTLS()
	defer srv.Close()

	var out bytes.Buffer
	err = Run([]string{
		"status",
		"--admin-url", srv.URL,
		"--tls-cert", bundle.ClientCertPath,
		"--tls-key", bundle.ClientKeyPath,
		"--tls-ca", bundle.CACertPath,
		"--tls-server-name", "admin.scrap.local",
		"--output", "json",
	}, &out, io.Discard, Deps{})
	if err != nil {
		t.Fatalf("Run status: %v", err)
	}
	if !strings.Contains(out.String(), `"status":"ok"`) {
		t.Fatalf("output missing ok status: %s", out.String())
	}
}

func TestStatusInProductionRequiresScrapctlTLS(t *testing.T) {
	t.Setenv("SCRAP_SECURITY_MODE", "production")

	err := Run([]string{"status"}, io.Discard, io.Discard, Deps{
		HTTPClient: healthClient(`{"status":"ok","upload_pressure":"ok","upload_pressure_level":0,"upload_pending_bytes":0,"upload_pending_blocks":0}`),
	})
	if err == nil {
		t.Fatal("expected TLS configuration error")
	}
	msg := fmt.Sprint(err)
	if !strings.Contains(msg, "SCRAP_TLS_SCRAPCTL") {
		t.Fatalf("error %q does not name SCRAP_TLS_SCRAPCTL", msg)
	}
}

func TestStatusAdminDenialsReturnBoundedErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{
		{name: "authorization denied", status: http.StatusForbidden},
		{name: "rate limited", status: http.StatusTooManyRequests},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := Run([]string{"status", "--admin-url", "http://admin.local"}, &out, io.Discard, Deps{
				HTTPClient: statusHTTPClient(tc.status, "secret-token tx-secret-raw invoice-secret.pdf"),
			})
			if err == nil {
				t.Fatal("expected status error")
			}
			if !strings.Contains(err.Error(), fmt.Sprintf("GET healthz status: %d", tc.status)) {
				t.Fatalf("error = %q, want bounded status", err)
			}
			for _, forbidden := range []string{"secret-token", "tx-secret-raw", "invoice-secret.pdf"} {
				if strings.Contains(err.Error(), forbidden) || strings.Contains(out.String(), forbidden) {
					t.Fatalf("scrapctl denial leaked %q in err=%q out=%q", forbidden, err, out.String())
				}
			}
		})
	}
}

func TestStatusWithTLSRejectsPlainHTTPURL(t *testing.T) {
	bundle := securityfixture.WriteCertBundle(t, t.TempDir(), securityfixture.CertOptions{
		ServerName: "admin.scrap.local",
	})

	err := Run([]string{
		"status",
		"--admin-url", "http://127.0.0.1:18100",
		"--tls-cert", bundle.ClientCertPath,
		"--tls-key", bundle.ClientKeyPath,
		"--tls-ca", bundle.CACertPath,
		"--tls-server-name", "admin.scrap.local",
	}, io.Discard, io.Discard, Deps{})
	if err == nil {
		t.Fatal("expected HTTPS requirement error")
	}
	if !strings.Contains(err.Error(), "requires https") {
		t.Fatalf("error = %q, want HTTPS requirement", err)
	}
}

func statusHTTPClient(status int, body string) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
}
