package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/petabytecl/scrap/internal/closeutil"
	"github.com/petabytecl/scrap/internal/releasegate"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scrap-release-gate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	tierValue := fs.String("tier", string(releasegate.TierRelease), "release gate tier: normal_pr, nightly, dedicated_runner, release")
	manifestPath := fs.String("manifest", "", "release evidence manifest JSON path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintf(stderr, "unexpected arguments: %v\n", fs.Args())
		return 2
	}
	tier, err := releasegate.ParseTier(*tierValue)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	manifest := releasegate.Manifest{}
	if *manifestPath != "" {
		file, err := os.Open(*manifestPath)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "open manifest: %v\n", err)
			return 2
		}
		defer closeutil.Ignore(file)
		manifest, err = releasegate.ReadManifest(file)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "read manifest: %v\n", err)
			return 2
		}
	}
	report := releasegate.Evaluate(tier, manifest)
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		_, _ = fmt.Fprintf(stderr, "write report: %v\n", err)
		return 2
	}
	if !report.Ready {
		return 1
	}
	return 0
}
