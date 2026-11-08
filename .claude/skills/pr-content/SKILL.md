---
name: pr-content
description: >-
  Creates or updates a pull request with a title and description focused on what
  changed and why, not implementation details, plus a Proof section with
  terminal output from the running connector. Use when opening a new PR, or
  updating an existing PR given a PR link/number, or when several changes must
  land as stacked PRs. In create mode it pushes the branch, opens the PR, and
  runs a bug-focused code review on it.
allowed-tools: >-
  Read, Glob, Grep, Skill, Agent, Bash(git log:*), Bash(git diff:*), Bash(git
  fetch:*), Bash(git push:*), Bash(gh pr:*), Bash(gh stack:*), Bash(gh repo
  view:*)
---
# PR content

## 1. Mode and base

- **Create**: no PR link or number given. Base is the branch the user named, otherwise `main`. Run `git fetch origin <base>` first.
- **Update**: a PR link or number given.
- **Stack**: several PRs that build on each other. Follow `references/stacks.md`.

## 2. Read the change

Create: `git log origin/<base>..HEAD --oneline` and `git diff origin/<base>...HEAD`. Update: `gh pr view <number> --json title,body,commits,files` and `gh pr diff <number>`. Read the full diff, so the "why" comes from the code and not only the commit messages.

## 3. Proof

The Proof section shows the change working in the running connector: command output, log lines, `/status` responses, before and after. Passing unit tests are not proof.

- Include only evidence that exists: checks run in this session, CI results, or material the user gave. Never claim something passed without having seen it.
- Without evidence, follow the `run-app` skill and use what it shows.
- If the change cannot be observed that way, ask the user for evidence instead of leaving the section empty or inventing it.

## 4. Write the content

**Title**: Conventional Commits, `<type>(<optional scope>): <description>`, `type` from the dominant change, `!` for a breaking change, imperative and lowercase, no trailing period, 72 characters at most.

**Body**: fill `.github/PULL_REQUEST_TEMPLATE.md`. Describe user impact and motivation; no file names, function names or code, except the commands in Proof. Add `Closes #N` for each GitHub issue referenced in the commits or branch name.

**Update mode**: keep what a person added to the existing body that is not a summary of the change (images, issue references, ticked checklists, notes for reviewers); replace only the what/why.

## 5. Publish

Pass the body through a quoted heredoc and wrap the title in single quotes (write `'` as `'\''`).

```bash
git push -u origin HEAD
gh pr create --base <base> --title '<title>' --body-file - <<'EOF'
<body>
EOF
```

Open the PR ready for review, never as a draft. Update with `gh pr edit <number> --title '<title>' --body-file - <<'EOF'`.

If the push is rejected because history was rewritten, retry with `git push --force-with-lease --force-if-includes -u origin HEAD`. If that is rejected too, someone else pushed: stop and report it. Never use `--force` or `-f`.

## 6. Review

In create and stack mode, run a bug-focused review of each new PR and report the findings in the terminal, without posting comments on GitHub: call the Skill tool with `code-review:code-review` and do not pass `--comment`.
