package main

import (
	"fmt"
	"io"
	"os"

	"github.com/petabytecl/scrap/internal/scrapctl"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if err := scrapctl.Run(args, stdout, stderr, scrapctl.Deps{}); err != nil {
		_, _ = fmt.Fprintf(stderr, "scrapctl: %v\n", err)
		return 1
	}
	return 0
}
