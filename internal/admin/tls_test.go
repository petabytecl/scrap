package admin

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/security"
	securityfixture "github.com/petabytecl/scrap/test/fixtures/security"
)

func TestServerServeListenerWithMTLS(t *testing.T) {
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
	clientTLS, err := security.BuildMTLSClientConfig("SCRAP_TLS_SCRAPCTL", security.ClientTLSFiles{
		CertPath:   bundle.ClientCertPath,
		KeyPath:    bundle.ClientKeyPath,
		RootCAPath: bundle.CACertPath,
		ServerName: "admin.scrap.local",
	})
	if err != nil {
		t.Fatalf("BuildMTLSClientConfig: %v", err)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0") //nolint:noctx // test listener, context not meaningful
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	srv := New()
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.serveListener(lis, serverTLS) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		if err := <-serveErr; err != nil {
			t.Fatalf("serveListener: %v", err)
		}
	})

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: clientTLS}}
	resp, err := client.Get("https://" + lis.Addr().String() + "/healthz") //nolint:noctx // test client request lifetime is bounded by server cleanup.
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if got["status"] != "ok" {
		t.Fatalf("status field = %v, want ok", got["status"])
	}
}
