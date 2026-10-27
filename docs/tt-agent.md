# The scheduled `tt-agent` worker

A cron job named `tt-agent` runs against this server every **30 minutes** as the
`hermes` handle (GitHub side: the bot account `j13g-hermes`). Each tick reads an
inbox, acts on it, and stops. Nobody is live in those sessions, so the worker
never asks questions in chat: decisions that belong to a human go into a ticket
comment and the ticket leaves the queue.

## What it does

**Works tickets.** A ticket assigned to `hermes` with status `ready` or
`in_progress` is claimed (`tt claim <id> --as hermes`), implemented on a branch
named `hermes/<id>-<slug>`, and opened as a pull request against the project's
repo. The worker follows the existing repo conventions, runs the repo's tests
(`go vet ./... && go test ./...` for this repo) and leaves the tree clean. The
PR body carries a `Ticket:` link back to the task, and is created with
`--reviewer jrdn --assignee jrdn`. The PR URL is then posted as a comment on the
ticket and the ticket moves to `in_review`. Tickets are never moved to `done` or
`cancelled` — review is jrdn's call. If tests fail or the run runs out of time,
the worker comments exactly where it stopped and leaves the ticket
`in_progress`, and may open at most one such ticket per tick.

**Replies to comments.** A comment addressed to `hermes` (a mention, or any
comment on a ticket assigned to `hermes`) that has no `hermes` comment after it
gets a short reply — substance only, one to four lines, with code or PR links
where relevant. If a reply is not warranted (acknowledgement, already answered,
informational), nothing is posted; the item is marked handled instead.

**Replies on GitHub.** Unanswered comments and review threads on PRs the worker
is involved with are answered with `gh pr comment` for conversation and
review-body comments, and via `POST /repos/<repo>/pulls/<n>/comments` with
`in_reply_to` for inline threads. Review feedback that asks for a code change is
applied on the same PR branch, tested, pushed and answered with the commit sha —
never a second PR. The worker never approves, merges, closes or force-pushes a
PR, and never pushes to `main`.

## Inbox detection

`tt-agent-inbox.py` builds three lists:

- `work` — tasks assigned to `hermes` in `ready` or `in_progress`;
- `replies` — comments addressed to `hermes` with no `hermes` comment after them;
- `prs` — unanswered comments/review threads on the worker's PRs.

Every item has a `key`. Detection is **server-state based**: a comment counts as
answered as soon as a `hermes` comment exists after it, so there is no local
bookkeeping to drift. The only local state is a small handled-key list, for items
deliberately left unanswered — `--handled <key>` adds an entry, `--unhandle`
removes one, and entries expire after 30 days. It lives in
`/opt/data/tt-agent/state.json` (override with `TT_AGENT_STATE`).

The cron job uses the script's default **digest** mode (one line per item) as its
monitor: a tick whose digest is identical to the previous tick does not wake the
agent at all, so idle time costs nothing. When nothing is pending — work,
replies and PR comments all empty — the worker replies `tt-agent: idle` and
stops. It also stops rather than working around a `tt` or `gh` auth/network
failure, and says what failed.

## Helper scripts (on the pod, `/opt/data/scripts/`)

- `tt-agent-inbox.py` — builds the inbox. Default digest, `--json` for full
  detail, `--handled` / `--unhandle` to manage suppressed keys, `--repo-map` to
  print the project-to-repo mapping.
- `tt-all.py` — lists every task across every project the API key can see
  (`--status`, `--project`, `--assignee`, `--open`, `--json`). The `tt` CLI
  only ever sees one project at a time, so this is how the worker surveys the
  whole board.

Both talk HTTP to the same `/api/v1` endpoints the web UI uses, authenticated
with `TT_SERVER` / `TT_API_KEY` from the environment. They send an explicit
`User-Agent`, because Cloudflare in front of the server rejects the default
`python-urllib` one with a 403.

## Project to repo mapping

Tickets live in the project's own repo:

- `tt` → `jrdn/tt`
- `puck` → `jrdn/puck`
- `life` → `jrdn/life`

Missing clones are fetched with `gh repo clone <owner>/<repo>
/opt/data/home/work/<name>`; existing ones are reused in place.
