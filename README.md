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
│  S3 / Git LFS / rsync / etc │
└─────────────────────────────┘
```

## Storage

Databases live under `~/.config/tt/`. The active database is selected automatically:

1. If the current directory is inside a git repo, uses `~/.config/tt/<repo-name>.db`
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

## Task Schema

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes | Short hash, generated on create |
| `title` | string | yes | One-line summary |
| `status` | enum | yes | See below |
| `parent_id` | string | no | ID of parent task |
| `description` | text | no | Longer body / notes |
| `priority` | int | no | 0 (critical) – 4 (backlog), default 2 |
| `created_by` | string | no | Free-form handle of creator (human or agent) |
| `assignee` | string | no | Free-form handle: `jrdn`, `claude/opus4.7`, `lmstudio/qwen3-coder` |
| `due_date` | date | no | YYYY-MM-DD |
| `created_at` | timestamp | yes | Set on create, immutable |
| `updated_at` | timestamp | yes | Updated on every write |
| `closed_at` | timestamp | no | Set when status moves to `done` or `cancelled` |

### Status Values

| Value | Meaning |
|---|---|
| `open` | Not started |
| `in_progress` | Actively being worked |
| `done` | Completed |
| `cancelled` | Explicitly abandoned |

`parent_id` enables subtask trees of arbitrary depth.

### Comments

Tasks have a comments log for appending progress notes, agent reasoning, or status updates without modifying the description.

| Field | Type | Description |
|---|---|---|
| `id` | string | Short hash |
| `task_id` | string | Parent task |
| `author` | string | Free-form handle, same format as `created_by` / `assignee` |
| `body` | text | Comment content |
| `created_at` | timestamp | Immutable |

```bash
tt comment <id> "tried approach X, hit rate limit — retrying with backoff"
tt show <id>   # renders comments chronologically below description
```

### Relations

Tasks can have typed relations to other tasks, stored as a separate edge table:

| Field | Type | Description |
|---|---|---|
| `from_id` | string | Source task |
| `to_id` | string | Target task |
| `type` | enum | `blocks`, `duplicates`, `related` |

Relations are directional. `blocks` means the source must be resolved before the target. `duplicates` means the source is a duplicate of the target. `related` is a weak bidirectional link with no implied ordering.

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

Each database has its own sync config at `~/.config/tt/config/<dbname>.toml`. For example, `~/.config/tt/myrepo.db` is configured at `~/.config/tt/config/myrepo.toml`:

```toml
backend = "s3"          # s3 | rsync | lfs

[s3]
bucket = "your-bucket"

[rsync]
target = "user@host:~/.config/tt/"

[lfs]
branch = "tasks"
```

Then sync with:

```bash
tt sync pull
tt sync push
```

### AWS S3

```toml
backend = "s3"

[s3]
bucket = "your-bucket"
```

Enable versioning on the bucket. If you overwrite with stale data you can recover a previous version.

For continuous background replication, [Litestream](https://litestream.io) streams SQLite's WAL to S3 automatically and supports point-in-time recovery:

```bash
litestream replicate ~/.config/tt/myrepo.db s3://your-bucket/myrepo.db
litestream restore -o ~/.config/tt/myrepo.db s3://your-bucket/myrepo.db
```

### rsync over SSH

```toml
backend = "rsync"

[rsync]
target = "user@host:~/.config/tt/"
```

Simple and dependency-free if you have a machine accessible from both devices.

### Git LFS (orphan branch)

```toml
backend = "lfs"

[lfs]
branch = "tasks"
```

Tasks live on an orphan branch in any existing repo (or a dedicated one), with LFS handling binary storage. Requires no new infrastructure if you're already on GitHub or GitLab.

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

LFS bandwidth on GitHub is 1GB/month free. At task-manager scale you will not hit this limit.

### Syncthing

Run Syncthing on both machines and add `~/.config/tt/` as a shared folder. Sync is continuous and automatic. Syncthing creates conflict files rather than silently overwriting, which makes accidental simultaneous writes recoverable.

Good option if you want set-and-forget sync without a cloud dependency. No git config needed — just point Syncthing at the directory.

### Dropbox / iCloud

Symlink `~/.config/tt/` into your Dropbox or iCloud Drive folder. Sync is handled automatically.

Note: iCloud has no official Linux client. Dropbox has a Linux client but limits support to ext4 filesystems. Neither is recommended if Linux is a primary machine.

## Conflict Behavior

No sync backend here provides true conflict resolution for simultaneous writes from two offline machines. The realistic failure scenario is: you write tasks on machine A while offline, write tasks on machine B while offline, then sync. One machine's changes will win depending on the backend.

In practice this is rare if you're switching between machines rather than using both simultaneously. S3 versioning and Litestream both provide recovery options if you do overwrite something you didn't mean to.

If true offline peer sync with conflict resolution is a hard requirement, reconsider this tool.
