## Build & Test

```bash
mise run test:ui      # headless-Chrome tests for the web UI (skipped without Chrome)
```

## tt Task Manager

This project uses **tt** for task tracking. Tasks live in a local SQLite database at `~/.config/tt/<repo>.db`.

### Key Commands

```bash
tt ls                                        # list open tasks
tt ls --status=in_progress                  # filter by status
tt add "title"                              # create a task
tt add "title" --assignee claude/opus4.7   # assign on create
tt show <id>                                # full detail: subtasks, relations, comments
tt done <id>                                # mark done
tt update <id> --status=in_progress         # update any field
tt update <id> --assignee <handle>          # reassign
tt comment <id> "note"                      # append progress note without editing description
tt relate <id> blocks <id>                  # link tasks (blocks | duplicates | related)
```

### Notes for Agents

- All commands accept `--json` for machine-readable output. `tt show <id> --json` returns task, subtasks, relations, and comments in one object.
- IDs are short 4-char base32 hashes. Prefix matching is supported — `tt show ab` works if unambiguous.
- `--assignee` accepts free-form handles: `jrdn`, `claude/opus4.7`, `lmstudio/qwen3-coder`.
- Use `tt comment` to log reasoning and progress without overwriting the description. Always pass `--author` to identify yourself (e.g. `--author claude/opus4.7`, `--author cursor/claude-sonnet`) — default falls back to OS username, which is not meaningful for agents.
- Priority: 0=critical, 1=high, 2=normal (default), 3=low, 4=backlog.

### Resumability

Leave a trail so another agent can pick up your work if you crash or are interrupted. When working on a task, comment regularly with:

- What you've done so far
- What's still left
- The current branch name
- Commit hashes for any commits you've made
- Any gotchas, blockers, or context a fresh agent would need

```bash
tt c <id> "implemented auth middleware on branch feat/auth (a3f2c1b); still need to wire up the logout route and write tests" --author claude/opus4.7
```

Before ending a session on an in-progress task, always leave a comment summarizing the current state. `tt show <id>` is the first thing a resuming agent should run.

### Sync

`tt sync pull` / `tt sync push` are available but should only be run on explicit request — sync is not automatic.

<!-- end tt -->
