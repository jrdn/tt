package db

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS tasks (
	id          TEXT PRIMARY KEY,
	title       TEXT NOT NULL,
	status      TEXT NOT NULL DEFAULT 'open',
	parent_id   TEXT REFERENCES tasks(id),
	description TEXT,
	priority    INTEGER NOT NULL DEFAULT 2,
	created_by  TEXT,
	assignee    TEXT,
	due_date    TEXT,
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL,
	closed_at   TEXT
);

CREATE TABLE IF NOT EXISTS comments (
	id         TEXT PRIMARY KEY,
	task_id    TEXT NOT NULL REFERENCES tasks(id),
	author     TEXT,
	body       TEXT NOT NULL,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS relations (
	from_id TEXT NOT NULL REFERENCES tasks(id),
	to_id   TEXT NOT NULL REFERENCES tasks(id),
	type    TEXT NOT NULL CHECK(type IN ('blocks','duplicates','related')),
	PRIMARY KEY (from_id, to_id, type)
);
`

func Open() (*sqlx.DB, error) {
	path, err := dbPath()
	if err != nil {
		return nil, err
	}
	return OpenPath(path)
}

func OpenPath(path string) (*sqlx.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}
	db, err := sqlx.Open("sqlite", path+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return db, nil
}

func dbPath() (string, error) {
	if v := os.Getenv("TASKS_DB"); v != "" {
		return v, nil
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	base := filepath.Join(configDir, "tt")

	name, err := repoDBName()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, name+".db"), nil
}

func repoDBName() (string, error) {
	// Check for explicit override in git config
	out, err := exec.Command("git", "config", "--local", "tt.db-name").Output()
	if err == nil {
		name := strings.TrimSpace(string(out))
		if name != "" {
			return name, nil
		}
	}

	// Find git root
	out, err = exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		// Not in a git repo — use global fallback
		return "tasks", nil
	}
	root := strings.TrimSpace(string(out))

	// Check for name collision: two repos with same basename
	repoName := filepath.Base(root)
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dbFile := filepath.Join(configDir, "tt", repoName+".db")

	// If db exists, it might belong to a different repo — check marker
	markerFile := filepath.Join(configDir, "tt", ".repo-roots", repoName)
	if data, err := os.ReadFile(markerFile); err == nil {
		storedRoot := strings.TrimSpace(string(data))
		if storedRoot != root {
			return "", fmt.Errorf(
				"repo name collision: both %q and %q map to %q\nRun: git config tt.db-name <unique-name>",
				root, storedRoot, dbFile,
			)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		// First time — record this repo's root
		if err := os.MkdirAll(filepath.Dir(markerFile), 0700); err != nil {
			return "", err
		}
		if err := os.WriteFile(markerFile, []byte(root), 0600); err != nil {
			return "", err
		}
	}

	return repoName, nil
}
