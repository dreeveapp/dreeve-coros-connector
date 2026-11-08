# Stacked PRs

Use `gh stack` for PRs that build on each other. Never chain them by hand with `gh pr create --base <another-pr-branch>`, and never rebase the layers with `git rebase --onto` and a force push each: GitHub then shows unrelated PRs and nothing cascades.

Build the stack bottom (closest to the default branch) to top:

```bash
gh stack init <first-branch>          # from the default branch; adopts an existing branch
# commit the first layer
gh stack add <next-branch>            # starts the next layer on top of the current one
# commit, repeat for each layer
gh stack submit --auto --open         # pushes every layer, opens a PR per layer with the right base, links the stack
```

`gh stack init <b1> <b2> <b3>` adopts branches that already exist, in that order. `submit --auto` gives each PR a generated title, so write each PR's title and body afterwards with `gh pr edit` as in the skill's "Write the content" step, bottom to top, each with its own Proof section. Each PR describes only its own layer.

When PRs were already opened by hand on chained branches, link them instead of recreating them: `gh stack link <bottom-pr> <next-pr> ...`.

Keep the stack in sync with `gh stack`, not by hand:

- After the default branch moves: `gh stack sync`. It fetches, cascade-rebases every layer and pushes with `--force-with-lease`.
- On a rebase conflict: `gh stack rebase`, resolve, then `gh stack rebase --continue` (or `--abort`).
- After amending a lower layer: `gh stack rebase --upstack`, then `gh stack push`.

Run the review step on every PR of the stack.
