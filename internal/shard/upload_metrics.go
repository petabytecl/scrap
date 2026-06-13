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

// The recordUpload*/setUpload* helpers and the upload processor moved to
// uploadController (upload_controller.go). This file now declares only the
// metrics contract the controller implements against.
