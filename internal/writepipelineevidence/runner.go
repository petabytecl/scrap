package writepipelineevidence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/safeconv"
	"github.com/petabytecl/scrap/internal/storageapp"
)

const (
	DefaultSampleCount              = 128
	DefaultConcurrency              = 8
	DefaultDocumentSizeBytes uint64 = 4 * 1024
	DefaultMaxDuration              = 30 * time.Second
	DefaultReportPath               = "write-pipeline-performance-evidence.json"
	DefaultRunnerID                 = "local-application"
	MaxSampleCount                  = 100000
	MaxConcurrency                  = 512
	MaxDocumentSizeBytes     uint64 = 16 * 1024 * 1024
	MaxRunDuration                  = 10 * time.Minute
	payloadPatternModulo            = 251
)

type WriteApplication interface {
	WriteDocument(context.Context, storageapp.WriteDocumentInit, storageapp.ChunkReader) (storageapp.WriteDocumentResult, error)
}

func Run(ctx context.Context, opts Options, app WriteApplication) (Report, error) {
	opts, err := ValidateOptions(opts)
	if err != nil {
		return Report{}, err
	}
	if app == nil {
		return Report{}, errors.New("write pipeline evidence requires write application")
	}

	ctx, cancel := context.WithTimeout(ctx, opts.MaxDuration)
	defer cancel()

	startedAt := time.Now().UTC()
	runID := strconv.FormatInt(startedAt.UnixNano(), 10)
	jobs := make(chan int, opts.SampleCount)
	results := make(chan WriteSample, opts.SampleCount)
	workers := min(opts.Concurrency, opts.SampleCount)
	sampler := startComponentSignalSampler()

	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for index := range jobs {
				results <- runWriteSample(ctx, opts, app, runID, index)
			}
		}()
	}
	for index := range opts.SampleCount {
		jobs <- index
	}
	close(jobs)
	wg.Wait()
	close(results)

	samples := make([]WriteSample, 0, opts.SampleCount)
	for sample := range results {
		samples = append(samples, sample)
	}
	sortSamples(samples)
	opts.ComponentSignals = sampler.stopAndBuild(opts.RequireSignals)
	report := BuildReport(opts, samples, startedAt, time.Now().UTC())
	if report.Status != StatusPassed {
		return report, fmt.Errorf("write pipeline evidence status %s; update #148 with the blocking issue or threshold exception", report.Status)
	}
	return report, nil
}

func ValidateOptions(opts Options) (Options, error) {
	opts = defaultRunOptions(defaultOptions(opts))
	if err := validateRunShape(opts); err != nil {
		return Options{}, err
	}
	if err := validateThresholds(opts); err != nil {
		return Options{}, err
	}
	return opts, nil
}

func defaultRunOptions(opts Options) Options {
	if opts.SampleCount == 0 {
		opts.SampleCount = DefaultSampleCount
	}
	if opts.Concurrency == 0 {
		opts.Concurrency = DefaultConcurrency
	}
	if opts.DocumentSizeBytes == 0 {
		opts.DocumentSizeBytes = DefaultDocumentSizeBytes
	}
	if opts.MaxDuration == 0 {
		opts.MaxDuration = DefaultMaxDuration
	}
	return opts
}

func validateRunShape(opts Options) error {
	if opts.SampleCount < 1 || opts.SampleCount > MaxSampleCount {
		return fmt.Errorf("write pipeline --samples must be 1..%d", MaxSampleCount)
	}
	if opts.Concurrency < 1 || opts.Concurrency > MaxConcurrency {
		return fmt.Errorf("write pipeline --concurrency must be 1..%d", MaxConcurrency)
	}
	if opts.DocumentSizeBytes < 1 || opts.DocumentSizeBytes > MaxDocumentSizeBytes {
		return fmt.Errorf("write pipeline --document-size must be 1..%d bytes", MaxDocumentSizeBytes)
	}
	if opts.MaxDuration <= 0 || opts.MaxDuration > MaxRunDuration {
		return fmt.Errorf("write pipeline --duration must be positive and no more than %s", MaxRunDuration)
	}
	return nil
}

func validateThresholds(opts Options) error {
	if opts.MinWritesPerSecond < 0 {
		return errors.New("write pipeline --min-writes-per-second must be non-negative")
	}
	if opts.MaxP99AckLatency < 0 {
		return errors.New("write pipeline --max-p99-ack-latency must be non-negative")
	}
	return nil
}

func runWriteSample(ctx context.Context, opts Options, app WriteApplication, runID string, index int) WriteSample {
	documentName := fmt.Sprintf("doc-%06d.bin", index)
	payloadSize, err := safeconv.Uint64ToInt("write pipeline document size", opts.DocumentSizeBytes)
	if err != nil {
		return failedSample(documentName, opts.DocumentSizeBytes, err)
	}
	payload := documentPayload(payloadSize)
	expectedLength := uint64(len(payload))
	sum := sha256.Sum256(payload)
	init := storageapp.WriteDocumentInit{
		Identity: identity.Document{
			TenantID:      "write-pipeline",
			TransactionID: "run-" + runID,
			DocumentName:  documentName,
		},
		DocumentClass:        storageapp.DocumentClassPermanent,
		PriorityClass:        storageapp.PriorityClassNormal,
		ExpectedLength:       &expectedLength,
		ExpectedSHA256:       sum[:],
		ClientIdempotencyKey: fmt.Sprintf("write-pipeline-%s-%06d", runID, index),
		CreatedByService:     "scrap-write-pipeline-evidence",
		WorkflowStage:        "performance-smoke",
		Tags: map[string]string{
			"issue":  "148",
			"runner": opts.RunnerID,
		},
	}

	started := time.Now()
	result, err := app.WriteDocument(ctx, init, &singleChunkReader{chunk: payload})
	latency := time.Since(started).Microseconds()
	sample := WriteSample{
		DocumentName:  documentName,
		Bytes:         expectedLength,
		LatencyMicros: latency,
	}
	if err != nil {
		sample.ErrorClass = classifyError(err)
		sample.Error = err.Error()
		return sample
	}
	if result.Metadata.Length != expectedLength {
		sample.ErrorClass = "invalid-result"
		sample.Error = fmt.Sprintf("metadata length %d does not match expected %d", result.Metadata.Length, expectedLength)
	}
	return sample
}

func failedSample(documentName string, bytes uint64, err error) WriteSample {
	return WriteSample{
		DocumentName:  documentName,
		Bytes:         bytes,
		LatencyMicros: 0,
		ErrorClass:    classifyError(err),
		Error:         err.Error(),
	}
}

func documentPayload(size int) []byte {
	payload := make([]byte, size)
	var value byte
	for i := range payload {
		payload[i] = value
		value++
		if value == payloadPatternModulo {
			value = 0
		}
	}
	return payload
}

type singleChunkReader struct {
	chunk []byte
	read  bool
}

func (r *singleChunkReader) Recv() ([]byte, error) {
	if r.read {
		return nil, io.EOF
	}
	r.read = true
	return bytes.Clone(r.chunk), nil
}

func classifyError(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "transient"
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "timeout"):
		return "transient"
	case strings.Contains(text, "closed"):
		return "closed"
	default:
		return "error"
	}
}

func sortSamples(samples []WriteSample) {
	sort.Slice(samples, func(i, j int) bool {
		return samples[i].DocumentName < samples[j].DocumentName
	})
}
