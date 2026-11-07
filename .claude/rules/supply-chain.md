---
paths:
  - .github/**
  - renovate.json
  - go.mod
  - go.sum
  - package.json
  - package-lock.json
  - Dockerfile
---
# Supply chain

## GitHub Actions

- Reference every action by a full semver release tag (`actions/checkout@v7.0.1`), never a branch, a partial version (`@v7`) or an inline SHA. `.github/workflows/actions.lock` pins each tag, and every action those actions use, to a verified commit.
- Never edit `actions.lock` by hand. After adding a workflow or changing any `uses:`, run `gh actions-lock` from the repository root and commit the workflow and the lockfile together. Check the diff: a changed `commit` is new code running in CI, and a changed `owner_id` or `repo_id` means the repository behind the name changed.
- Never pass `--accept-moved`. A pin whose commit left upstream history needs a person to review the upstream change.
- Grant permissions per job, starting from `permissions: {}` at the workflow level, and check out with `persist-credentials: false`.
- Never interpolate `${{ }}` into a `run:` script; pass the value through `env:` and quote the variable.
- Container images are not covered by the lockfile: pin them by digest.

## CI checks

Each check is a job in `.github/workflows/ci.yml` and a dependency of the `CI gate` job. Run them locally before pushing a change to `.github/`, Go modules or npm tooling:

| Check | Command |
| --- | --- |
| Lockfile matches the workflows and verifies upstream | `gh actions-lock --verify` |
| Workflow correctness and script injection | `npx --no-install actionlint` |
| Workflow security | `zizmor .` |
| Reachable Go vulnerabilities | `go tool govulncheck ./...` |

Add a new job to the `needs` of `CI gate`.

## Renovate

`renovate.json` updates Go modules (including the `go` directive and `go tool` dependencies), the Dockerfile base images, the actions and the tool versions in their `with:` inputs, and the npm tooling. For an action, Renovate rewrites the tag and regenerates `actions.lock` with the `gh-actions-lock` version set in `constraints.ghActionsLock`; a custom manager keeps that version and the one CI installs in step.
