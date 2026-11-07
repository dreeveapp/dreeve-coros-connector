---
root: false
targets: ["*"]
description: "How agents pick up and deliver GitHub issues"
globs: ["**/*"]
---

# Issue workflow

Issues carry exactly one state label:

- `needs-triage`: not yet reviewed
- `needs-info`: waiting for an answer from the reporter
- `ready-for-agent`: an agent may take it
- `ready-for-human`: needs a person, for example a real COROS account or a decision

An issue is ready when it has behavioural acceptance criteria, no open human decision, and no open blocker.

An agent:

1. Takes only `ready-for-agent` issues that are not blocked by an open issue.
2. Assigns itself before starting.
3. Opens a pull request that links the issue (`Closes #N`).
4. Stops and asks when it finds an open decision, instead of choosing on the maintainer's behalf.
