package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/openclaw/beam/internal/cli"
)

var version = "dev"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	app := cli.App{Version: version}
	code := app.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	cancel()
	os.Exit(code)
}
