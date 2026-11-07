---
paths:
  - '**/*_test.go'
  - internal/fakecoros/**
  - testdata/**
---
# Testing

Test behaviour only, through public surfaces. Never test unexported functions or assert on implementation details.

Public surfaces:

- The binary: exit codes, stdout and stderr, environment variables
- The volumes: files in `/watch`, `/state/ledger.json`, `/tokens/token.json` and their modes
- Exported APIs of `internal/coros`, `internal/ledger`, `internal/daemon`, `internal/throttle`, `internal/logging` and `internal/fitscrub`
- The requests the fake COROS receives (`Requests`, `RequestsTo`), including logins and the region each request reached: request counts, regions and timing are what COROS observes, so they are behaviour
- The terminal: what `login` shows and echoes on a pseudo-terminal
- The `run` HTTP endpoints: `/healthz` and `/status`

Levels:

- **Acceptance** (`acceptance/`): `TestMain` builds the binary once. Each test gets a fake COROS server and `t.TempDir()` volumes, runs subcommands, and asserts only on public surfaces. Point the binary at the fake with `COROS_API_HOSTS` and at the volumes with `WATCH_DIR`, `STATE_DIR` and `TOKENS_DIR`. Drive `run` with `world.startDaemon`, which sets `POLL_INTERVAL_TESTING` to a short duration and `HTTP_ADDR` to a free port, and wait on observable outcomes with `eventually`, never fixed sleeps. Drive interactive `login` with `world.startInTerminal`, which attaches the binary to a pseudo-terminal; `expect` waits for text on it, `send` types keys, and `interrupt` sends SIGINT as Ctrl-C would. The binary gets no controlling terminal, so the pseudo-terminal stays usable after it exits and a test can check that echo was restored.
- **Time-controlled** (`internal/daemon`, `internal/throttle`): the schedule, the download delay and the 429 backoff run against a fake `Clock`; tests advance it by the exact duration asked for, so interval, jitter, delay and backoff bounds are asserted without real sleeps.
- **Fault injection** (`acceptance/`, `internal/coros`): `fakecoros.Server.InjectFaults` queues a 429, a 5xx, a truncated body or a stall for the next requests to an endpoint. Acceptance tests set `DOWNLOAD_DELAY_TESTING` to a short duration by default and `HTTP_TIMEOUT_TESTING` when they need a timeout, and simulate an elapsed backoff by rewriting `backoff.until` in the ledger.
- **Logs** (`acceptance/`, `internal/logging`): every log line is on stderr. Assert on substrings that survive both formats (field values such as a label id or an error), or set `LOG_FORMAT=json` and parse each line. The acceptance scan for the token and password in the binary's output is a regression guard: it cannot fail while the connector never logs them, so redaction itself is tested on `internal/logging` with secrets logged on purpose. Simulate a non-writable volume with `chmod 0555` on its temp dir, and skip when running as root.
- **Race** (`go test -race ./...`): the `run` loop and the HTTP handler share the daemon's status; `internal/daemon` reads `/status` concurrently with running cycles.
- **Contract** (`internal/coros`): the client runs against `internal/fakecoros`, which builds every response from the recorded fixtures in `testdata/coros/` and rejects requests that do not match the real API's shape. When the fake needs a new behaviour, start from a recorded fixture, never from a hand-written response, unless the file is named `*.synthetic.json`.
- **Live contract** (`internal/coros/live_test.go`, build tag `live`): run locally with a personal account before merging a change to the client. Never run it in CI.
- **Fuzz**: `FuzzParse` in `internal/ledger` checks that loading a ledger never panics and never accepts bytes that are not a valid ledger, so a corrupt ledger can never be silently reset. `FuzzScrub` in `internal/fitscrub` checks that a scrubbed file always keeps its size and decodes with checksums enforced.
- **Container smoke** (`TestContainerSmoke`, gated by `CONTAINER_SMOKE=1`): builds the image and runs `login` and `sync-once` as an arbitrary non-root user with a read-only root filesystem, then runs `run` detached and checks that Docker's `HEALTHCHECK` turns unhealthy on revocation and healthy again after `login`.

A new test must fail before the change that makes it pass. When a test passes immediately, break the code on purpose to confirm the test can fail.
