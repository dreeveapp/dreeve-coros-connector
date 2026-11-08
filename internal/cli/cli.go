package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"

	"github.com/xballoy/dreeve-coros-connector/internal/logging"
)

const usage = `usage: dreeve-coros-connector <command>

commands:
  login       log in to COROS and save the token; prompts in a terminal, otherwise
              reads COROS_EMAIL and COROS_PASSWORD or their _FILE variants
  sync-once   deliver new COROS activities as FIT files to the watch folder, then exit
  run         sync every POLL_INTERVAL seconds and serve /healthz and /status on HTTP_ADDR
  healthcheck exit 0 when the running daemon reports healthy on /healthz
  version     print the version and exit
`

var version = "dev"

type Env func(string) string

type umask os.FileMode

func (u umask) apply(perm os.FileMode) os.FileMode { return perm &^ os.FileMode(u) }

type app struct {
	log     *slog.Logger
	secrets *logging.Secrets
	umask   umask
	prompt  *prompter
}

func Run(ctx context.Context, args []string, getenv Env, stdin io.Reader, stdout, stderr io.Writer) int {
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
	case "version":
		_, _ = fmt.Fprintln(stdout, version)
		return 0
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
	a.prompt = newPrompter(stdin, stderr)
	if err := run(ctx, getenv, a); err != nil {
		a.log.Error(args[0]+" failed", "error", err)
		return 1
	}
	return 0
}

func newApp(getenv Env, w io.Writer) (*app, error) {
	secrets := &logging.Secrets{}
	format, formatErr := logging.ParseFormat(getenv("LOG_FORMAT"))
	level, levelErr := logging.ParseLevel(getenv("LOG_LEVEL"))
	a := &app{log: logging.New(w, level, format, secrets), secrets: secrets}
	if err := errors.Join(formatErr, levelErr); err != nil {
		return a, err
	}
	var err error
	a.umask, err = parseUmask(getenv("UMASK"))
	return a, err
}

func parseUmask(raw string) (umask, error) {
	if raw == "" {
		return 0o022, nil
	}
	n, err := strconv.ParseUint(raw, 8, 32)
	if err != nil || n > 0o777 {
		return 0, fmt.Errorf("UMASK must be an octal permission mask such as 022, got %q", raw)
	}
	return umask(n), nil
}
