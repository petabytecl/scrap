package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/petabytecl/scrap/internal/scrub"
	"github.com/petabytecl/scrap/internal/server"
	"github.com/petabytecl/scrap/internal/shard"
	scraptelemetry "github.com/petabytecl/scrap/internal/telemetry"
	"github.com/petabytecl/scrap/internal/ulid"
)

const (
	localTelemetryIdentity = "local"
	memberIdentityDirMode  = 0o700
	memberIdentityFileMode = 0o600
	instrumentationScope   = "github.com/petabytecl/scrap/cmd/scrapd"
	rpcServerDurationName  = "scrap.rpc.server.duration"
	secondsUnit            = "s"
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

// buildEnabled creates the OTLP metric reader and/or span processor, but only for
// the signals whose endpoint is configured. Each signal is gated independently:
// configuring only OTEL_EXPORTER_OTLP_METRICS_ENDPOINT must not also build a trace
// exporter (which would fall back to localhost:4317). A nil return for a signal
// means it is disabled. The reader is shut down if the span processor then fails.
func (f scrapdTelemetryPipelineFactory) buildEnabled(ctx context.Context) (sdkmetric.Reader, sdktrace.SpanProcessor, error) {
	var (
		metricReader  sdkmetric.Reader
		spanProcessor sdktrace.SpanProcessor
		err           error
	)

	if otlpSignalEnabled("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") {
		if metricReader, err = f.newMetricReader(ctx); err != nil {
			return nil, nil, err
		}
	}
	if otlpSignalEnabled("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") {
		if spanProcessor, err = f.newSpanProcessor(ctx); err != nil {
			if metricReader != nil {
				_ = metricReader.Shutdown(ctx)
			}
			return nil, nil, err
		}
	}
	return metricReader, spanProcessor, nil
}

// otlpSignalEnabled reports whether the given signal-specific OTLP endpoint, or
// the common OTEL_EXPORTER_OTLP_ENDPOINT, is configured. When neither is set, the
// signal is not exported, so scrapd never defaults to localhost:4317.
func otlpSignalEnabled(signalEnv string) bool {
	for _, key := range []string{"OTEL_EXPORTER_OTLP_ENDPOINT", signalEnv} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return true
		}
	}
	return false
}

type scrapdTelemetryRuntime struct {
	meterProvider  *sdkmetric.MeterProvider
	tracerProvider *sdktrace.TracerProvider
	server         server.Telemetry
	runtimeMetrics *scraptelemetry.RuntimeMetrics
	raftMetrics    *scraptelemetry.RaftMetrics
	diskMetrics    *scraptelemetry.DiskMetrics
	metricsHandler http.Handler
	resourceConfig scraptelemetry.ResourceConfig
}

func newScrapdTelemetry(ctx context.Context, memberSlotID, memberID string, raftID, shardID uint64, build BuildInfo) (*scrapdTelemetryRuntime, error) {
	resourceConfig := scrapdTelemetryResourceConfig(memberSlotID, memberID, raftID, shardID, build)
	otelResource, err := scraptelemetry.NewResource(ctx, resourceConfig)
	if err != nil {
		return nil, err
	}

	// The Prometheus reader is always present: it backs the admin /metrics
	// endpoint regardless of whether an OTLP collector is configured.
	promRegistry := prometheus.NewRegistry()
	promExporter, err := otelprom.New(otelprom.WithRegisterer(promRegistry))
	if err != nil {
		return nil, fmt.Errorf("create prometheus exporter: %w", err)
	}

	meterOpts := []sdkmetric.Option{
		sdkmetric.WithResource(otelResource),
		sdkmetric.WithReader(promExporter),
		sdkmetric.WithExemplarFilter(exemplar.AlwaysOffFilter),
		sdkmetric.WithView(scrapdRPCServerDurationView()),
	}
	tracerOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(otelResource),
	}

	// Only export over OTLP for signals whose endpoint is configured. Without this
	//  guard, the OTLP exporters default to localhost:4317 and silently ship to a
	// non-existent collector on every deployment that has not wired one up.
	metricReader, spanProcessor, err := scrapdTelemetryPipeline.buildEnabled(ctx)
	if err != nil {
		return nil, err
	}
	if metricReader != nil {
		meterOpts = append(meterOpts, sdkmetric.WithReader(metricReader))
	}
	if spanProcessor != nil {
		tracerOpts = append(tracerOpts, sdktrace.WithSpanProcessor(spanProcessor))
	}

	meterProvider := sdkmetric.NewMeterProvider(meterOpts...)
	tracerProvider := sdktrace.NewTracerProvider(tracerOpts...)
	otel.SetMeterProvider(meterProvider)
	otel.SetTracerProvider(tracerProvider)
	// W3C trace context + baggage on every gRPC hop (client<->server and
	// leader<->peer). The otelgrpc handlers default to the global propagator, so
	// without this every WriteDocument starts a disconnected root span, and the
	// client's trace_id is dropped at the boundary. See ADR 0013.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

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
		resourceConfig: resourceConfig,
	}, nil
}

