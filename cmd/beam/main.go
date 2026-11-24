package main

import (
	"context"
	"os"
	"os/signal"
)

var version string

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	app := application()
	code := app.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	cancel()
	os.Exit(code)
}
