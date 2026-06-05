## tt Task Manager

This project uses **tt** for task tracking. Tasks live in a local SQLite database at `~/.config/tt/<repo>.db`.

### Key Commands

```bash
tt prime                                     # orientation: open/in-progress tasks and ready work
tt next                                      # claim and display your next ready task (exit 1 when done)
tt ls                                        # list open tasks
tt ls --status=ready                        # unblocked tasks ready to pick up
tt ls --status=in_progress                  # filter by status
tt add "title"                              # create a task
tt add "title" --assignee claude/opus4.7   # assign on create
tt show <id>                                # full detail: subtasks, relations, comments
tt done <id>                                # mark done
tt update <id> --status=in_progress         # update any field
tt update <id> --assignee <handle>          # reassign
tt claim <id>                               # set in_progress and assign to yourself
tt comment <id> "note"                      # append progress note without editing description
tt search "query"                           # fuzzy search across titles, descriptions, comments
tt relate <id> blocks <id>                  # link tasks (blocks | duplicates | related)
```

### Notes for Agents

- All commands accept `--json` for machine-readable output. `tt show <id> --json` returns task, subtasks, relations, and comments in one object.
- IDs are short 4-char base32 hashes. Prefix matching is supported — `tt show ab` works if unambiguous.
- `--assignee` accepts free-form handles: `jrdn`, `claude/opus4.7`, `lmstudio/qwen3-coder`.
- Use `tt comment` to log reasoning and progress without overwriting the description. Always pass `--author` to identify yourself (e.g. `--author claude/opus4.7`, `--author cursor/claude-sonnet`) — default falls back to OS username, which is not meaningful for agents.
- Priority: 0=critical, 1=high, 2=normal (default), 3=low.

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

### Work Loop

To work through your task queue autonomously:

1. Run `tt next --as <your-handle>` — atomically claims and displays your next ready task. Exits 1 when the queue is empty or all tasks are blocked.
2. Do the work. Comment progress with `tt comment <id> "..." --author <your-handle>`.
3. Run `tt update <id> --status=in_review` when done (not `done` — that's for human sign-off).
4. Repeat until `tt next` exits 1.

Configure your default handle once so `tt next` and `tt claim` don't need `--as`:

```bash
git config tt.handle claude/sonnet-4.6   # or: codex, opencode, lmstudio/qwen3-coder, etc.
```

### Sync

`tt sync pull` / `tt sync push` are available but should only be run on explicit request — sync is not automatic.

<!-- end tt -->
