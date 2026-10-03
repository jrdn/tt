-- Mirrors the SQLite schema in db.go with native Postgres types.
-- Applied once per project schema; global tables live in migrations/global.

CREATE TABLE tasks (
	id          TEXT PRIMARY KEY,
	title       TEXT NOT NULL,
	status      TEXT NOT NULL DEFAULT 'open'
	            CHECK (status IN ('backlog','open','ready','in_progress','in_review','done','cancelled')),
	parent_id   TEXT REFERENCES tasks(id),
	description TEXT,
	priority    INTEGER NOT NULL DEFAULT 2 CHECK (priority BETWEEN 0 AND 3),
	created_by  TEXT,
	assignee    TEXT,
	due_date    TEXT CHECK (due_date ~ '^\d{4}-\d{2}-\d{2}$'),
	created_at  TIMESTAMPTZ NOT NULL,
	updated_at  TIMESTAMPTZ NOT NULL,
	closed_at   TIMESTAMPTZ
);

-- Prefix lookups use lower(id) LIKE 'abc%'.
CREATE INDEX tasks_id_prefix_idx ON tasks (lower(id) text_pattern_ops);
CREATE INDEX tasks_parent_idx ON tasks (parent_id);
CREATE INDEX tasks_assignee_status_idx ON tasks (assignee, status);

CREATE TABLE comments (
	id         TEXT PRIMARY KEY,
	task_id    TEXT NOT NULL REFERENCES tasks(id),
	author     TEXT,
	body       TEXT NOT NULL,
	created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX comments_task_idx ON comments (task_id, created_at);

CREATE TABLE relations (
	from_id TEXT NOT NULL REFERENCES tasks(id),
	to_id   TEXT NOT NULL REFERENCES tasks(id),
	type    TEXT NOT NULL CHECK (type IN ('blocks','duplicates','related')),
	PRIMARY KEY (from_id, to_id, type)
);

CREATE INDEX relations_to_idx ON relations (to_id, type);

CREATE TABLE external_refs (
	task_id     TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
	source      TEXT NOT NULL,
	external_id TEXT NOT NULL,
	url         TEXT,
	PRIMARY KEY (source, external_id)
);
