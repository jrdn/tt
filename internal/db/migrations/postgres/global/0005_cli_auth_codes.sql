-- One-time codes for `tt login`'s loopback flow (RFC 8252 with PKCE). The
-- code is redirected to the CLI's 127.0.0.1 listener and can only be
-- redeemed with the verifier matching code_challenge.
CREATE TABLE cli_auth_codes (
	code_hash      BYTEA PRIMARY KEY,
	principal_id   TEXT NOT NULL REFERENCES principals(id),
	code_challenge TEXT NOT NULL,
	redirect_uri   TEXT NOT NULL,
	client_name    TEXT NOT NULL DEFAULT '',
	expires_at     TIMESTAMPTZ NOT NULL
);
