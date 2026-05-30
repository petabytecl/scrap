package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc/codes"
)

func TestScrapdTelemetryResourceConfigUsesMemberAndBuildMetadata(t *testing.T) {
	t.Setenv("SCRAP_CELL_ID", "cell-a")
	t.Setenv("SCRAP_MEMBER_ID", "member-123")
	t.Setenv("SCRAP_ENVIRONMENT", "stress")

	build := BuildInfo{
		Version:   "v2.3.4",
		BuildSHA:  "abc123",
		BuildTime: "2026-05-28T12:00:00Z",
	}
	cfg := scrapdTelemetryResourceConfig("scrapd-0", "member-123", 3, 7, build)

	assertString(t, "ServiceName", cfg.ServiceName, "scrapd")
	assertString(t, "Environment", cfg.Environment, "stress")
	assertString(t, "Version", cfg.Version, "v2.3.4")
	assertString(t, "BuildSHA", cfg.BuildSHA, "abc123")
	assertString(t, "BuildTime", cfg.BuildTime, "2026-05-28T12:00:00Z")
	assertString(t, "CellID", cfg.CellID, "cell-a")
	assertString(t, "InstanceID", cfg.InstanceID, "scrapd-0")
	assertString(t, "MemberSlotID", cfg.MemberSlotID, "scrapd-0")
	assertString(t, "MemberID", cfg.MemberID, "member-123")
	if cfg.RaftID != 3 {
		t.Fatalf("RaftID = %d, want 3", cfg.RaftID)
	}
	if cfg.ShardID != 7 {
		t.Fatalf("ShardID = %d, want 7", cfg.ShardID)
	}
}

func TestScrapdTelemetryResourceConfigDefaultsLocalIdentity(t *testing.T) {
	t.Setenv("SCRAP_CELL_ID", "")
	t.Setenv("SCRAP_MEMBER_ID", "")
	t.Setenv("SCRAP_ENVIRONMENT", "")

	cfg := scrapdTelemetryResourceConfig("", "", 1, 0, BuildInfo{})

	assertString(t, "Environment", cfg.Environment, "local")
	assertString(t, "CellID", cfg.CellID, "local")
	assertString(t, "InstanceID", cfg.InstanceID, "local")
	assertString(t, "MemberSlotID", cfg.MemberSlotID, "local")
	assertString(t, "MemberID", cfg.MemberID, "local")
	if cfg.RaftID != 1 {
		t.Fatalf("RaftID = %d, want 1", cfg.RaftID)
	}
	if cfg.ShardID != 0 {
		t.Fatalf("ShardID = %d, want 0", cfg.ShardID)
	}
}

func TestScrapdTelemetryResourceConfigDerivesInstanceID(t *testing.T) {
	tests := map[string]struct {
		memberSlotID string
		memberID     string
		want         string
	}{
		"member slot identity wins": {
			memberSlotID: "scrapd-0",
			memberID:     "member-123",
			want:         "scrapd-0",
		},
		"durable member identity fallback": {
			memberID: "member-123",
			want:     "member-123",
		},
		"local fallback": {
			want: "local",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := scrapdTelemetryResourceConfig(tt.memberSlotID, tt.memberID, 1, 0, BuildInfo{})

			assertString(t, "InstanceID", cfg.InstanceID, tt.want)
		})
	}
}

func TestTelemetryResourceLogAttrsMatchResourceIdentity(t *testing.T) {
	cfg := scrapdTelemetryResourceConfig("scrapd-0", "member-123", 3, 7, BuildInfo{})

	attrs := logAttrMap(telemetryResourceLogAttrs(cfg))

	assertString(t, "service.instance.id", attrs["service.instance.id"], "scrapd-0")
	assertString(t, "scrap.cell_id", attrs["scrap.cell_id"], "local")
	assertString(t, "scrap.member_slot_id", attrs["scrap.member_slot_id"], "scrapd-0")
	assertString(t, "scrap.member_id", attrs["scrap.member_id"], "member-123")
	assertString(t, "scrap.shard_id", attrs["scrap.shard_id"], "7")
	assertString(t, "scrap.raft_id", attrs["scrap.raft_id"], "3")
}

func TestResolveScrapdTelemetryMemberIDUsesEnvOverride(t *testing.T) {
	t.Setenv("SCRAP_CELL_ID", "cell-a")
	t.Setenv("SCRAP_MEMBER_ID", "member-override")

	dir := t.TempDir()
	got, err := resolveScrapdTelemetryMemberID(dir)
	if err != nil {
		t.Fatalf("resolveScrapdTelemetryMemberID: %v", err)
	}
	assertString(t, "memberID", got, "member-override")

	path := filepath.Join(dir, "identity", "member.json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("member identity file err = %v, want not exist", err)
	}
}

