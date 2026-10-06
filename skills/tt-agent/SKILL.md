---
name: tt-agent
description: >
  Use this skill to work with tasks on the tt server (https://tt.j13g.party) as the `hermes` agent: look up, claim, work, comment on and update tickets. Trigger on any mention of tt, tickets, tasks, the task queue, "what should I work on", "pick up", "claim", "log this", or when a request is clearly about tracking or finding work.
---

# tt as an agent

`tt` is the team's task tracker. You talk to a shared server, not a local database. You are the agent **`hermes`**, acting on behalf of jrdn, and you have member access to the projects `life`, `puck` and `tt`.

The environment is already set up: `TT_SERVER` and `TT_API_KEY` are exported, so never run `tt login`. Check with `tt whoami`.

## Pick a project

Every command works inside one project. Set it per command, and do not guess:

```bash
TT_PROJECT=tt tt ls
```

If the request does not say which project, run `tt project ls` and ask. Use `--json` when you need to parse output.

## Everyday commands

```bash
tt ls                              # open and in-progress tasks
tt ls --status=ready               # also: backlog open ready in_progress in_review done cancelled
tt show <id>                       # full detail, subtasks, relations, comments (read this first)
tt search "words"                  # fuzzy search titles, descriptions, comments
tt add "title" -d "details"        # create (-p 0 critical .. 3 low, --parent <id>)
tt claim <id>                      # in_progress, assigned to you
tt comment <id> "note" --author hermes
tt update <id> --status=in_review
tt relate <id> blocks <id>         # blocks | duplicates | related
```

IDs are short; a unique prefix works.

## How to work a task

1. `tt show <id>` and read the comments before you start. Someone may have left context.
2. `tt claim <id>`.
3. Comment as you go, with `--author hermes`: what you did, what is left, any branch or commit, and what a fresh agent would need to know. If you stop, the last comment should let someone else resume.
4. When the work is finished, set `--status=in_review`. Do **not** mark a task `done` or `cancelled` unless jrdn asks: review is theirs.

## Judgment

- Create a task for work that takes more than a few minutes or could be interrupted. Do not create tasks for one-liners.
- Prefer a comment over editing a description. Comments keep the history.
- Do not reassign other people's tasks, and do not close or delete tasks you did not create.
- Do not run `tt sync`, `tt init` or `tt server`. They are for local databases and the server itself.
- Never print or paste `TT_API_KEY`.
