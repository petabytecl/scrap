package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/petabytecl/scrap/internal/scrub"
	"github.com/petabytecl/scrap/internal/server"
	"github.com/petabytecl/scrap/internal/shard"
	scraptelemetry "github.com/petabytecl/scrap/internal/telemetry"
	"github.com/petabytecl/scrap/internal/ulid"
)

var (
	version   = "dev"
	buildSHA  = "unknown"
	buildTime = "unknown"
)

const (
	localTelemetryIdentity = "local"
	memberIdentityDirMode  = 0o700
	memberIdentityFileMode = 0o600
	instrumentationScope   = "github.com/petabytecl/scrap/cmd/scrapd"
)

type scrapdTelemetryPipelineFactory struct {
	newMetricReader  func(context.Context) (sdkmetric.Reader, error)
	newSpanProcessor func(context.Context) (sdktrace.SpanProcessor, error)
}

var scrapdTelemetryPipeline = scrapdTelemetryPipelineFactory{
	newMetricReader: func(ctx context.Context) (sdkmetric.Reader, error) {
		exporter, err := otlpmetricgrpc.New(ctx)
		if err != nil {
			return nil, fmt.Errorf("create OTLP metric exporter: %w", err)
		}
		return sdkmetric.NewPeriodicReader(exporter), nil
	},
	newSpanProcessor: func(ctx context.Context) (sdktrace.SpanProcessor, error) {
		exporter, err := otlptracegrpc.New(ctx)
		if err != nil {
			return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
		}
		return sdktrace.NewBatchSpanProcessor(exporter), nil
	},
}

type scrapdTelemetryRuntime struct {
	meterProvider  *sdkmetric.MeterProvider
	tracerProvider *sdktrace.TracerProvider
	server         server.Telemetry
	runtimeMetrics *scraptelemetry.RuntimeMetrics
	raftMetrics    *scraptelemetry.RaftMetrics
	metricsHandler http.Handler
}

func newScrapdTelemetry(ctx context.Context, memberSlotID, memberID string, raftID, shardID uint64) (*scrapdTelemetryRuntime, error) {
	otelResource, err := scraptelemetry.NewResource(ctx, scrapdTelemetryResourceConfig(memberSlotID, memberID, raftID, shardID))
	if err != nil {
		return nil, err
	}

	metricReader, err := scrapdTelemetryPipeline.newMetricReader(ctx)
	if err != nil {
		return nil, err
	}
	if metricReader == nil {
		return nil, errors.New("metric reader is required")
	}

	spanProcessor, err := scrapdTelemetryPipeline.newSpanProcessor(ctx)
	if err != nil {
		_ = metricReader.Shutdown(ctx)
		return nil, err
	}
	if spanProcessor == nil {
		_ = metricReader.Shutdown(ctx)
		return nil, errors.New("span processor is required")
	}

	promRegistry := prometheus.NewRegistry()
	promExporter, err := otelprom.New(otelprom.WithRegisterer(promRegistry))
	if err != nil {
		_ = metricReader.Shutdown(ctx)
		_ = spanProcessor.Shutdown(ctx)
		return nil, fmt.Errorf("create prometheus exporter: %w", err)
	}

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(otelResource),
		sdkmetric.WithReader(metricReader),
		sdkmetric.WithReader(promExporter),
	)
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(otelResource),
		sdktrace.WithSpanProcessor(spanProcessor),
	)
	otel.SetMeterProvider(meterProvider)
	otel.SetTracerProvider(tracerProvider)

	m := meterProvider.Meter(instrumentationScope)
	t := tracerProvider.Tracer(instrumentationScope)

	serverTelemetry, err := server.NewOTelTelemetry(m, t)
	if err != nil {
		_ = meterProvider.Shutdown(ctx)
		_ = tracerProvider.Shutdown(ctx)
		return nil, fmt.Errorf("create server telemetry: %w", err)
	}

	runtimeMetrics, err := scraptelemetry.NewRuntimeMetrics(m)
	if err != nil {
		_ = meterProvider.Shutdown(ctx)
		_ = tracerProvider.Shutdown(ctx)
		return nil, fmt.Errorf("create runtime metrics: %w", err)
	}

	return &scrapdTelemetryRuntime{
		meterProvider:  meterProvider,
		tracerProvider: tracerProvider,
		server:         serverTelemetry,
		runtimeMetrics: runtimeMetrics,
		metricsHandler: promhttp.HandlerFor(promRegistry, promhttp.HandlerOpts{}),
	}, nil
}

func newScrapdTelemetryForHost(ctx context.Context, dataDir string, raftID, shardID uint64) (*scrapdTelemetryRuntime, error) {
	memberSlotID, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("hostname: %w", err)
	}
	memberID, err := resolveScrapdTelemetryMemberID(dataDir)
	if err != nil {
		return nil, err
	}
	return newScrapdTelemetry(ctx, memberSlotID, memberID, raftID, shardID)
}

