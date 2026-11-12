// Command app is the single binary for the service's subcommands.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/xingyunqi-tech/go-template/internal/app"
	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/pkg/logger"
)

var version = "dev"

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

const usage = `Usage: app <command>

Commands:
  serve     run the HTTP API server
  version   print the build version
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("app", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { _, _ = fmt.Fprint(stderr, usage) }

	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	switch fs.Arg(0) {
	case "serve":
		return serve(stderr)
	case "version":
		_, _ = fmt.Fprintln(stdout, version)

		return exitOK
	default:
		fs.Usage()

		return exitUsage
	}
}

func serve(stderr io.Writer) int {
	cfg, err := config.Load()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)

		return exitError
	}

	log := logger.New(stderr, cfg.Log.LevelOr(cfg.App.IsProduction()), cfg.Log.PrettyPrint.Or(!cfg.App.IsProduction()))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := app.Serve(ctx, cfg, log); err != nil {
		log.Error("serve failed", "error", err)

		return exitError
	}

	return exitOK
}
