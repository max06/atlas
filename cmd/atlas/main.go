// Command atlas drives ATLAS consumer repositories: render, inspect, discover
// and review. See `atlas --help`.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/max06/atlas/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Execute(ctx)
	stop()
	os.Exit(code)
}
