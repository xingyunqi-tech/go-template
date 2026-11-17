// Command app is the single binary for the service's subcommands.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/app"
	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/pkg/logger"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

var version = "dev"

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2

	_healthcheckTimeout = 5 * time.Second
	_minPasswordLength  = 6
)

const usage = `Usage: app <command>

Commands:
  serve     run the HTTP API server
  worker    run the queue workers and periodic jobs
  migrate   apply the database migrations
  seed      insert reference data and seed accounts, then clear the permission cache
  permissions clear-cache
            delete the cached role permission map
  createsuperuser -email <email>
            create an active administrator; the password is read from the first line of stdin
  healthcheck [--ready]
            probe the local API liveness (or readiness) endpoint; exit 0 when it answers 200
  version   print the build version
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
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
	case "permissions":
		if fs.Arg(1) != "clear-cache" {
			fs.Usage()

			return exitUsage
		}

		return command(stderr, "permissions clear-cache", app.ClearPermissionCache)
	case "createsuperuser":
		return createSuperuser(fs.Args()[1:], stdin, stderr)
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

func commands() map[string]func(context.Context, *config.Config, *slog.Logger) error {
	return map[string]func(context.Context, *config.Config, *slog.Logger) error{
		"serve": app.Serve, "worker": app.Work, "migrate": app.Migrate, "seed": app.Seed,
	}
}

// createSuperuser reads -email and the password from the first line of stdin.
func createSuperuser(args []string, stdin io.Reader, stderr io.Writer) int {
	fs := flag.NewFlagSet("createsuperuser", flag.ContinueOnError)
	fs.SetOutput(stderr)
	email := fs.String("email", "", "administrator email")

	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	address := strings.ToLower(strings.TrimSpace(*email))
	if !validatorx.IsEmail(address) {
		_, _ = fmt.Fprintln(stderr, "createsuperuser: -email must be an email address")

		return exitUsage
	}

	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		_, _ = fmt.Fprintln(stderr, err)

		return exitError
	}

	pass := strings.TrimRight(line, "\r\n")
	if len(pass) < _minPasswordLength {
		_, _ = fmt.Fprintf(stderr, "createsuperuser: the password on stdin must have at least %d characters\n", _minPasswordLength)

		return exitUsage
	}

	return command(stderr, "createsuperuser", app.CreateSuperuser(address, pass))
}

// healthcheck GETs /<API_PREFIX>/v1/health/live (ready with --ready) on 127.0.0.1:<APP_PORT>.
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

	url := fmt.Sprintf("http://127.0.0.1:%d/%s/v1/health/%s", cfg.App.Port, cfg.App.APIPrefix, probe)

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

func command(stderr io.Writer, name string, run func(context.Context, *config.Config, *slog.Logger) error) int {
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

	log := obs.Logger(logger.New(stderr, cfg.Log.LevelOr(cfg.App.IsProduction()), cfg.Log.PrettyPrint.Or(!cfg.App.IsProduction()), app.LogAttrs))
	app.RouteLibraryLogs(log)

	if err := run(ctx, cfg, log); err != nil {
		log.ErrorContext(ctx, "command failed", "command", name, "error", err)

		return exitError
	}

	return exitOK
}
