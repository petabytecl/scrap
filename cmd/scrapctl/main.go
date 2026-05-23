package main

import (
	"os"

	"github.com/petabytecl/scrap/internal/scrapctl"
)

func main() {
	os.Exit(scrapctl.Main(os.Args[1:], os.Stdout, os.Stderr))
}
