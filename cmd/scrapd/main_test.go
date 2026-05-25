package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/backendupload"
	"github.com/petabytecl/scrap/internal/config"
	"github.com/petabytecl/scrap/internal/localstorage"
	"github.com/petabytecl/scrap/internal/node"
	"github.com/petabytecl/scrap/internal/operations"
	"github.com/petabytecl/scrap/internal/published"
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
	testutil.RequireNoErrorf(t, apps.Health.CheckLiveness(context.Background()), "health application liveness")
}

func TestBuildApplicationsDisabledReturnsNoopCleanup(t *testing.T) {
	cfg := config.Default()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	apps, uploadRunner, operationExecutor, cleanup, err := buildApplications(cfg, logger, func(string, ...any) {})
	testutil.RequireNoErrorf(t, err, "build applications")
	testutil.RequireNilf(t, uploadRunner, "upload runner")
	testutil.RequireNilf(t, operationExecutor, "operation executor")
	testutil.RequireNilf(t, apps.Health, "health application")
	cleanup()
}

func TestBuildApplicationsWithLocalFilesystemBackend(t *testing.T) {
	cfg := config.Default()
	cfg.EnableLocalNonProductionStorage = true
	cfg.EnableLocalFilesystemBackend = true
	cfg.LocalDataDir = t.TempDir()
	cfg.LocalBackendDataDir = t.TempDir()
	cfg.LocalSealBlockAtBytes = 1
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	apps, uploadRunner, operationExecutor, cleanup, err := buildApplications(cfg, logger, func(string, ...any) {})
	testutil.RequireNoErrorf(t, err, "build applications")
	defer cleanup()
	testutil.RequireNotNilf(t, apps.Documents, "documents application")
	testutil.RequireNotNilf(t, uploadRunner, "upload runner")
	testutil.RequireNotNilf(t, operationExecutor, "operation executor")
	result, err := uploadRunner.RunOnceFunc(context.Background())
	testutil.RequireNoErrorf(t, err, "run upload once")
	testutil.RequireEqualf(t, result.Scanned, 0, "empty upload scan")
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

func TestScrapdBackgroundLoggingHelpers(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	logf := logPrintfFunc(logger)
	logf("hello %s", "world")

	logOperationRecoveryReport(logger, operations.RecoveryResult{}, nil)
	logOperationRecoveryReport(logger, operations.RecoveryResult{Scanned: 2, Queued: 1, Requeued: 1}, nil)
	logOperationRecoveryReport(logger, operations.RecoveryResult{}, errors.New("recovery failed"))
	logOperationRunReport(logger, localstorage.OperationRunResult{}, nil)
	logOperationRunReport(logger, localstorage.OperationRunResult{Scanned: 2, Pending: 1, Succeeded: 1}, nil)
	logOperationRunReport(logger, localstorage.OperationRunResult{}, errors.New("scan failed"))
	logBackendUploadReport(logger, backendupload.RunResult{}, nil)
	logBackendUploadReport(logger, backendupload.RunResult{Scanned: 2, Deferred: 1, Uploaded: 1, Errors: backendUploadErrorCounts(backend.ErrorClassTransient)}, nil)
	logBackendUploadReport(logger, backendupload.RunResult{}, errors.New("upload failed"))
	logBackendUploadPublication(logger, localstorage.BackendUploadOnceResult{Sealed: true, SealedBlockID: "block-1"})
	logBackendUploadPublication(logger, localstorage.BackendUploadOnceResult{
		MetadataPublished: true,
		MetadataPublication: &published.SnapshotPublication{
			Manifest: nil,
		},
	})

	output := logs.String()
	for _, want := range []string{
		"hello world",
		"operation recovery",
		"operation recovery failed",
		"operation scan",
		"operation scan failed",
		"backend upload scan",
		"backend upload scan failed",
		"backend upload sealed block",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("logs missing %q:\n%s", want, output)
		}
	}
}

