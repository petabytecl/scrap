package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/petabytecl/scrap/internal/crashfault"
)

var errParseFlags = errors.New("parse flags")

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	options, outPath, err := parseOptions(args, stderr)
	if err != nil {
		if errors.Is(err, errParseFlags) {
			return 2
		}
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return 2
	}
	report, err := crashfault.Run(context.Background(), options)
	if err != nil {
		return fail(stderr, "run crash/fault evidence", err)
	}
	data, err := crashfault.MarshalReport(report)
	if err != nil {
		return fail(stderr, "marshal crash/fault evidence", err)
	}
	if err := writeReport(outPath, stdout, data); err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return 2
	}
	if !report.Ready {
		return 1
	}
	return 0
}

func parseOptions(args []string, stderr io.Writer) (crashfault.Options, string, error) {
	fs := flag.NewFlagSet("scrap-crash-fault-evidence", flag.ContinueOnError)
	fs.SetOutput(stderr)
	outPath := fs.String("out", "", "write JSON evidence report to this path; defaults to stdout")
	runnerProfile := fs.String("runner-profile", "", "runner profile recorded in evidence")
	filesystemProfile := fs.String("filesystem-profile", "", "filesystem profile recorded in evidence")
	seed := fs.String("seed", "", "scenario seed or deterministic run label")
	artifactURI := fs.String("artifact-uri", "", "artifact URI written into release-gate evidence entries")
	count := fs.Int("count", 1, "go test -count value for each scenario command")
	race := fs.Bool("race", false, "run scenario tests with go test -race")
	if err := fs.Parse(args[1:]); err != nil {
		return crashfault.Options{}, "", fmt.Errorf("%w: %w", errParseFlags, err)
	}
	workDir, err := os.Getwd()
	if err != nil {
		return crashfault.Options{}, "", fmt.Errorf("get working directory: %w", err)
	}
	uri, err := artifactURIForOutput(*artifactURI, *outPath)
	if err != nil {
		return crashfault.Options{}, "", err
	}
	return crashfault.Options{
		WorkDir:           workDir,
		Count:             *count,
		Race:              *race,
		RunnerProfile:     *runnerProfile,
		FilesystemProfile: *filesystemProfile,
		Seed:              *seed,
		ArtifactURI:       uri,
		CommandLine:       append([]string(nil), args...),
	}, *outPath, nil
}

func artifactURIForOutput(artifactURI, outPath string) (string, error) {
	if artifactURI != "" || outPath == "" {
		return artifactURI, nil
	}
	abs, err := filepath.Abs(outPath)
	if err != nil {
		return "", fmt.Errorf("resolve evidence report path: %w", err)
	}
	return "file://" + abs, nil
}

func writeReport(outPath string, stdout io.Writer, data []byte) error {
	if outPath == "" {
		if _, err := stdout.Write(data); err != nil {
			return fmt.Errorf("write crash/fault evidence: %w", err)
		}
		return nil
	}
	// #nosec G304,G703 -- the report path is an explicit CLI destination.
	if err := os.WriteFile(outPath, data, 0o600); err != nil {
		return fmt.Errorf("write crash/fault evidence: %w", err)
	}
	return nil
}

func fail(stderr io.Writer, operation string, err error) int {
	_, _ = fmt.Fprintf(stderr, "%s: %v\n", operation, err)
	return 2
}
