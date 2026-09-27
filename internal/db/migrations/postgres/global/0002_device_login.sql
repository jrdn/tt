-- Pending `tt login` requests (OAuth-style device flow). The CLI polls with
-- device_code; the user approves user_code in a logged-in browser.
CREATE TABLE device_logins (
	device_code_hash BYTEA PRIMARY KEY,
	user_code        TEXT NOT NULL UNIQUE,
	principal_id     TEXT REFERENCES principals(id), -- set on approval
	client_name      TEXT NOT NULL DEFAULT '',
	created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
	expires_at       TIMESTAMPTZ NOT NULL
);
