package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/petabytecl/scrap/internal/releasegate"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("scrap-release-gate", flag.ContinueOnError)
	tierValue := fs.String("tier", string(releasegate.TierRelease), "release gate tier: normal_pr, nightly, dedicated_runner, release")
	manifestPath := fs.String("manifest", "", "release evidence manifest JSON path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	tier, err := releasegate.ParseTier(*tierValue)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	manifest := releasegate.Manifest{}
	if *manifestPath != "" {
		file, err := os.Open(*manifestPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "open manifest: %v\n", err)
			return 2
		}
		defer file.Close()
		manifest, err = releasegate.ReadManifest(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read manifest: %v\n", err)
			return 2
		}
	}
	report := releasegate.Evaluate(tier, manifest)
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintf(os.Stderr, "write report: %v\n", err)
		return 2
	}
	if !report.Ready {
		return 1
	}
	return 0
}
