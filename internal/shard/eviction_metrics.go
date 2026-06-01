package shard

import (
	"time"

	"github.com/petabytecl/scrap/internal/eviction"
)

type EvictionMetrics interface {
	RecordPlan(shardID uint64, plan eviction.Plan)
	RecordApply(shardID uint64, reason, status string, duration time.Duration, skipCountsByReason map[string]int)
	RecordRestore(shardID uint64, reason, result, failureReason string, duration time.Duration)
	SetHealth(shardID uint64, snapshot eviction.HealthSnapshot)
}

type noopEvictionMetrics struct{}

func (noopEvictionMetrics) RecordPlan(uint64, eviction.Plan) {}

func (noopEvictionMetrics) RecordApply(uint64, string, string, time.Duration, map[string]int) {}

func (noopEvictionMetrics) RecordRestore(uint64, string, string, string, time.Duration) {}

func (noopEvictionMetrics) SetHealth(uint64, eviction.HealthSnapshot) {}

func (s *Shard) evictionMetricRecorder() EvictionMetrics {
	if s.evictionMetrics == nil {
		return noopEvictionMetrics{}
	}
	return s.evictionMetrics
}
