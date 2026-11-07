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
- Exported APIs of `internal/coros` and `internal/ledger`

Levels:

- **Acceptance** (`acceptance/`): `TestMain` builds the binary once. Each test gets a fake COROS server and `t.TempDir()` volumes, runs subcommands, and asserts only on public surfaces. Point the binary at the fake with `COROS_API_HOSTS` and at the volumes with `WATCH_DIR`, `STATE_DIR` and `TOKENS_DIR`.
- **Contract** (`internal/coros`): the client runs against `internal/fakecoros`, which builds every response from the recorded fixtures in `testdata/coros/` and rejects requests that do not match the real API's shape. When the fake needs a new behaviour, start from a recorded fixture, never from a hand-written response, unless the file is named `*.synthetic.json`.
- **Live contract** (`internal/coros/live_test.go`, build tag `live`): run locally with a personal account before merging a change to the client. Never run it in CI.
- **Fuzz**: `FuzzParse` in `internal/ledger` checks that loading a ledger never panics and never accepts bytes that are not a valid ledger, so a corrupt ledger can never be silently reset.
- **Container smoke** (`TestContainerSmoke`, gated by `CONTAINER_SMOKE=1`): builds the image and runs `login` and `sync-once` as an arbitrary non-root user with a read-only root filesystem.

A new test must fail before the change that makes it pass. When a test passes immediately, break the code on purpose to confirm the test can fail.