type shardTelemetryBundle struct {
	uploadMetrics    *shard.UploadOTelMetrics
	scrubMetrics     *scrub.OTelScrubMetrics
	deepScrubMetrics *scrub.OTelDeepScrubMetrics
	writeTelemetry   *shard.WriteTelemetry
}

func (r *scrapdTelemetryRuntime) newShardTelemetry() (*shardTelemetryBundle, error) {
	m := r.meterProvider.Meter(instrumentationScope)
	t := r.tracerProvider.Tracer(instrumentationScope)

	um, err := shard.NewUploadOTelMetrics(m)
	if err != nil {
		return nil, fmt.Errorf("create upload metrics: %w", err)
	}
	sm, err := scrub.NewOTelScrubMetrics(m)
	if err != nil {
		return nil, fmt.Errorf("create scrub metrics: %w", err)
	}
	dsm, err := scrub.NewOTelDeepScrubMetrics(m)
	if err != nil {
		return nil, fmt.Errorf("create deep scrub metrics: %w", err)
	}
	wt, err := shard.NewWriteTelemetry(m, t)
	if err != nil {
		return nil, fmt.Errorf("create write telemetry: %w", err)
	}
	return &shardTelemetryBundle{
		uploadMetrics:    um,
		scrubMetrics:     sm,
		deepScrubMetrics: dsm,
		writeTelemetry:   wt,
	}, nil
}

func (r *scrapdTelemetryRuntime) registerRaftMetrics(provider scraptelemetry.RaftStateProvider) error {
	m := r.meterProvider.Meter(instrumentationScope)
	rm, err := scraptelemetry.NewRaftMetrics(m, provider)
	if err != nil {
		return err
	}
	r.raftMetrics = rm
	return nil
}

func (r *scrapdTelemetryRuntime) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}
	return errors.Join(
		r.runtimeMetrics.Unregister(),
		r.raftMetrics.Unregister(),
		r.meterProvider.Shutdown(ctx),
		r.tracerProvider.Shutdown(ctx),
	)
}

func scrapdTelemetryResourceConfig(memberSlotID, memberID string, raftID, shardID uint64) scraptelemetry.ResourceConfig {
	if memberSlotID == "" {
		memberSlotID = localTelemetryIdentity
	}
	if memberID == "" {
		memberID = localTelemetryIdentity
	}

	return scraptelemetry.ResourceConfig{
		ServiceName:  "scrapd",
		Environment:  envString("SCRAP_ENVIRONMENT", localTelemetryIdentity),
		Version:      version,
		BuildSHA:     buildSHA,
		BuildTime:    buildTime,
		CellID:       envString("SCRAP_CELL_ID", localTelemetryIdentity),
		MemberSlotID: memberSlotID,
		MemberID:     memberID,
		ShardID:      shardID,
		RaftID:       raftID,
	}
}

type memberIdentityRecord struct {
	MemberID string `json:"member_id"`
}

func resolveScrapdTelemetryMemberID(dataDir string) (string, error) {
	if memberID := strings.TrimSpace(os.Getenv("SCRAP_MEMBER_ID")); memberID != "" {
		return memberID, nil
	}
	if envString("SCRAP_CELL_ID", localTelemetryIdentity) == localTelemetryIdentity {
		return localTelemetryIdentity, nil
	}
	if strings.TrimSpace(dataDir) == "" {
		return "", errors.New("data directory is required to resolve member identity")
	}

	return readOrCreateTelemetryMemberID(filepath.Join(dataDir, "identity", "member.json"))
}

func readOrCreateTelemetryMemberID(path string) (string, error) {
	memberID, err := readTelemetryMemberID(path)
	if err == nil {
		return memberID, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return createTelemetryMemberID(path)
}

func readTelemetryMemberID(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is data-dir/identity/member.json, derived by resolveScrapdTelemetryMemberID.
	if err != nil {
		return "", fmt.Errorf("read member identity %s: %w", path, err)
	}

	var record memberIdentityRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return "", fmt.Errorf("decode member identity %s: %w", path, err)
	}

	memberID := strings.TrimSpace(record.MemberID)
	if memberID == "" {
		return "", fmt.Errorf("decode member identity %s: member_id is required", path)
	}
	return memberID, nil
}

func createTelemetryMemberID(path string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), memberIdentityDirMode); err != nil {
		return "", fmt.Errorf("create member identity directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, memberIdentityFileMode) //nolint:gosec // path is data-dir/identity/member.json, derived by resolveScrapdTelemetryMemberID.
	if errors.Is(err, os.ErrExist) {
		return readTelemetryMemberID(path)
	}
	if err != nil {
		return "", fmt.Errorf("create member identity %s: %w", path, err)
	}

	memberID := ulid.New().String()
	payload, err := json.MarshalIndent(memberIdentityRecord{MemberID: memberID}, "", "  ")
	if err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("encode member identity: %w", err)
	}
	payload = append(payload, '\n')

	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("write member identity %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close member identity %s: %w", path, err)
	}
	return memberID, nil
}

func envString(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
