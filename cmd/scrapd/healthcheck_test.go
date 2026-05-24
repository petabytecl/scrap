package main

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/petabytecl/scrap/internal/testutil"
)

func TestRunHealthcheckSucceedsForServingStatus(t *testing.T) {
	address, stop := startHealthcheckServer(t, "scrap.admin.v1-readiness", healthv1.HealthCheckResponse_SERVING)
	defer stop()

	err := runHealthcheck([]string{
		"--address=" + address,
		"--service=scrap.admin.v1-readiness",
		"--timeout=2s",
	}, io.Discard)

	testutil.RequireNoErrorf(t, err, "run readiness healthcheck")
}

func TestRunHealthcheckFailsForNotServingStatus(t *testing.T) {
	address, stop := startHealthcheckServer(t, "", healthv1.HealthCheckResponse_NOT_SERVING)
	defer stop()

	err := runHealthcheck([]string{
		"--address=" + address,
		"--timeout=2s",
	}, io.Discard)

	if err == nil || !strings.Contains(err.Error(), "NOT_SERVING") {
		t.Fatalf("healthcheck error = %v, want NOT_SERVING", err)
	}
}

func TestRunHealthcheckRejectsInvalidTimeout(t *testing.T) {
	err := runHealthcheck([]string{"--timeout=0s"}, io.Discard)

	if err == nil || !strings.Contains(err.Error(), "timeout must be positive") {
		t.Fatalf("healthcheck error = %v, want timeout validation", err)
	}
}

func startHealthcheckServer(
	t *testing.T,
	service string,
	status healthv1.HealthCheckResponse_ServingStatus,
) (string, func()) {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	testutil.RequireNoErrorf(t, err, "listen health server")
	server := grpc.NewServer()
	healthServer := health.NewServer()
	healthServer.SetServingStatus(service, status)
	healthv1.RegisterHealthServer(server, healthServer)
	go func() {
		_ = server.Serve(listener)
	}()
	return listener.Addr().String(), func() {
		server.Stop()
	}
}
