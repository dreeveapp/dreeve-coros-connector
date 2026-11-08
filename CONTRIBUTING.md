# Contributing

## Prerequisites

- Go, at the version in `go.mod`
- Docker, for the container smoke test and the Dreeve oracle
- Node.js and npm, for actionlint and rulesync
- [`gh`](https://cli.github.com) with the [`gh actions-lock`](https://github.com/github/gh-actions-lock) extension, and [zizmor](https://docs.zizmor.sh/)

## Workflow

Work test-first, through public surfaces only: the binary, its volumes, its HTTP endpoints and the requests COROS receives. A pull request fills `.github/PULL_REQUEST_TEMPLATE.md`; its Proof section shows the change working in the running connector, because passing unit tests are not proof.

Never log in against the real COROS API to test a change: every login signs out Training Hub and every other connector on the account.

## Running the tests

[`.rulesync/rules/overview.md`](.rulesync/rules/overview.md#running-the-tests) lists every test level and its command. [`.rulesync/rules/testing.md`](.rulesync/rules/testing.md) describes what each level covers.

## Running the connector by hand

`tools/fakecoros` is a fake COROS server that replays the recorded fixtures. The [`run-app` skill](.rulesync/skills/run-app/SKILL.md) walks through it. These variables point the connector at it:

| Variable | Meaning |
| --- | --- |
| `COROS_API_HOSTS` | `1=<url>,2=<url>,3=<url>` region-to-host mapping, printed by the fake |
| `WATCH_DIR`, `STATE_DIR`, `TOKENS_DIR` | Volume paths outside a container |
| `POLL_INTERVAL_TESTING` | A Go duration replacing `POLL_INTERVAL`, without its minimum |
| `DOWNLOAD_DELAY_TESTING` | A Go duration replacing `DOWNLOAD_DELAY_SECONDS` and the login retry delay |
| `HTTP_TIMEOUT_TESTING` | A Go duration for the COROS request timeout (default `60s`) |

## Fixtures

Recorded COROS responses live in `testdata/coros/` and golden FIT files in `testdata/fit/`. Only someone with a COROS account can add them, and every one must be anonymised first. [`.rulesync/rules/fixtures.md`](.rulesync/rules/fixtures.md) explains how.

## CI and dependencies

CI runs the tests, golangci-lint, govulncheck, actionlint, zizmor, the actions lockfile check, the container smoke test on both architectures, and Dreeve's own `fit-tool` on delivered files. Every dependency is pinned to an exact version; after changing any `uses:`, run `gh actions-lock` and commit the lockfile with the workflow. [`.rulesync/rules/supply-chain.md`](.rulesync/rules/supply-chain.md) has the rules. Renovate keeps dependencies up to date.

## Agent guidance

`CLAUDE.md` and `.claude/` are generated. Edit `.rulesync/`, run `npm ci --ignore-scripts && npx rulesync generate`, and commit both.

## Releasing

Follow [RELEASING.md](RELEASING.md).
