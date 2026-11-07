---
targets:
  - '*'
name: pr-content
description: >-
  Creates or updates a pull request with a title and description focused on
  what changed and why, not implementation details, plus a Proof section with
  screenshots, a video or output from the running app attached through gh. Use when
  opening a new PR, or updating an existing PR given a PR link/number. In
  create mode it pushes the branch, opens the PR, and runs a bug-focused code
  review on it.
claudecode:
  allowed-tools: Read, Glob, Grep, Skill, Agent, Bash(git log:*), Bash(git diff:*), Bash(git fetch:*), Bash(git push:*), Bash(gh pr:*), Bash(gh stack:*), Bash(gh repo view:*)
---

# PR Content Generation

Generate or update pull request content that focuses on **what** changed and **why**, without technical implementation details.

## Instructions

### 1. Determine Mode and Base

- **Create mode**: No PR link/number provided → generate content for new PR
- **Update mode**: PR link or number provided → update existing PR description
- **Stack mode**: the work is several PRs that build on each other, because the user asks for a stack or for issues to land as stacked PRs, or because one PR needs another's branch. Create and maintain the stack with `gh stack` (see "Stacks" in step 6). Never chain the PRs by hand with `gh pr create --base <another-pr-branch>`, and never rebase the layers with `git rebase --onto` and a force push each: GitHub then shows unrelated PRs and nothing cascades.

In create mode, determine the base branch:
- If the user named a base branch, use it.
- Otherwise use the repo's default branch: `gh repo view --json defaultBranchRef -q .defaultBranchRef.name`

Then refresh it so the comparison is not stale:
```bash
git fetch origin <base>
```

### 2. Check for PR Template

GitHub looks for templates in the repo root, `docs/`, and `.github/`, with case-insensitive file names. Search for:
1. `PULL_REQUEST_TEMPLATE.md` in `.github/`, the repo root, or `docs/`
2. A `PULL_REQUEST_TEMPLATE/` directory in any of those locations, containing several templates

If there is a single template, use its structure for the PR body. If there is a directory of templates, pick the one that best fits the change (e.g. bug fix vs feature); if none clearly fits, use the default or first one.

### 3. Analyze Changes

**For create mode:**
```bash
git log origin/<base>..HEAD --oneline
git diff origin/<base>...HEAD
```
Read the full diff, not only the stat, so the "why" comes from the code and not just commit messages. For a very large diff, start with `--stat` and then read the significant files.

**For update mode:**
```bash
gh pr view <number> --json title,body,commits,files
gh pr diff <number>
```

### 4. Find Linked Issues

Read `docs/agents/issue-tracker.md` if it exists to learn where issues are tracked.

- **Local markdown tracker** (issues under `.scratch/`): do not add issue references. They point to nothing on the remote.
- **Remote tracker** (GitHub Issues, GitLab, Linear, Jira, ...): collect issue identifiers referenced in the commit messages and branch name (e.g. `#123`, `ENG-42`, `PROJ-7`). Add a closing reference for each in the body:
  - GitHub / GitLab / Linear: `Closes <id>`
  - Jira: the issue key on its own line (Jira links PRs by key; it does not close on merge)
- If the file is missing, apply the same rules based on what the identifiers look like, and skip anything that is a `.scratch/` path.

### 5. Generate Content