func (r *scrapdTelemetryRuntime) logIdentityAttrs() []any {
	if r == nil {
		return nil
	}
	return telemetryResourceLogAttrs(r.resourceConfig)
}

func telemetryResourceLogAttrs(cfg scraptelemetry.ResourceConfig) []any {
	return []any{
		"service.instance.id", cfg.InstanceID,
		"scrap.cell_id", cfg.CellID,
		"scrap.member_slot_id", cfg.MemberSlotID,
		"scrap.member_id", cfg.MemberID,
		"scrap.shard_id", strconv.FormatUint(cfg.ShardID, 10),
		"scrap.raft_id", strconv.FormatUint(cfg.RaftID, 10),
	}
}

func scrapdRPCServerDurationView() sdkmetric.View {
	return sdkmetric.NewView(
		sdkmetric.Instrument{
			Name: rpcServerDurationName,
			Unit: secondsUnit,
		},
		sdkmetric.Stream{
			Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
				Boundaries: scrapdRPCServerDurationSecondBuckets(),
			},
		},
	)
}

func scrapdRPCServerDurationSecondBuckets() []float64 {
	return []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
}

func newScrapdTelemetryForHost(ctx context.Context, dataDir string, raftID, shardID uint64, build BuildInfo) (*scrapdTelemetryRuntime, error) {
	memberSlotID, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("hostname: %w", err)
	}
	memberID, err := resolveScrapdTelemetryMemberID(dataDir)
	if err != nil {
		return nil, err
	}
	return newScrapdTelemetry(ctx, memberSlotID, memberID, raftID, shardID, build)
}

type shardTelemetryBundle struct {
	uploadMetrics    *shard.UploadOTelMetrics
	evictionMetrics  *shard.EvictionOTelMetrics
	scrubMetrics     *scrub.OTelMetrics
	deepScrubMetrics *scrub.OTelDeepMetrics
	writeTelemetry   *shard.WriteTelemetry
}

func (r *scrapdTelemetryRuntime) newShardTelemetry() (*shardTelemetryBundle, error) {
	m := r.meterProvider.Meter(instrumentationScope)
	t := r.tracerProvider.Tracer(instrumentationScope)

	um, err := shard.NewUploadOTelMetrics(m)
	if err != nil {
		return nil, fmt.Errorf("create upload metrics: %w", err)
	}
	em, err := shard.NewEvictionOTelMetrics(m)
	if err != nil {
		return nil, fmt.Errorf("create eviction metrics: %w", err)
	}
	sm, err := scrub.NewOTelMetrics(m)
	if err != nil {
		return nil, fmt.Errorf("create scrub metrics: %w", err)
	}
	dsm, err := scrub.NewOTelDeepMetrics(m)
	if err != nil {
		return nil, fmt.Errorf("create deep scrub metrics: %w", err)
	}
	wt, err := shard.NewWriteTelemetry(m, t)
	if err != nil {
		return nil, fmt.Errorf("create write telemetry: %w", err)
	}
	return &shardTelemetryBundle{
		uploadMetrics:    um,
		evictionMetrics:  em,
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

func (r *scrapdTelemetryRuntime) registerDiskMetrics(provider scraptelemetry.DiskStatsProvider) error {
	m := r.meterProvider.Meter(instrumentationScope)
	dm, err := scraptelemetry.NewDiskMetrics(m, provider)
	if err != nil {
		return err
	}
	r.diskMetrics = dm
	return nil
}

func (r *scrapdTelemetryRuntime) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}
	return errors.Join(
		r.runtimeMetrics.Unregister(),
		r.raftMetrics.Unregister(),
		r.diskMetrics.Unregister(),
		r.meterProvider.Shutdown(ctx),
		r.tracerProvider.Shutdown(ctx),
	)
}

func scrapdTelemetryResourceConfig(memberSlotID, memberID string, raftID, shardID uint64, build BuildInfo) scraptelemetry.ResourceConfig {
	build = build.withDefaults()
	instanceID := scrapdTelemetryInstanceID(memberSlotID, memberID)
	if memberSlotID == "" {
		memberSlotID = localTelemetryIdentity
	}
	if memberID == "" {
		memberID = localTelemetryIdentity
	}

	return scraptelemetry.ResourceConfig{
		ServiceName:  "scrapd",
		Environment:  envString("SCRAP_ENVIRONMENT", localTelemetryIdentity),
		Version:      build.Version,
		BuildSHA:     build.BuildSHA,
		BuildTime:    build.BuildTime,
		CellID:       envString("SCRAP_CELL_ID", localTelemetryIdentity),
		InstanceID:   instanceID,
		MemberSlotID: memberSlotID,
		MemberID:     memberID,
		ShardID:      shardID,
		RaftID:       raftID,
	}
}

func scrapdTelemetryInstanceID(memberSlotID, memberID string) string {
	if memberSlotID != "" {
		return memberSlotID
	}
	if memberID != "" {
		return memberID
	}
	return localTelemetryIdentity
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
