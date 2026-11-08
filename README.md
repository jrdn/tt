# tt

A simple task tracker for working with AI agents. Run it locally with zero setup, or self-host it when a team needs to share tasks.

## Why

Agents work better when they have somewhere to put the plan: what's next, what's blocked, what was tried, what's waiting on a human. `tt` is that place. It is small enough to run on a laptop and plain enough that an agent can drive it from the command line.

It is in the same space as [beads](https://github.com/steveyegge/beads), and it aims to keep the same idea with less machinery. The biggest difference is how it handles more than one machine:

- **Local:** tasks live in a single SQLite file. There is no server and no daemon. The CLI reads and writes the file directly.
- **Shared:** to use `tt` from several machines, or with a team, run one `tt server` backed by Postgres and point your repos at it. There is a single source of truth, so nothing needs merging.

You pick one of the two. `tt` has no peer-to-peer or git-based merging to configure, debug or recover from.

## What you get

- **Tasks that fit agent work:** subtasks, `blocks` / `duplicates` / `related` links, priorities, assignees, and a status lifecycle (`ready` → `in_progress` → `in_review` → `done`).
- **A progress log:** comments let an agent record what it did and what is left, so another agent or a human can pick the task up later.
- **Agent identity:** agents are assigned tasks under their own handle (`claude/opus4.7`). On a server they get their own scoped API keys, and every change is attributed.
- **Several ways in:** CLI, terminal UI and web UI. The TUI and web UI update live when an agent changes something.
- **Output for machines:** every command takes `--json`.

## Quick start

```bash
tt add "write the migration"          # create a task
tt ls                                 # list open tasks
tt update <id> --status=in_progress
tt comment <id> "schema done, tests next"
tt show <id>                          # detail, subtasks, relations, comments
tt done <id>
```

Inside a git repo, tasks go to `~/.config/tt/<repo>.db`, so each repo has its own list. Add `--json` to any command for scripts and agents. Run `tt --help` for the rest, and `tt tui` or `tt web` for the interactive views.

## Sharing across machines or a team

```bash
tt init --server https://tt.example.com --project myproj   # writes .tt.json; commit it
tt login
```

After that, every command works against the server project exactly as it does locally. The server handles logins, per-project permissions and API keys for agents. See [Server mode](docs/reference.md#server-mode) for setup and [Running a server](docs/reference.md#running-a-server) to host your own.

Prefer to stay local on a few machines? You can sync the SQLite file yourself with S3, rsync, Git LFS or Syncthing. This is manual and has no conflict resolution, so for anything more than one person switching machines, use a server. See [Sync](docs/reference.md#sync).

## More

- [docs/reference.md](docs/reference.md): storage, task schema, server mode, API keys, sync, observability, releases
- [AGENTS.md](AGENTS.md): build and test, commit conventions, and how agents should use `tt` in this repo
