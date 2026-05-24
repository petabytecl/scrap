package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/petabytecl/scrap/internal/closeutil"
	"github.com/petabytecl/scrap/internal/localstorage"
	"github.com/petabytecl/scrap/internal/writepipelineevidence"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scrap-write-pipeline-evidence", flag.ContinueOnError)
	fs.SetOutput(stderr)
	outPath := fs.String("out", writepipelineevidence.DefaultReportPath, "output report path, or - for stdout")
	dir := fs.String("dir", "", "scratch data directory; defaults to a temporary directory")
	releaseSHA := fs.String("release-sha", "", "release SHA recorded in evidence")
	dirtyTree := fs.String("dirty-tree", "", "dirty tree state recorded in evidence")
	runnerID := fs.String("runner-id", writepipelineevidence.DefaultRunnerID, "runner/profile identifier")
	samples := fs.Int("samples", writepipelineevidence.DefaultSampleCount, "document write sample count")
	concurrency := fs.Int("concurrency", writepipelineevidence.DefaultConcurrency, "concurrent document writers")
	documentSize := fs.Uint64("document-size", writepipelineevidence.DefaultDocumentSizeBytes, "document size in bytes")
	duration := fs.Duration("duration", writepipelineevidence.DefaultMaxDuration, "bounded campaign timeout")
	minWritesPerSecond := fs.Float64("min-writes-per-second", 0, "optional minimum writes/sec threshold")
	maxP99AckLatency := fs.Duration("max-p99-ack-latency", 0, "optional maximum p99 ACK latency threshold")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintf(stderr, "unexpected arguments: %s\n", strings.Join(fs.Args(), " "))
		return 2
	}

	opts := writepipelineevidence.Options{
		ReleaseSHA:         *releaseSHA,
		DirtyTree:          *dirtyTree,
		RunnerID:           *runnerID,
		SampleCount:        *samples,
		Concurrency:        *concurrency,
		DocumentSizeBytes:  *documentSize,
		MaxDuration:        *duration,
		MinWritesPerSecond: *minWritesPerSecond,
		MaxP99AckLatency:   *maxP99AckLatency,
	}
	if err := runEvidence(context.Background(), opts, *dir, *outPath, stdout); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func runEvidence(ctx context.Context, opts writepipelineevidence.Options, dir, outPath string, stdout io.Writer) error {
	dataDir := dir
	if dataDir == "" {
		tempDir, err := os.MkdirTemp("", "scrap-write-pipeline-evidence-*")
		if err != nil {
			return fmt.Errorf("create scratch dir: %w", err)
		}
		defer func() {
			_ = os.RemoveAll(tempDir)
		}()
		dataDir = tempDir
	}

	app, err := localstorage.OpenWithContext(ctx, dataDir)
	if err != nil {
		return fmt.Errorf("open local storage application: %w", err)
	}
	defer closeutil.Ignore(app)

	report, runErr := writepipelineevidence.Run(ctx, opts, app)
	if report.ReportKind != "" {
		if err := writeReport(outPath, stdout, report); err != nil {
			return err
		}
	}
	return runErr
}

func writeReport(path string, stdout io.Writer, report writepipelineevidence.Report) error {
	writer := stdout
	var file *os.File
	if path != "-" {
		var err error
		// #nosec G304 -- the report path is an operator-provided local evidence output path.
		file, err = os.Create(path)
		if err != nil {
			return fmt.Errorf("create report: %w", err)
		}
		defer closeutil.Ignore(file)
		writer = file
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
