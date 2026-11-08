---
root: false
targets: ["*"]
description: "Testing strategy: behaviour through public surfaces only"
globs: ["**/*_test.go", "internal/fakecoros/**", "testdata/**"]
---

# Testing

Test behaviour only, through public surfaces. Never test unexported functions or assert on implementation details.

Public surfaces:

- The binary: exit codes, stdout and stderr, environment variables
- The volumes: files in `/watch`, `/state/ledger.json`, `/tokens/token.json` and their modes
- Exported APIs of `internal/coros`, `internal/ledger`, `internal/daemon`, `internal/throttle`, `internal/logging`, `internal/fit`, `internal/fittitle` and `internal/fitscrub`
- The requests the fake COROS receives (`Requests`, `RequestsTo`), including logins and the region each request reached: request counts, regions and timing are what COROS observes, so they are behaviour
- The terminal: what `login` shows and echoes on a pseudo-terminal
- The `run` HTTP endpoints: `/healthz` and `/status`

Levels:

- **Acceptance** (`acceptance/`): `TestMain` builds the binary once. Each test gets a fake COROS server and `t.TempDir()` volumes and asserts only on public surfaces. Drive `run` with `world.startDaemon` and wait with `eventually`, never fixed sleeps. Drive interactive `login` with `world.startInTerminal` (`expect`, `send`, `interrupt`). Activities serve a valid FIT file by default; use `withContent` for anything else.
- **Time-controlled** (`internal/daemon`, `internal/throttle`): the schedule, the download delay and the 429 backoff run against a fake `Clock`; advance it by the exact duration asked for, never sleep. Read the clock's start time before starting the daemon, never after.
- **Fault injection** (`acceptance/`, `internal/coros`): `fakecoros.Server.InjectFaults` queues a 429, a 5xx, a truncated body or a stall for the next requests to an endpoint. Set `HTTP_TIMEOUT_TESTING` when a test needs a timeout, and simulate an elapsed backoff by rewriting `backoff.until` in the ledger.
- **Logs** (`acceptance/`, `internal/logging`): every log line is on stderr. Assert on substrings that survive both formats (field values such as a label id or an error), or set `LOG_FORMAT=json` and parse each line. The acceptance scan for the token and password is only a regression guard: it cannot fail while the connector never logs them, so test redaction itself on `internal/logging` with secrets logged on purpose. Simulate a non-writable volume with `chmod 0555` on its temp dir, and skip when running as root.
- **Contract** (`internal/coros`): `internal/fakecoros` builds every response from the recorded fixtures in `testdata/coros/` and rejects requests that do not match the real API's shape. When the fake needs a new behaviour, start from a recorded fixture, never from a hand-written response, unless the file is named `*.synthetic.json`.
- **Live contract** (`internal/coros/live_test.go`, build tag `live`): run locally with a personal account before merging a change to the client.
- **Fuzz** (`FuzzParse`, `FuzzScrub`, `FuzzWrite`): each target's assertions are its specification; keep them when you change the package.
- **FIT** (`internal/fit`, `internal/fittitle`, `acceptance/`): decode with `muktihari/fit` and checksums enforced, never `WithIgnoreChecksum()`. When an assertion is about bytes, compare the encoded field bytes too: the decoder drops invalid UTF-8, which hides a title cut mid-character.
- **Dreeve oracle** (`TestDreeveOracle`): builds Dreeve's `fit-tool` at the commit pinned in `testdata/dreeve/fit-tool.json` and fails on any warning, because `fit-tool` silently retries without checksums on a CRC mismatch. Renovate bumps the pin; never edit it by hand to make the test pass.
- **Container smoke** (`TestContainerSmoke`): `CONTAINER_SMOKE=1` builds the image; `CONTAINER_SMOKE_IMAGE` pulls a pushed one instead and fails unless its variant matches the host's architecture.

A new test must fail before the change that makes it pass. When a test passes immediately, break the code on purpose to confirm it can fail.
