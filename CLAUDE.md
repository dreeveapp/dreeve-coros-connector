# Dreeve COROS connector

Go service that downloads completed COROS activities as FIT files into Dreeve's watch folder. The design lives in issue #1; each vertical slice is a sub-issue.

## Layout

Package names say what each holds. What they don't say:

- `internal/ledger`: `/state/ledger.json` is a contract; it also stores the 429 backoff so it survives restarts
- `internal/atomicfile`: the only way the connector writes a file it keeps
- `internal/fit`: walks, validates and reseals raw FIT bytes; `fittitle` and `fitscrub` build on it so neither ever re-encodes a file
- `internal/fakecoros`: test fake that replays `testdata/coros/` and injects faults
- `tools/fakecoros`, `tools/fitscrub`, `internal/fitscrub`: dev-only, never shipped in the image

## Rules

- Write no comments in code, including test files. Names and tests carry the meaning; rationale goes in the PR description.
- Work test-first: write a test through a public surface, watch it fail, make it pass.
- Pin every dependency to an exact version: Go modules, npm devDependencies, container images by digest, GitHub Actions by full semver tag locked in `.github/workflows/actions.lock` (see the supply-chain rule).
- Never log or persist the COROS password. Only `login` reads it. Log through the app's `*slog.Logger` only, never `fmt` to stdout or stderr (the usage text, `version` and the `login` prompts are the only exceptions), and register any new secret with `Secrets.Add` as soon as it is read.
- Write every file the connector keeps (FIT files, the ledger, the token) only through `internal/atomicfile` with a mode passed through the app's `umask`, so `UMASK` applies to it. The boot write probe, created and removed at once, is the only exception.
- Every COROS login revokes every other token on the account, Training Hub included. Never add code or tests that log in against the real API outside the live contract check.
- Never add a `Co-Authored-By` trailer for an AI assistant to a commit message.
- Agent guidance lives in `.rulesync/`. Edit it there, run `npm ci --ignore-scripts && npx rulesync generate`, and commit the generated files.

## Running the tests

| Level | Command |
| --- | --- |
| Unit, contract and acceptance | `go test ./...` |
| Race | `go test -race ./...` |
| Fuzz | `go test -run '^$' -fuzz <FuzzParse\|FuzzScrub\|FuzzWrite> -fuzztime 30s ./internal/<ledger\|fitscrub\|fittitle>` |
| Dreeve oracle | `DREEVE_ORACLE=1 go test -count=1 -run TestDreeveOracle ./acceptance` |
| Container smoke | `CONTAINER_SMOKE=1 go test -count=1 -run TestContainerSmoke ./acceptance`; add `CONTAINER_SMOKE_IMAGE=ghcr.io/...@sha256:...` to test a pushed image |
| Live contract (local only, never CI) | `COROS_EMAIL=... COROS_PASSWORD=... go test -tags live -count=1 -run TestLiveContract ./internal/coros` |
| Lint | `golangci-lint run ./...` |
| Vulnerabilities | `go tool govulncheck ./...` |
| Workflows | `npx --no-install actionlint`, `zizmor .`, `gh actions-lock --verify` |

## Releasing

Follow `RELEASING.md`. The end-to-end release run pastes the compose example from the [COROS connector page](https://docs.dreeve.app/connectors/coros-connector/) unchanged, so when a change touches the image, volumes, `user:`, commands or configuration, update that page in `dreeveapp/dreeve` (`docs/src/content/docs/connectors/coros-connector.mdx`) to match.
