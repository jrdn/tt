## Build & Test

```bash
mise run test         # unit tests
mise run lint         # go vet
mise run test:ui      # headless-Chrome tests for the web UI (skipped without Chrome)
```

`mise run test:pg` covers the Postgres-backed suites (`mise run pg:up` first)
and `mise run test:e2e` drives the real binary end to end.

CI (`.github/workflows/test.yml`) runs all of these on every pull request to
`main`: vet and unit tests, the web UI tests, the integration suite and the
end-to-end suite, each against a Postgres 17 service container where one is
needed.

## Commits and releases

Release tags are automatic. Every push to `main` runs
`.github/workflows/tag.yml`, which reads the commits since the last tag, picks
the next `vX.Y.Z`, tags the commit and triggers the image build. Nothing is
tagged by hand.

The commit subject decides the bump, so write it as a Conventional Commit:

| Subject | Bump |
| --- | --- |
| `fix: keep the project switcher sticky` | patch — `v0.0.8` → `v0.0.9` |
| `feat: add deep links to tasks` | minor — `v0.0.8` → `v0.1.0` |
| `feat!: drop the v1 API`, or a `BREAKING CHANGE:` footer | major — `v1.0.0` |
| `docs:` `chore:` `ci:` `style:` `test:` `build:` | none on their own |
| no type, e.g. `a0ssm4f: add an activity feed` | patch, so old-style commits still ship |

- Put the tt task id in the body (`Task: a0ssm4f`), not in the subject. The
  branch name `hermes/<task>-<slug>` keeps the task → PR link either way.
- `Release-Type: minor` (on its own line, anywhere in the message) overrides the
  guess, and `[skip release]` suppresses the release for that push entirely.
- A push whose commits are all non-release types tags nothing, so a docs-only
  change does not burn a version.
- Bump the running server afterwards by hand: run `bump-all` in `jrdn/config`,
  which defaults to the newest tag here.
- The rules and their tests live in `.github/scripts/next_version.py` and
  `.github/scripts/test_next_version.py`.

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
- A task in another project takes a qualified ID: `tt show tt.1hde6nf` or `tt:1hde6nf`. The web UI always shows the qualified form.
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
