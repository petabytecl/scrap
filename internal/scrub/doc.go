// Package scrub implements light scrubbing (cross-replica consistency checks)
// and deep scrubbing (on-disk frame verification with repair), with I/O rate
// limiting, latency-based pausing, and OpenTelemetry metrics.
package scrub
