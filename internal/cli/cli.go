package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"

	"github.com/xballoy/dreeve-coros-connector/internal/logging"
)

const usage = `usage: dreeve-coros-connector <command>

commands:
  login       log in to COROS with COROS_EMAIL and COROS_PASSWORD and save the token
  sync-once   deliver new COROS activities as FIT files to the watch folder, then exit
  run         sync every POLL_INTERVAL seconds and serve /healthz and /status on HTTP_ADDR
  healthcheck exit 0 when the running daemon reports healthy on /healthz
`

type Env func(string) string

type app struct {
	log     *slog.Logger
	secrets *logging.Secrets
	umask   os.FileMode
}

func Run(ctx context.Context, args []string, getenv Env, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
	var run func(context.Context, Env, *app) error
	switch args[0] {
	case "login":
		run = login
	case "sync-once":
		run = syncOnce
	case "run":
		run = runDaemon
	case "healthcheck":
		run = healthcheck
	case "help", "-h", "--help":
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	a, err := newApp(getenv, stderr)
	if err != nil {
		a.log.Error(args[0]+" failed", "error", err)
		return 1
	}
	if err := run(ctx, getenv, a); err != nil {
		a.log.Error(args[0]+" failed", "error", err)
		return 1
	}
	return 0
}

func newApp(getenv Env, w io.Writer) (*app, error) {
	secrets := &logging.Secrets{}
	a := &app{log: logging.New(w, slog.LevelInfo, logging.Text, secrets), secrets: secrets}
	format, err := logging.ParseFormat(getenv("LOG_FORMAT"))
	if err != nil {
		return a, err
	}
	a.log = logging.New(w, slog.LevelInfo, format, secrets)
	level, err := logging.ParseLevel(getenv("LOG_LEVEL"))
	if err != nil {
		return a, err
	}
	a.log = logging.New(w, level, format, secrets)
	a.umask, err = parseUmask(getenv("UMASK"))
	return a, err
}

func parseUmask(raw string) (os.FileMode, error) {
	if raw == "" {
		return 0o022, nil
	}
	n, err := strconv.ParseUint(raw, 8, 32)
	if err != nil || n > 0o777 {
		return 0, fmt.Errorf("UMASK must be an octal permission mask such as 022, got %q", raw)
	}
	return os.FileMode(n), nil
}
