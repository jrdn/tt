# tt

A lightweight local-first task manager. Tasks live in a SQLite database; `tt` reads and writes it directly — no server, no daemon.

## Design

Tasks are stored in a SQLite file under `~/.config/tt/`. The CLI talks to it directly. Sync is an explicit, separate concern: pull before you start, push when you're done.

## Architecture

```
┌─────────────────────────────┐
│        tt (Go CLI)          │
└──────────────┬──────────────┘
               │ direct read/write
┌──────────────▼──────────────┐
│  ~/.config/tt/<repo>.db     │
│  (SQLite)                   │
└──────────────┬──────────────┘
               │ sync (your choice)
┌──────────────▼──────────────┐
│       sync backend          │
│  S3 / Git LFS / directory   │
└─────────────────────────────┘
```

## Storage

Databases live under `~/.config/tt/`. The active database is selected automatically:

1. If the current directory is inside a git repo, uses `~/.config/tt/<repo-name>.db`
   - When working inside a linked worktree, uses the primary worktree repo name
   - When working inside a nested git repo, uses the enclosing parent repo name
2. Otherwise falls back to `~/.config/tt/tasks.db`

If two unrelated repos share the same name, `tt` errors on startup with a message explaining the conflict. Override the DB name for a repo via git config:

```bash
git config tt.db-name my-unique-name
```

This writes to `.git/config` (local to that repo) and resolves the collision.

Override the active database entirely with `TASKS_DB`:

```bash
TASKS_DB=/path/to/tasks.db tt ls
```

## Status Values

| Value | Meaning |
|---|---|
| `backlog` | Parked, not yet scheduled |
| `open` | Not started |
| `ready` | Unblocked, ready to pick up |
| `in_progress` | Actively being worked |
| `in_review` | Waiting on review |
| `done` | Completed |
| `cancelled` | Explicitly abandoned |

## Comments

Tasks have a comments log for appending progress notes, agent reasoning, or status updates without modifying the description.

```bash
tt comment <id> "tried approach X, hit rate limit — retrying with backoff"
tt show <id>   # renders comments chronologically below description
```

## Relations

Tasks can have typed relations to other tasks:

- `blocks` — source must be resolved before the target
- `duplicates` — source is a duplicate of the target
- `related` — weak bidirectional link with no implied ordering

```bash
tt relate <id> blocks <id>
tt relate <id> duplicates <id>
tt relate <id> related <id>
```

`tt show <id>` surfaces all relations in both directions (e.g. "blocked by" is the inverse of "blocks").

## Usage

```bash
tt add "do thing"                   # create a task
tt add "subtask" --parent <id>      # create a subtask
tt ls                               # list tasks (ID, status, title)
tt ls --status=open                 # filter by status
tt ls --parent=<id>                 # show subtasks of a task
tt show <id>                        # full detail view
tt done <id>                        # mark done
tt update <id> --status=in_progress # update any field
```

## Sync

Sync is intentionally not automatic. Pull before you start working, push when you're done.

```bash
tt sync pull
tt sync push
```

Sync config is stored in `~/.config/tt/config.json` and managed with `tt sync configure`.

### Local directory

```bash
tt sync configure --type directory --path /path/to/shared/dir
```

### AWS S3

```bash
tt sync configure --type s3 --bucket my-bucket --region us-east-1
```

Enable versioning on the bucket so you can recover from accidental overwrites.

### Git LFS

```bash
tt sync configure --type git_lfs --branch tasks
```

Tasks live on an orphan branch in any existing repo, with LFS handling binary storage. Requires no new infrastructure if you're already on GitHub or GitLab.

```bash
# one-time setup
git lfs install
git checkout --orphan tasks
git rm -rf .
echo "*.db filter=lfs diff=lfs merge=lfs -text" > .gitattributes
git add .gitattributes
git commit -m "init tasks branch"
git push origin tasks
```
