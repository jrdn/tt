# tt

A lightweight local-first task manager. By default tasks live in a SQLite database that `tt` reads and writes directly — no server, no daemon. Teams can instead point a repo at a shared `tt server` backed by Postgres, with logins, per-project permissions and API keys for agents.

## Design

Tasks are stored in a SQLite file under `~/.config/tt/`. The CLI talks to it directly. Sync is an explicit, separate concern: pull before you start, push when you're done.

For shared use, a repo can opt into [server mode](#server-mode): the same commands then work against a project on a `tt server`.

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

In server mode the CLI, TUI and web UI talk HTTP to `tt server` instead:

```
tt CLI / TUI / web UI ──HTTPS + JWT──▶ tt server ──▶ Postgres
                                        (one schema per project)
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

## Server mode

A repo uses a tt server when it has a `.tt.json` at its root. Commit it so teammates share the setting:

```bash
tt init --server https://tt.example.com --project myproj   # writes .tt.json
tt login                                                   # once per machine
```

`tt login` opens your browser; sign in with GitHub if asked and click Approve. The browser hands a one-time code back to the CLI on `127.0.0.1`, which redeems it with a secret only it holds (PKCE), so a forwarded login link is useless to anyone else. On a machine without a browser (e.g. over SSH), use `tt login --device`: open the URL it prints on any device and type the code. Either way the CLI receives an API key (90 days by default) and stores it in the OS keychain (macOS Keychain, Linux Secret Service, Windows Credential Manager). Without a keychain, or with `TT_KEYRING=off`, it goes in `credentials.json` in the tt config directory with mode 0600. `tt login --key <key>` stores an existing key instead.

After that every command (`tt ls`, `tt add`, `tt next`, `tt tui`, `tt web`, …) works against the server project, with the same output as local mode. The one difference: the server records who made each change from your login, so `--author` and `--created-by` are ignored.

```bash
tt whoami                               # your handle, projects and roles
tt project ls                           # projects you can access
tt project create myproj                # server admins
tt project member myproj alice member   # owners: viewer | member | owner, or --remove
tt export > backup.json                 # every task with comments and relations
tt logout                               # revokes the key and forgets it
```

`tt login` also makes that server your default, so account commands (`whoami`, `project`, `key`, `agent`, `logout`) work outside a repo without `--server`; `tt logout` clears it. They look for a server in this order: `--server`, `TT_SERVER`, the repo's `.tt.json`, then the default. Task commands (`ls`, `add`, …) only use a server when the repo has a `.tt.json`, or `TT_SERVER` and `TT_PROJECT` are both set.

`TT_SERVER`, `TT_PROJECT` and `TT_API_KEY` override `.tt.json` and the stored login. `tt sync` only applies to local databases.

The TUI and web UI refresh live when anyone changes a task. This also works locally: `tt tui` and `tt web` pick up writes from other processes, such as agents.

### Agents and API keys

Agents act with their own identity, on behalf of the human who registered them:

```bash
tt agent add claude/opus4.7
tt key create --agent claude/opus4.7 --project myproj --expires 720h   # prints the key once
```

Give the key to the agent as `TT_API_KEY`. Its changes show as `claude/opus4.7`, and the audit log records that it acted on your behalf. Agent keys are capped at `member` unless you pass `--role`. `tt key ls` lists your keys and `tt key revoke <id>` revokes one.

An agent can hand a sub-agent a narrower key without contacting the server:

```bash
tt key attenuate --task ab12cde --actions task:read,comment:create --expires 2h --label sub-reviewer
```

The new key can only do less than the one it came from: fewer projects, a lower role, writes only under a task, fewer actions, an earlier expiry. Revoking a key also revokes every key derived from it. Actions: `task:read`, `task:create`, `task:edit`, `task:status`, `task:assign`, `task:claim`, `comment:create`, `relation:write`, `import`, `key:create`, `project:admin`.

### Running a server

`tt server` needs Postgres and a master secret; see `tt server --help` for every setting.

```bash
export TT_DATABASE_URL=postgres://tt:…@db/tt
export TT_MASTER_KEY=$(openssl rand -base64 32)   # keep this stable and secret
export TT_BASE_URL=https://tt.example.com
export TT_GITHUB_CLIENT_ID=… TT_GITHUB_CLIENT_SECRET=…
export TT_ADMINS=your-github-login
tt server --addr :8080
```

For local development, `mise run server` builds tt and runs it against the Docker Postgres from `mise run pg:up` (started if needed), creating the database and a persistent master key in `~/.tt-master-key` on first run. Put GitHub credentials and other settings in the `[env]` section of `mise.local.toml` (gitignored; see `mise.local.toml.example`).

Create a GitHub OAuth app with the callback URL `$TT_BASE_URL/auth/github/callback`. Migrations run automatically on start. Put TLS in front with a reverse proxy, and don't let it buffer responses from `/api/v1/projects/*/events`, which stream live updates.

Without GitHub (local development, or bootstrapping), create users and keys directly:

```bash
tt server add-user jrdn --admin --github jrdn   # --github: link to that GitHub login
tt server create-key jrdn                       # then: tt login --key <key>
```

A user created this way is used by a later GitHub sign-in only if it was linked with `--github`, or afterwards with `tt server link-github <handle> <github-login>`. Otherwise a GitHub sign-in whose login matches an existing handle is refused with instructions, rather than merged into that account.

To move an existing local database onto the server, keeping IDs, timestamps and authors (safe to re-run):

```bash
tt server import --project myproj --as jrdn ~/.config/tt/myrepo.db
```

Default lifetimes: API keys 1 year (`TT_KEY_DEFAULT_TTL`, capped by `TT_KEY_MAX_TTL`), `tt login` keys 90 days (`TT_LOGIN_TTL`), JWTs 1 hour (`TT_JWT_TTL`). Revoking a key, removing a member or lowering their role takes effect immediately; open live-update streams close within 30 seconds.

### Observability

The server emits OpenTelemetry metrics and logs.

- **Metrics**: Prometheus format at `GET /metrics` on `--metrics-addr` (default `:9090`, a separate listener from the API so it isn't exposed publicly; empty disables). Series: `http_server_request_total`, `http_server_request_duration_seconds` and `http_server_active_requests`, labelled by method, route pattern and status. Scrape with Prometheus or Alloy (`prometheus.scrape`).
- **Logs**: JSON on stderr, including one line per request, so Alloy can tail the container's output. Set `OTEL_EXPORTER_OTLP_ENDPOINT` (e.g. `http://alloy:4318`) to also push them over OTLP/HTTP to an `otelcol.receiver.otlp`.

`OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES` set the resource attributes (default service name `tt-server`).

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
