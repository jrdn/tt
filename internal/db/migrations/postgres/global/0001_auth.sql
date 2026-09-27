-- Server-wide tables in the public schema. Each project's tasks live in
-- their own schema (see migrations/project).

-- Anything that can act or be assigned: humans, agents, service accounts.
CREATE TABLE principals (
	id             TEXT PRIMARY KEY,
	kind           TEXT NOT NULL CHECK (kind IN ('user','agent','service')),
	handle         TEXT NOT NULL UNIQUE,
	-- Agents act on behalf of the human who owns them.
	owner_id       TEXT REFERENCES principals(id),
	created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
	disabled_at    TIMESTAMPTZ,
	-- Tokens issued before this instant are rejected (immediate revocation).
	revoked_before TIMESTAMPTZ,
	CHECK ((kind = 'agent') = (owner_id IS NOT NULL))
);

-- Login details for human principals.
CREATE TABLE users (
	principal_id TEXT PRIMARY KEY REFERENCES principals(id),
	github_id    BIGINT UNIQUE, -- NULL for users created by the operator CLI
	github_login TEXT NOT NULL,
	email        TEXT,
	name         TEXT,
	is_admin     BOOLEAN NOT NULL DEFAULT false
);

CREATE TABLE projects (
	id          TEXT PRIMARY KEY,
	slug        TEXT NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
	name        TEXT NOT NULL,
	schema_name TEXT NOT NULL UNIQUE,
	created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Agents have no rows here: they inherit their owner's memberships.
CREATE TABLE project_members (
	project_id   TEXT NOT NULL REFERENCES projects(id),
	principal_id TEXT NOT NULL REFERENCES principals(id),
	role         TEXT NOT NULL CHECK (role IN ('viewer','member','owner')),
	PRIMARY KEY (project_id, principal_id)
);

-- Root API keys. The macaroon root secret is derived from the server's
-- master key and the key id, so nothing secret is stored; attenuated
-- keys are never stored at all.
CREATE TABLE api_keys (
	id             TEXT PRIMARY KEY,
	principal_id   TEXT NOT NULL REFERENCES principals(id),
	created_by     TEXT NOT NULL REFERENCES principals(id),
	name           TEXT NOT NULL DEFAULT '',
	project_scopes TEXT,             -- comma-separated slugs; NULL = every project the principal can see
	max_role       TEXT CHECK (max_role IN ('viewer','member','owner')),
	expires_at     TIMESTAMPTZ,
	created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
	last_used_at   TIMESTAMPTZ,
	revoked_at     TIMESTAMPTZ
);

CREATE INDEX api_keys_principal_idx ON api_keys (principal_id);

-- Browser sessions; the cookie holds a random token, stored hashed.
CREATE TABLE sessions (
	token_hash   BYTEA PRIMARY KEY,
	principal_id TEXT NOT NULL REFERENCES principals(id),
	created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
	expires_at   TIMESTAMPTZ NOT NULL
);

-- Audit history, written in the same request as each change.
CREATE TABLE task_events (
	id           BIGSERIAL PRIMARY KEY,
	project_id   TEXT NOT NULL REFERENCES projects(id),
	task_id      TEXT NOT NULL,
	actor_id     TEXT NOT NULL REFERENCES principals(id),
	actor_label  TEXT,
	token_id     TEXT,
	at           TIMESTAMPTZ NOT NULL DEFAULT now(),
	action       TEXT NOT NULL,
	detail       JSONB
);

CREATE INDEX task_events_task_idx ON task_events (project_id, task_id, at);
