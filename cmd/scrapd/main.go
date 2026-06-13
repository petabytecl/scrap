package main

import (
	"fmt"
	"os"

	internalcmd "github.com/petabytecl/scrap/internal/cmd"
)

var (
	version   = "dev"
	buildSHA  = "unknown"
	buildTime = "unknown"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := internalcmd.RunHealthcheck(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
			os.Exit(1)
		}
		return
	}

	build := internalcmd.BuildInfo{
		Version:   version,
		BuildSHA:  buildSHA,
		BuildTime: buildTime,
	}
	if err := internalcmd.Run(os.Args[1:], os.Stderr, build); err != nil {
		fmt.Fprintf(os.Stderr, "scrapd: %v\n", err)
		os.Exit(1)
	}
}
