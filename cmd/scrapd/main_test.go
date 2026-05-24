package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/config"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestMetricsEndpointServesMetricsPath(t *testing.T) {
	endpoint, err := listenMetricsEndpoint("127.0.0.1:0")
	testutil.RequireNoErrorf(t, err, "listen metrics endpoint")
	defer func() { testutil.RequireNoErrorf(t, endpoint.Close(), "close metrics endpoint") }()

	errCh := endpoint.Serve()
	conn, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", endpoint.Address())
	testutil.RequireNoErrorf(t, err, "dial metrics endpoint")
	defer func() { testutil.RequireNoErrorf(t, conn.Close(), "close metrics connection") }()
	_, err = fmt.Fprintf(conn, "%s /metrics HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", http.MethodGet, endpoint.Address())
	testutil.RequireNoErrorf(t, err, "write metrics request")
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	testutil.RequireNoErrorf(t, err, "read metrics response")
	defer func() { testutil.RequireNoErrorf(t, resp.Body.Close(), "close response body") }()
	testutil.RequireEqualf(t, resp.StatusCode, http.StatusOK, "metrics status")
	body, err := io.ReadAll(resp.Body)
	testutil.RequireNoErrorf(t, err, "read metrics body")
	if !strings.Contains(string(body), "scrap_write_latency_seconds") {
		t.Fatalf("metrics body missing write latency metric:\n%s", string(body))
	}

	testutil.RequireNoErrorf(t, endpoint.Close(), "close metrics endpoint")
	testutil.RequireNoErrorf(t, <-errCh, "metrics endpoint stopped")
}

func TestBuildLocalApplicationsRegistersHealthApplication(t *testing.T) {
	cfg := config.Default()
	cfg.LocalDataDir = t.TempDir()

	apps, localApp, operationStore, err := buildLocalApplications(cfg)
	testutil.RequireNoErrorf(t, err, "build local applications")
	defer func() { testutil.RequireNoErrorf(t, operationStore.Close(), "close operation store") }()
	defer func() { testutil.RequireNoErrorf(t, localApp.Close(), "close local application") }()

	testutil.RequireNotNilf(t, apps.Health, "health application")
	testutil.RequireTruef(t, apps.Health == localApp, "health application is not local application")
}

func TestRegisterFlagSetParsesGRPCServerLimits(t *testing.T) {
	cfg := config.Default()
	flags := flag.NewFlagSet("scrapd-test", flag.ContinueOnError)
	registerFlagSet(flags, &cfg)

	err := flags.Parse([]string{
		"-grpc-max-concurrent-streams=17",
		"-grpc-max-recv-msg-size-bytes=4096",
		"-grpc-max-send-msg-size-bytes=8192",
		"-grpc-keepalive-min-time=15s",
		"-grpc-keepalive-permit-without-stream=false",
		"-grpc-keepalive-max-connection-idle=1m",
		"-grpc-keepalive-max-connection-age=2m",
		"-grpc-keepalive-max-connection-age-grace=3s",
		"-grpc-keepalive-time=30s",
		"-grpc-keepalive-timeout=4s",
	})
	testutil.RequireNoErrorf(t, err, "parse grpc limit flags")

	testutil.RequireEqualf(t, cfg.GRPCServerLimits.MaxConcurrentStreams, uint64(17), "max concurrent streams")
	testutil.RequireEqualf(t, cfg.GRPCServerLimits.MaxRecvMsgSizeBytes, 4096, "max recv message bytes")
	testutil.RequireEqualf(t, cfg.GRPCServerLimits.MaxSendMsgSizeBytes, 8192, "max send message bytes")
	testutil.RequireEqualf(t, cfg.GRPCServerLimits.KeepaliveMinTime, 15*time.Second, "keepalive min time")
	testutil.RequireFalsef(t, cfg.GRPCServerLimits.KeepalivePermitWithoutStream, "permit without stream")
	testutil.RequireEqualf(t, cfg.GRPCServerLimits.KeepaliveMaxConnectionIdle, time.Minute, "max connection idle")
	testutil.RequireEqualf(t, cfg.GRPCServerLimits.KeepaliveMaxConnectionAge, 2*time.Minute, "max connection age")
	testutil.RequireEqualf(t, cfg.GRPCServerLimits.KeepaliveMaxConnectionAgeGrace, 3*time.Second, "max connection age grace")
	testutil.RequireEqualf(t, cfg.GRPCServerLimits.KeepaliveTime, 30*time.Second, "keepalive time")
	testutil.RequireEqualf(t, cfg.GRPCServerLimits.KeepaliveTimeout, 4*time.Second, "keepalive timeout")
}

func TestRegisterFlagSetParsesGRPCTLSConfig(t *testing.T) {
	cfg := config.Default()
	flags := flag.NewFlagSet("scrapd-test", flag.ContinueOnError)
	registerFlagSet(flags, &cfg)

	err := flags.Parse([]string{
		"-grpc-tls-enabled=true",
		"-grpc-tls-cert-file=server.pem",
		"-grpc-tls-key-file=server-key.pem",
		"-grpc-tls-ca-cert-file=ca.pem",
	})
	testutil.RequireNoErrorf(t, err, "parse grpc TLS flags")

	testutil.RequireTruef(t, cfg.TLSEnabled, "grpc TLS enabled")
	testutil.RequireEqualf(t, cfg.TLSCertFile, "server.pem", "grpc TLS cert file")
	testutil.RequireEqualf(t, cfg.TLSKeyFile, "server-key.pem", "grpc TLS key file")
	testutil.RequireEqualf(t, cfg.TLSCACertFile, "ca.pem", "grpc TLS CA cert file")
}
