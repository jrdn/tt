-- users.github_login now means "link this GitHub account to this user".
-- Operator-created users used to record their handle here implicitly; clear
-- that so only an explicit link (add-user --github, link-github) can attach
-- a GitHub account to an existing user.
ALTER TABLE users ALTER COLUMN github_login DROP NOT NULL;
UPDATE users SET github_login = NULL WHERE github_id IS NULL;
CREATE UNIQUE INDEX users_github_login_unlinked_idx ON users (lower(github_login)) WHERE github_id IS NULL;
