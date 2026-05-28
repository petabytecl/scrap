package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/petabytecl/scrap/internal/server"
	scraptelemetry "github.com/petabytecl/scrap/internal/telemetry"
)

var (
	version   = "dev"
	buildSHA  = "unknown"
	buildTime = "unknown"
)

type scrapdTelemetryRuntime struct {
	meterProvider  *sdkmetric.MeterProvider
	tracerProvider *sdktrace.TracerProvider
	server         server.Telemetry
}

func newScrapdTelemetry(ctx context.Context, memberSlotID string, raftID, shardID uint64) (*scrapdTelemetryRuntime, error) {
	otelResource, err := scraptelemetry.NewResource(ctx, scrapdTelemetryResourceConfig(memberSlotID, raftID, shardID))
	if err != nil {
		return nil, err
	}

	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithResource(otelResource))
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithResource(otelResource))
	otel.SetMeterProvider(meterProvider)
	otel.SetTracerProvider(tracerProvider)

	serverTelemetry, err := server.NewOTelTelemetry(
		meterProvider.Meter("github.com/petabytecl/scrap/cmd/scrapd"),
		tracerProvider.Tracer("github.com/petabytecl/scrap/cmd/scrapd"),
	)
	if err != nil {
		_ = meterProvider.Shutdown(ctx)
		_ = tracerProvider.Shutdown(ctx)
		return nil, fmt.Errorf("create server telemetry: %w", err)
	}

	return &scrapdTelemetryRuntime{
		meterProvider:  meterProvider,
		tracerProvider: tracerProvider,
		server:         serverTelemetry,
	}, nil
}

func (r *scrapdTelemetryRuntime) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}
	return errors.Join(
		r.meterProvider.Shutdown(ctx),
		r.tracerProvider.Shutdown(ctx),
	)
}

func scrapdTelemetryResourceConfig(memberSlotID string, raftID, shardID uint64) scraptelemetry.ResourceConfig {
	if memberSlotID == "" {
		memberSlotID = "local"
	}

	return scraptelemetry.ResourceConfig{
		ServiceName:  "scrapd",
		Environment:  envString("SCRAP_ENVIRONMENT", "local"),
		Version:      version,
		BuildSHA:     buildSHA,
		BuildTime:    buildTime,
		CellID:       envString("SCRAP_CELL_ID", "local"),
		MemberSlotID: memberSlotID,
		MemberID:     envString("SCRAP_MEMBER_ID", "local"),
		ShardID:      shardID,
		RaftID:       raftID,
	}
}

func envString(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
