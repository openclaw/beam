// Command beam-clean is the offline protocol-1 executable for host integrations.
package main

import (
	"fmt"
	"os"
	"slices"

	"github.com/openclaw/beam/internal/adapter"
	"github.com/openclaw/beam/internal/clean"
	"github.com/openclaw/beam/internal/cleanstdio"
)

var version = "dev"

func main() {
	args := os.Args[1:]
	if slices.Equal(args, []string{"--version"}) {
		fmt.Println(version)
		return
	}
	if len(args) != 3 || args[0] != "clean" || args[1] != "--protocol" {
		fmt.Fprintln(os.Stderr, "usage: beam-clean clean --protocol 1")
		os.Exit(2)
	}
	if err := cleanstdio.Run(args[2], os.Stdin, os.Stdout, os.Stderr, clean.Options{}); err != nil {
		fmt.Fprintln(os.Stderr, "beam:", err)
		os.Exit(adapter.ExitCode(err))
	}
}