func TestResolveScrapdTelemetryMemberIDDefaultsLocalOnlyForLocalCell(t *testing.T) {
	t.Setenv("SCRAP_CELL_ID", "")
	t.Setenv("SCRAP_MEMBER_ID", "")

	got, err := resolveScrapdTelemetryMemberID(t.TempDir())
	if err != nil {
		t.Fatalf("resolveScrapdTelemetryMemberID: %v", err)
	}
	assertString(t, "memberID", got, "local")
}

func TestResolveScrapdTelemetryMemberIDCreatesDurableRecordForNonLocalCell(t *testing.T) {
	t.Setenv("SCRAP_CELL_ID", "cell-a")
	t.Setenv("SCRAP_MEMBER_ID", "")

	dir := t.TempDir()
	memberID, err := resolveScrapdTelemetryMemberID(dir)
	if err != nil {
		t.Fatalf("resolveScrapdTelemetryMemberID: %v", err)
	}
	if memberID == "" || memberID == "local" {
		t.Fatalf("memberID = %q, want durable non-local identity", memberID)
	}

	path := filepath.Join(dir, "identity", "member.json")
	data, err := os.ReadFile(path) //nolint:gosec // test reads the identity file it just resolved under t.TempDir.
	if err != nil {
		t.Fatalf("ReadFile member identity: %v", err)
	}
	var record memberIdentityRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("Unmarshal member identity: %v", err)
	}
	assertString(t, "record.MemberID", record.MemberID, memberID)

	second, err := resolveScrapdTelemetryMemberID(dir)
	if err != nil {
		t.Fatalf("resolveScrapdTelemetryMemberID second call: %v", err)
	}
	assertString(t, "second memberID", second, memberID)
}

