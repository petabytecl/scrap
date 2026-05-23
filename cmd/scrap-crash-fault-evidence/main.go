package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/petabytecl/scrap/internal/crashfault"
)

func main() {
	os.Exit(run(os.Args))
}

func run(args []string) int {
	fs := flag.NewFlagSet("scrap-crash-fault-evidence", flag.ContinueOnError)
	outPath := fs.String("out", "", "write JSON evidence report to this path; defaults to stdout")
	runnerProfile := fs.String("runner-profile", "", "runner profile recorded in evidence")
	filesystemProfile := fs.String("filesystem-profile", "", "filesystem profile recorded in evidence")
	seed := fs.String("seed", "", "scenario seed or deterministic run label")
	artifactURI := fs.String("artifact-uri", "", "artifact URI written into release-gate evidence entries")
	count := fs.Int("count", 1, "go test -count value for each scenario command")
	race := fs.Bool("race", false, "run scenario tests with go test -race")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	workDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get working directory: %v\n", err)
		return 2
	}
	if *artifactURI == "" && *outPath != "" {
		abs, err := filepath.Abs(*outPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "resolve evidence report path: %v\n", err)
			return 2
		}
		*artifactURI = "file://" + abs
	}
	report, err := crashfault.Run(context.Background(), crashfault.Options{
		WorkDir:           workDir,
		Count:             *count,
		Race:              *race,
		RunnerProfile:     *runnerProfile,
		FilesystemProfile: *filesystemProfile,
		Seed:              *seed,
		ArtifactURI:       *artifactURI,
		CommandLine:       append([]string(nil), args...),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "run crash/fault evidence: %v\n", err)
		return 2
	}
	data, err := crashfault.MarshalReport(report)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal crash/fault evidence: %v\n", err)
		return 2
	}
	if *outPath == "" {
		if _, err := os.Stdout.Write(data); err != nil {
			fmt.Fprintf(os.Stderr, "write crash/fault evidence: %v\n", err)
			return 2
		}
	} else if err := os.WriteFile(*outPath, data, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "write crash/fault evidence: %v\n", err)
		return 2
	}
	if !report.Ready {
		return 1
	}
	return 0
}
