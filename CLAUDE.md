# Project Instructions for AI Agents

## Build & Test

```bash
mise run build        # build ./tt binary
mise run test         # unit tests
mise run test:integ   # integration tests (real SQLite)
mise run test:all     # both
mise run lint         # go vet
mise run install      # install to $GOPATH/bin
```

Or directly:

```bash
go build -o tt ./cmd/tt
go test ./...
go test -tags integration ./...
```

## Architecture Overview

Local-first task manager. No HTTP server or daemon — the CLI reads/writes SQLite directly.

```
cmd/tt/main.go          entry point
internal/db/            SQLite open/migrate, per-repo DB selection
internal/task/          task model + CRUD (tasks, comments, relations)
internal/cmd/           cobra commands
```

Storage: `~/.config/tt/<repo-name>.db` when inside a git repo, `~/.config/tt/tasks.db` otherwise. Override with `TASKS_DB=/path/to/file.db`.

## Conventions & Patterns

- IDs are 4-char base32 short hashes. All lookups accept a prefix.
- Assignee is freeform: `jrdn`, `claude/opus4.7`, `lmstudio/qwen3-coder`.
- Integration tests use `//go:build integration` and open a temp DB via `db.OpenPath(t.TempDir() + "/test.db")`.

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

### Sync

`tt sync pull` / `tt sync push` are available but should only be run on explicit request — sync is not automatic.

<!-- end tt -->
