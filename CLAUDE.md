# Dreeve COROS connector

Go service that downloads completed COROS activities as FIT files into Dreeve's watch folder. The design lives in issue #1; each vertical slice is a sub-issue.

## Layout

- `cmd/dreeve-coros-connector`: binary entry point
- `internal/cli`: subcommands (`login`, `sync-once`), configuration from environment variables, token store
- `internal/coros`: COROS API client
- `internal/ledger`: `/state/ledger.json` contract
- `internal/fakecoros`: fake COROS server for tests, replays `testdata/coros/`
- `acceptance`: black-box tests that build and drive the binary
- `tools/fakecoros`: dev-only fake COROS server for running the connector by hand (see the `run-app` skill); never shipped in the image

## Rules

- Write no comments in code, including test files. Names and tests carry the meaning; rationale goes in the PR description.
- Work test-first: write a failing test through a public surface, make it pass, refactor.
- Pin every dependency to an exact version: Go modules, npm devDependencies, container images by digest, GitHub Actions by full commit SHA with a `# vX.Y.Z` comment.
- Never log or persist the COROS password. Only `login` reads it.
- Every COROS login revokes every other token on the account. Never add code or tests that log in against the real API outside the live contract check.
- Agent guidance lives in `.rulesync/`. Edit it there, run `npm ci --ignore-scripts && npx rulesync generate`, and commit the generated files.

## Running the tests

| Level | Command |
| --- | --- |
| Unit, contract and acceptance | `go test ./...` |
| Race | `go test -race ./...` |
| Fuzz | `go test -run '^$' -fuzz FuzzParse -fuzztime 30s ./internal/ledger` |
| Container smoke | `CONTAINER_SMOKE=1 go test -count=1 -run TestContainerSmoke ./acceptance` |
| Live contract (local only, never CI) | `COROS_EMAIL=... COROS_PASSWORD=... go test -tags live -count=1 -run TestLiveContract ./internal/coros` |
| Lint | `golangci-lint run ./...` |

The live contract check logs in to the real COROS API, which signs out Training Hub and every running connector on that account.