**Title**: [Conventional Commits](https://www.conventionalcommits.org/) format: `<type>(<optional scope>): <description>`
- `type` is one of `feat`, `fix`, `refactor`, `perf`, `docs`, `test`, `build`, `ci`, `chore`, `revert`, chosen from the dominant change in the PR
- Add `!` after the type/scope for a breaking change (e.g. `feat(api)!: remove v1 endpoints`)
- Scope only when one area is clearly affected
- Description in imperative mood, lowercase, no trailing period
- 50 characters or less when possible, 72 at most

**Body**:
- If template exists: fill in template sections
- If no template: write a brief paragraph explaining what changed and why
- Add a **Proof** section (or fill the template's testing/verification section) showing evidence that the change works in practice: observed behavior of the running app, command or API output, screenshots or recordings for UI changes, before/after behavior. Do not mention adding or passing unit tests; that is not evidence that the change works.
- Add the issue references from step 4

**Proof rules**:
- Only include evidence that actually exists: checks run in this session, CI results, or material the user provided. Never claim a test passed or a check was done without having seen it.
- If there is no evidence and the PR branch is checked out locally, follow the `run-app` skill to launch the app and observe the change working. Use what it shows (output, screenshots, observed behavior) as the proof.
- If the change cannot be observed that way (nothing runnable, needs credentials or external services, branch not checked out) or the run fails, ask the user for evidence before creating or updating the PR rather than leaving the section empty or inventing it.

**Screenshots or video**:
- Use screenshots when the proof is a state: a value, a layout, an empty or error screen, a before/after pair. They are faster to review, can be compared side by side and stay readable in the PR timeline. This is the default.
- Use a video when the proof is a sequence the reviewer would otherwise have to imagine between two frames: a failed save followed by a successful retry, browser Back restoring the previous screen, a multi-step form, a transition or anything driven by timers. One video per flow, 15 to 40 seconds.
- Do not use a video for refactors, copy changes or a single static screen. Alongside a video, add at most one screenshot of the end state as the inline image reviewers see first, not a screenshot per step.

Record screenshots and videos with the `run-app` skill.

**Content guidelines**:
- Focus on the user/business impact
- Explain the motivation for the change
- Do NOT include: file names, function names, technical implementation details, code snippets
- Exception: the Proof section may name the commands that were run to observe the behavior

**Update mode**: keep content a person added to the existing body that is not a summary of the change: screenshots, images, issue references, ticked checklists, notes for reviewers. Replace only the what/why content.

### 6. Execute

Pass the body through a quoted heredoc so backticks and `$` are not interpreted by the shell, or, when attaching media, through a body file (see below). Wrap the title in single quotes (write a literal `'` as `'\''`).

**Create:**
```bash
git push -u origin HEAD
gh pr create --base <base> --title '<title>' --body-file - <<'EOF'
<body>
EOF
```
Open the PR ready for review, never as a draft.

If the push is rejected because local history was rewritten (rebase, amend), retry with:
```bash
git push --force-with-lease --force-if-includes -u origin HEAD
```
If that is rejected too, someone else pushed to the branch: stop and report it. Never use `--force` or `-f`.

**Stacks:**

Build the stack with `gh stack`, bottom (closest to the default branch) to top:
```bash
gh stack init <first-branch>          # from the default branch; adopts an existing branch
# commit the first layer
gh stack add <next-branch>            # starts the next layer on top of the current one
# commit, repeat for each layer
gh stack submit --auto --open         # pushes every layer, opens a PR per layer with the right base, links the stack
```
`gh stack init <b1> <b2> <b3>` adopts branches that already exist, in that order. `submit --auto` gives each PR a generated title, so write each PR's title and body afterwards with `gh pr edit` as in step 5, bottom to top, with its own Proof section and attachments. Each PR describes only its own layer.

When PRs were already opened by hand on chained branches, link them instead of recreating them: `gh stack link <bottom-pr> <next-pr> ...`.

Keep the stack in sync with `gh stack`, not by hand:
- After the default branch moves: `gh stack sync`. It fetches, cascade-rebases every layer and pushes with `--force-with-lease`.
- On a rebase conflict: `gh stack rebase`, resolve, then `gh stack rebase --continue` (or `--abort`).
- After amending a lower layer: `gh stack rebase --upstack`, then `gh stack push`.

Run the review in step 7 on every PR of the stack.

**Update:**
```bash
gh pr edit <number> --title '<title>' --body-file - <<'EOF'
<body>
EOF
```

**Attaching proof images or videos:**

`gh` (2.103+) uploads media itself with `--attach` on `pr create`, `pr edit`, `pr comment`, `issue create` and `issue comment`. Do not push screenshots to a branch, commit them, or host them elsewhere.

- Save the screenshots or recordings in one directory (the session scratchpad), write the body to a file in that same directory, and reference each one inline where it belongs: `![What the image shows](./name.png)`.
- Pass the body with `--body-file <dir>/body.md` (not stdin) and each file with `--attach <dir>/name.png`. `gh` uploads it and rewrites the matching `./name.png` reference to the hosted URL. Relative paths in a body file resolve from the file's directory, then the working directory.
- An attachment the body does not reference is appended to the end of the body. Alt text written in the body wins; otherwise add it after `#`: `--attach '<dir>/name.png#Alt text'`. Videos render as a player and take no alt text. Up to 50 files per command.
- If some uploads fail, the PR is still created or updated with the ones that succeeded, and the command exits non-zero. Check the result with `gh pr view <number> --json body --jq .body | grep -o '!\[[^]]*\]([^)]*)'`: every reference should point at `https://github.com/user-attachments/assets/...`, and none should still say `./`.

```bash
gh pr edit <number> --title '<title>' --body-file "$DIR/body.md" \
  --attach "$DIR/1-before.png" --attach "$DIR/2-after.png"
```

**Check every image and video before attaching it.** A blank, half-rendered or wrong frame is not proof.
- Open each screenshot and look at it.
- A video cannot be opened directly: extract its frames and look at every one, as the `run-app` skill describes. Do not attach the frames; they are only for checking.

**Size limits**: images up to 10 MB; videos up to 10 MB on free plans and 100 MB on paid plans. Check with `ffprobe -v error -show_entries format=duration,size -of compact "$DIR/flow.webm"`. If a video is too large, cut the flow first (fewer chapters, shorter pauses), then re-encode it smaller:

```bash
ffmpeg -v error -y -i "$DIR/flow.webm" -vf "scale=-2:600,fps=15" -c:v libvpx-vp9 -crf 40 -b:v 0 -an "$DIR/flow-small.webm"
```

Lower the height (`600`) or raise `-crf` (up to about 50) for a smaller file, then extract and check the frames of the re-encoded file again (`VIDEO=flow-small.webm` in the `run-app` command), because text in the app must still be legible. Attach the re-encoded file, not the original. A video can't take alt text and should not be referenced inline; let `gh` append it to the end of the body, and describe in the Proof section what it shows.

### 7. Review

**Create and stack mode:** after a PR is created, run a bug-focused review of the new PR and report the findings in the terminal, without posting comments on GitHub. In Claude Code, call the Skill tool with `code-review:code-review` and do not pass `--comment`.