func TestResolveScrapdTelemetryMemberIDRejectsInvalidDurableRecord(t *testing.T) {
	t.Setenv("SCRAP_CELL_ID", "cell-a")
	t.Setenv("SCRAP_MEMBER_ID", "")

	path := filepath.Join(t.TempDir(), "identity", "member.json")
	if err := os.MkdirAll(filepath.Dir(path), memberIdentityDirMode); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"member_id":""}`), memberIdentityFileMode); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := resolveScrapdTelemetryMemberID(filepath.Dir(filepath.Dir(path)))
	if err == nil || !strings.Contains(err.Error(), "member_id is required") {
		t.Fatalf("invalid member identity error = %v, want member_id is required", err)
	}
}

func TestNewScrapdTelemetryInitializesRuntime(t *testing.T) {
	stubScrapdTelemetryPipeline(t)

	rt, err := newScrapdTelemetry(context.Background(), "scrapd-0", "member-123", 1, 0, BuildInfo{})
	if err != nil {
		t.Fatalf("newScrapdTelemetry: %v", err)
	}
	if rt.meterProvider == nil {
		t.Fatal("meterProvider is nil")
	}
	if rt.tracerProvider == nil {
		t.Fatal("tracerProvider is nil")
	}
	if rt.server == nil {
		t.Fatal("server telemetry is nil")
	}
	if err := rt.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestNewScrapdTelemetryForHostInitializesRuntime(t *testing.T) {
	stubScrapdTelemetryPipeline(t)

	rt, err := newScrapdTelemetryForHost(context.Background(), t.TempDir(), 1, 0, BuildInfo{})
	if err != nil {
		t.Fatalf("newScrapdTelemetryForHost: %v", err)
	}
	if rt.server == nil {
		t.Fatal("server telemetry is nil")
	}
	if err := rt.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestNewScrapdTelemetrySkipsOTLPWhenEndpointUnset(t *testing.T) {
	// No OTLP endpoint configured: the pipeline factory must not be invoked, and
	// the runtime must still expose a Prometheus /metrics handler.
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")

	var factoryCalled bool
	previous := scrapdTelemetryPipeline
	scrapdTelemetryPipeline = scrapdTelemetryPipelineFactory{
		newMetricReader: func(context.Context) (sdkmetric.Reader, error) {
			factoryCalled = true
			return sdkmetric.NewManualReader(), nil
		},
		newSpanProcessor: func(context.Context) (sdktrace.SpanProcessor, error) {
			factoryCalled = true
			return tracetest.NewSpanRecorder(), nil
		},
	}
	t.Cleanup(func() { scrapdTelemetryPipeline = previous })

	rt, err := newScrapdTelemetry(context.Background(), "scrapd-0", "member-123", 1, 0, BuildInfo{})
	if err != nil {
		t.Fatalf("newScrapdTelemetry: %v", err)
	}
	t.Cleanup(func() { _ = rt.Shutdown(context.Background()) })

	if factoryCalled {
		t.Fatal("OTLP pipeline factory invoked despite no OTEL_EXPORTER_OTLP_ENDPOINT")
	}
	if rt.metricsHandler == nil {
		t.Fatal("metricsHandler should be set so /metrics works without an OTLP collector")
	}
}

func TestNewScrapdTelemetryGatesSignalsIndependently(t *testing.T) {
	// Only a metrics endpoint is configured: the metric reader must be built, but
	// the span processor must not (it would otherwise fall back to localhost:4317).
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "otel-collector:4317")

	var metricBuilt, traceBuilt bool
	previous := scrapdTelemetryPipeline
	scrapdTelemetryPipeline = scrapdTelemetryPipelineFactory{
		newMetricReader: func(context.Context) (sdkmetric.Reader, error) {
			metricBuilt = true
			return sdkmetric.NewManualReader(), nil
		},
		newSpanProcessor: func(context.Context) (sdktrace.SpanProcessor, error) {
			traceBuilt = true
			return tracetest.NewSpanRecorder(), nil
		},
	}
	t.Cleanup(func() { scrapdTelemetryPipeline = previous })

	rt, err := newScrapdTelemetry(context.Background(), "scrapd-0", "member-123", 1, 0, BuildInfo{})
	if err != nil {
		t.Fatalf("newScrapdTelemetry: %v", err)
	}
	t.Cleanup(func() { _ = rt.Shutdown(context.Background()) })

	if !metricBuilt {
		t.Fatal("metric reader should be built when the metrics endpoint is set")
	}
	if traceBuilt {
		t.Fatal("span processor must not be built when only the metrics endpoint is set")
	}
}

func TestNewScrapdTelemetryUsesRPCDurationSecondBuckets(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "otel-collector:4317")

	reader := sdkmetric.NewManualReader()
	var traceBuilt bool
	previous := scrapdTelemetryPipeline
	scrapdTelemetryPipeline = scrapdTelemetryPipelineFactory{
		newMetricReader: func(context.Context) (sdkmetric.Reader, error) {
			return reader, nil
		},
		newSpanProcessor: func(context.Context) (sdktrace.SpanProcessor, error) {
			traceBuilt = true
			return tracetest.NewSpanRecorder(), nil
		},
	}
	t.Cleanup(func() { scrapdTelemetryPipeline = previous })

	ctx := context.Background()
	rt, err := newScrapdTelemetry(ctx, "scrapd-0", "member-123", 1, 0, BuildInfo{})
	if err != nil {
		t.Fatalf("newScrapdTelemetry: %v", err)
	}
	t.Cleanup(func() { _ = rt.Shutdown(context.Background()) })
	if traceBuilt {
		t.Fatal("span processor must not be built when only the metrics endpoint is set")
	}

	_, rpc := rt.server.StartRPC(ctx, "WriteDocument")
	rpc.Finish(codes.OK)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}

	duration := scrapdHistogramDataPoint(t, rm, "scrap.rpc.server.duration")
	want := []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
	if !slices.Equal(duration.Bounds, want) {
		t.Fatalf("duration bounds = %v, want %v", duration.Bounds, want)
	}
}

func TestScrapdTelemetryRuntimeShutdownNilReceiver(t *testing.T) {
	var rt *scrapdTelemetryRuntime
	if err := rt.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown nil receiver: %v", err)
	}
}

func stubScrapdTelemetryPipeline(t *testing.T) {
	t.Helper()

	// Configure an OTLP endpoint so newScrapdTelemetry exercises the OTLP branch
	// and uses the stubbed reader/processor below.
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317")

	previous := scrapdTelemetryPipeline
	scrapdTelemetryPipeline = scrapdTelemetryPipelineFactory{
		newMetricReader: func(context.Context) (sdkmetric.Reader, error) {
			return sdkmetric.NewManualReader(), nil
		},
		newSpanProcessor: func(context.Context) (sdktrace.SpanProcessor, error) {
			return tracetest.NewSpanRecorder(), nil
		},
	}
	t.Cleanup(func() { scrapdTelemetryPipeline = previous })
}

func scrapdHistogramDataPoint(t *testing.T, rm metricdata.ResourceMetrics, name string) metricdata.HistogramDataPoint[float64] {
	t.Helper()

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			hist, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("%s has data type %T, want float64 histogram", name, m.Data)
			}
			if len(hist.DataPoints) == 0 {
				t.Fatalf("%s has no data points", name)
			}
			return hist.DataPoints[0]
		}
	}
	t.Fatalf("metric %s not found", name)
	return metricdata.HistogramDataPoint[float64]{}
}

func assertString(t *testing.T, field, got, want string) {
	t.Helper()

	if got != want {
		t.Fatalf("%s = %q, want %q", field, got, want)
	}
}

func logAttrMap(attrs []any) map[string]string {
	out := make(map[string]string, len(attrs)/2)
	for i := 0; i < len(attrs)-1; i += 2 {
		key, ok := attrs[i].(string)
		if !ok {
			continue
		}
		value, ok := attrs[i+1].(string)
		if !ok {
			continue
		}
		out[key] = value
	}
	return out
}