func backendUploadErrorCounts(class backend.ErrorClass) map[backend.ErrorClass]int {
	counts := map[backend.ErrorClass]int{
		backend.ErrorClassThrottled: 0,
		backend.ErrorClassTransient: 0,
		backend.ErrorClassAuth:      0,
		backend.ErrorClassNotFound:  0,
		backend.ErrorClassConflict:  0,
		backend.ErrorClassCorrupt:   0,
		backend.ErrorClassPermanent: 0,
	}
	counts[class] = 1
	return counts
}

func TestRunOperationLoopRecoversAndRunsUntilContextStops(t *testing.T) {
	store, err := operations.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open operations store")
	defer func() { testutil.RequireNoErrorf(t, store.Close(), "close operations store") }()
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	ctx, cancel := context.WithCancel(context.Background())
	var recoverCalls int32
	var runCalls int32
	executor := fakeOperationExecutor{
		recover: func(context.Context, *operations.Store) (operations.RecoveryResult, error) {
			atomic.AddInt32(&recoverCalls, 1)
			return operations.RecoveryResult{Scanned: 1}, nil
		},
		run: func(context.Context, *operations.Store) (localstorage.OperationRunResult, error) {
			atomic.AddInt32(&runCalls, 1)
			cancel()
			return localstorage.OperationRunResult{Scanned: 1, Succeeded: 1}, nil
		},
	}

	runOperationLoop(ctx, nodeApplicationsWithOperations(store), executor, time.Hour, logger)
	testutil.RequireEqualf(t, atomic.LoadInt32(&recoverCalls), int32(1), "recover calls")
	testutil.RequireEqualf(t, atomic.LoadInt32(&runCalls), int32(1), "run calls")
	if !strings.Contains(logs.String(), "operation scan") {
		t.Fatalf("logs = %q, want operation scan", logs.String())
	}
}

func TestRunOperationLoopStopsOnRecoveryError(t *testing.T) {
	store, err := operations.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open operations store")
	defer func() { testutil.RequireNoErrorf(t, store.Close(), "close operations store") }()
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	var runCalls int32
	executor := fakeOperationExecutor{
		recover: func(context.Context, *operations.Store) (operations.RecoveryResult, error) {
			return operations.RecoveryResult{}, errors.New("recover failed")
		},
		run: func(context.Context, *operations.Store) (localstorage.OperationRunResult, error) {
			atomic.AddInt32(&runCalls, 1)
			return localstorage.OperationRunResult{}, nil
		},
	}

	runOperationLoop(context.Background(), nodeApplicationsWithOperations(store), executor, time.Hour, logger)
	testutil.RequireEqualf(t, atomic.LoadInt32(&runCalls), int32(0), "run calls after recovery error")
	if !strings.Contains(logs.String(), "operation recovery failed") {
		t.Fatalf("logs = %q, want recovery failure", logs.String())
	}
}

func nodeApplicationsWithOperations(store *operations.Store) node.Applications {
	return node.Applications{Operations: store}
}

type fakeOperationExecutor struct {
	run     func(context.Context, *operations.Store) (localstorage.OperationRunResult, error)
	recover func(context.Context, *operations.Store) (operations.RecoveryResult, error)
}

func (e fakeOperationExecutor) RunQueuedOperationsOnce(ctx context.Context, store *operations.Store) (localstorage.OperationRunResult, error) {
	return e.run(ctx, store)
}

func (e fakeOperationExecutor) RecoverInterruptedOperations(ctx context.Context, store *operations.Store) (operations.RecoveryResult, error) {
	return e.recover(ctx, store)
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
		"-require-certificate-identity=true",
	})
	testutil.RequireNoErrorf(t, err, "parse grpc TLS flags")

	testutil.RequireTruef(t, cfg.TLSEnabled, "grpc TLS enabled")
	testutil.RequireEqualf(t, cfg.TLSCertFile, "server.pem", "grpc TLS cert file")
	testutil.RequireEqualf(t, cfg.TLSKeyFile, "server-key.pem", "grpc TLS key file")
	testutil.RequireEqualf(t, cfg.TLSCACertFile, "ca.pem", "grpc TLS CA cert file")
	testutil.RequireTruef(t, cfg.RequireCertificateIdentity, "require certificate identity")
}
