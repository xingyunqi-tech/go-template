// Command app is the single binary for the service's subcommands.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/app"
	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/pkg/logger"
)

var version = "dev"

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2

	_healthcheckTimeout = 5 * time.Second
)

const usage = `Usage: app <command>

Commands:
  serve     run the HTTP API server
  worker    run the queue workers and periodic jobs
  migrate [up|down]
            create DATABASE_SCHEMA and apply the migrations, or roll them all back
  seed      insert the seed accounts
  healthcheck [--ready]
            probe the local API liveness (or readiness) endpoint; exit 0 when it answers 200
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

	if run, ok := commands()[fs.Arg(0)]; ok {
		return command(stderr, fs.Arg(0), run)
	}

	switch fs.Arg(0) {
	case "migrate":
		switch fs.Arg(1) {
		case "", "up":
			return command(stderr, "migrate", app.Migrate(false))
		case "down":
			return command(stderr, "migrate down", app.Migrate(true))
		default:
			fs.Usage()

			return exitUsage
		}
	case "healthcheck":
		return healthcheck(fs.Args()[1:], stderr)
	case "version":
		_, _ = fmt.Fprintln(stdout, version)

		return exitOK
	default:
		fs.Usage()

		return exitUsage
	}
}

type runFunc = func(context.Context, *config.Config, *slog.Logger, *app.Observability) error

func commands() map[string]runFunc {
	return map[string]runFunc{"serve": app.Serve, "worker": app.Work, "seed": app.Seed}
}

// healthcheck GETs /health/live (/health/ready with --ready) on 127.0.0.1:<APP_PORT>.
func healthcheck(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ready := fs.Bool("ready", false, "probe readiness instead of liveness")

	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	cfg, err := config.Load()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)

		return exitError
	}

	probe := "live"
	if *ready {
		probe = "ready"
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/health/%s", cfg.App.Port, probe)

	ctx, cancel := context.WithTimeout(context.Background(), _healthcheckTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)

		return exitError
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)

		return exitError
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		_, _ = fmt.Fprintf(stderr, "%s: %d\n", url, res.StatusCode)

		return exitError
	}

	return exitOK
}

func command(stderr io.Writer, name string, run runFunc) int {
	cfg, err := config.Load()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)

		return exitError
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	obs, err := app.SetupObservability(ctx, cfg, version)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)

		return exitError
	}

	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.App.ShutdownTimeout)
		defer cancel()

		if err := obs.Shutdown(shutdownCtx); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
		}
	}()

	log := obs.Logger(logger.New(stderr, logger.Options{
		Level: cfg.Log.Level, Text: cfg.Log.Format == "text", Service: cfg.Telemetry.ServiceName, Env: cfg.App.Env,
	}, app.LogAttrs))
	app.RouteLibraryLogs(log)

	if err := run(ctx, cfg, log, obs); err != nil {
		log.ErrorContext(ctx, "command failed", "command", name, "error", err)

		return exitError
	}

	return exitOK
}
