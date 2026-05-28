package shard

import "time"

type UploadMetrics interface {
	SetPending(shardID uint64, pendingBytes int64, pendingBlocks int)
	RecordUpload(shardID uint64, status string, duration time.Duration)
	RecordVerify(shardID uint64, status string)
	SetPressureLevel(shardID uint64, level UploadPressureLevel)
	SetConcurrency(shardID uint64, concurrency int)
	SetAuthPaused(shardID uint64, paused bool)
}

func (s *Shard) recordUploadPressureMetrics(stats uploadPressureStats, level UploadPressureLevel, concurrency int) {
	if s.upload.Metrics == nil {
		return
	}
	s.upload.Metrics.SetPending(s.shardID, stats.pendingBytes, stats.pendingBlocks)
	s.upload.Metrics.SetPressureLevel(s.shardID, level)
	s.upload.Metrics.SetConcurrency(s.shardID, concurrency)
}

func (s *Shard) recordUploadMetric(status string, duration time.Duration) {
	if s.upload.Metrics != nil {
		s.upload.Metrics.RecordUpload(s.shardID, status, duration)
	}
}

func (s *Shard) recordUploadVerifyMetric(status string) {
	if s.upload.Metrics != nil {
		s.upload.Metrics.RecordVerify(s.shardID, status)
	}
}

func (s *Shard) setUploadConcurrencyMetric(concurrency int) {
	if s.upload.Metrics != nil {
		s.upload.Metrics.SetConcurrency(s.shardID, concurrency)
	}
}

func (s *Shard) setUploadAuthPausedMetric(paused bool) {
	if s.upload.Metrics != nil {
		s.upload.Metrics.SetAuthPaused(s.shardID, paused)
	}
}
