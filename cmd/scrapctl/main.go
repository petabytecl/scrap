package main

import (
	"fmt"
	"os"

	"github.com/petabytecl/scrap/internal/scrapctl"
)

func main() {
	if err := scrapctl.Run(os.Args[1:], os.Stdout, os.Stderr, scrapctl.Deps{}); err != nil {
		fmt.Fprintf(os.Stderr, "scrapctl: %v\n", err)
		os.Exit(1)
	}
}
