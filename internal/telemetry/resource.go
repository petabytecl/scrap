// Package telemetry builds OpenTelemetry resources and instruments for scrapd.
package telemetry

import (
	"context"
	"fmt"
	"strconv"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
)

const (
	defaultServiceName       = "scrapd"
	defaultServiceInstanceID = "local"
)

// ResourceConfig holds the bounded identity and build metadata attached to
// scrapd telemetry.
type ResourceConfig struct {
	// ServiceName is the OpenTelemetry service.name value.
	ServiceName string
	// Environment identifies the deployment environment for evidence runs.
	Environment string
	// InstanceID is the stable OpenTelemetry service.instance.id value.
	InstanceID string
	// Version identifies the scrapd release version.
	Version string
	// BuildSHA identifies the source revision used to build scrapd.
	BuildSHA string
	// BuildTime identifies when the scrapd binary was built.
	BuildTime string
	// CellID is the permanent identity of one S.C.R.A.P. Cell.
	CellID string
	// MemberSlotID is the StatefulSet slot or equivalent member slot identity.
	MemberSlotID string
	// MemberID is the durable identity stored with the member data volume.
	MemberID string
	// ShardID identifies the Shard hosted by this process.
	ShardID uint64
	// ShardIDLabel overrides ShardID when the process hosts an aggregate Shard set.
	ShardIDLabel string
	// RaftID identifies this member within the Shard's Raft group.
	RaftID uint64
	// SecurityMode is the bounded configured security mode.
	SecurityMode string
	// ProductionReadinessStatus is the bounded production-readiness state.
	ProductionReadinessStatus string
	// ProductionReadinessReason is the bounded reason for non-ready status.
	ProductionReadinessReason string
}

// NewResource returns the OpenTelemetry resource used by scrapd telemetry.
func NewResource(ctx context.Context, cfg ResourceConfig) (*resource.Resource, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(resourceAttributes(cfg)...),
	)
	if err != nil {
		return nil, fmt.Errorf("create telemetry resource: %w", err)
	}
	return res, nil
}

func resourceAttributes(cfg ResourceConfig) []attribute.KeyValue {
	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = defaultServiceName
	}

	attrs := []attribute.KeyValue{
		attribute.String("service.name", serviceName),
		attribute.String("service.instance.id", serviceInstanceID(cfg)),
		attribute.String("deployment.environment", cfg.Environment),
		attribute.String("service.version", cfg.Version),
		attribute.String("scrap.cell_id", cfg.CellID),
		attribute.String("scrap.member_slot_id", cfg.MemberSlotID),
		attribute.String("scrap.member_id", cfg.MemberID),
		attribute.String("scrap.shard_id", shardIDLabel(cfg)),
		attribute.String("scrap.raft_id", strconv.FormatUint(cfg.RaftID, 10)),
	}

	if cfg.BuildSHA != "" {
		attrs = append(attrs, attribute.String("scrap.build.sha", cfg.BuildSHA))
	}
	if cfg.BuildTime != "" {
		attrs = append(attrs, attribute.String("scrap.build.time", cfg.BuildTime))
	}
	if cfg.SecurityMode != "" {
		attrs = append(attrs, attribute.String("scrap.security_mode", cfg.SecurityMode))
	}
	if cfg.ProductionReadinessStatus != "" {
		attrs = append(attrs, attribute.String("scrap.production_readiness.status", cfg.ProductionReadinessStatus))
	}
	if cfg.ProductionReadinessReason != "" {
		attrs = append(attrs, attribute.String("scrap.production_readiness.reason", cfg.ProductionReadinessReason))
	}
	return attrs
}

func shardIDLabel(cfg ResourceConfig) string {
	if cfg.ShardIDLabel != "" {
		return cfg.ShardIDLabel
	}
	return strconv.FormatUint(cfg.ShardID, 10)
}

func serviceInstanceID(cfg ResourceConfig) string {
	if cfg.InstanceID != "" {
		return cfg.InstanceID
	}
	if cfg.MemberSlotID != "" {
		return cfg.MemberSlotID
	}
	if cfg.MemberID != "" {
		return cfg.MemberID
	}
	return defaultServiceInstanceID
}
