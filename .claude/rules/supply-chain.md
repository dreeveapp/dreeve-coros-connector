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

Every check is a job in `.github/workflows/ci.yml`; add a new one to the `needs` of `CI gate`, and give it a `timeout-minutes`. Run the workflow checks from the overview's test table before pushing a change to `.github/`.

Never set `zizmor-action`'s `version` input: an exact version fails once it is newer than the action, and the action's default is already hash-pinned.

`release.yml` is the only workflow with write permissions, each granted on the one job that needs it, and it uses no cache, so a poisoned cache cannot reach a published image.

## Renovate

`renovate.json` extends the shared `github>xballoy/renovate-config` preset; do not repeat its settings here. If a Renovate PR fails the `actions-lock` job, regenerate the lockfile on its branch with `gh actions-lock` and review the diff.
