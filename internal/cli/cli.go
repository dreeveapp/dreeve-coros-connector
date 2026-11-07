package cli

import (
	"context"
	"fmt"
	"io"
)

const usage = `usage: dreeve-coros-connector <command>

commands:
  login       log in to COROS with COROS_EMAIL and COROS_PASSWORD and save the token
  sync-once   deliver new COROS activities as FIT files to the watch folder, then exit
  run         sync every POLL_INTERVAL seconds and serve /healthz and /status on HTTP_ADDR
  healthcheck exit 0 when the running daemon reports healthy on /healthz
`

type Env func(string) string

func Run(ctx context.Context, args []string, getenv Env, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
	var run func(context.Context, Env, io.Writer, io.Writer) error
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
	if err := run(ctx, getenv, stdout, stderr); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", args[0], err)
		return 1
	}
	return 0
}
