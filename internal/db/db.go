package db

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// SchemaVersion is incremented whenever the database schema changes.
const SchemaVersion = 1

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

CREATE TABLE IF NOT EXISTS meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL DEFAULT ''
);
`

// triggerDefs lists (name, body) for every mutation trigger. On each open
// they are dropped and recreated so the definitions here are always canonical.
var triggerDefs = []struct{ name, body string }{
	{"meta_mut_tasks_insert", `AFTER INSERT ON tasks BEGIN
	UPDATE meta SET value = NEW.updated_at                            WHERE key = 'last_mutation_at';
	UPDATE meta SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT)   WHERE key = 'mutation_count';
END`},
	{"meta_mut_tasks_update", `AFTER UPDATE ON tasks BEGIN
	UPDATE meta SET value = NEW.updated_at                            WHERE key = 'last_mutation_at';
	UPDATE meta SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT)   WHERE key = 'mutation_count';
END`},
	{"meta_mut_tasks_delete", `AFTER DELETE ON tasks BEGIN
	UPDATE meta SET value = strftime('%Y-%m-%dT%H:%M:%SZ','now')     WHERE key = 'last_mutation_at';
	UPDATE meta SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT)   WHERE key = 'mutation_count';
END`},
	{"meta_mut_comments_insert", `AFTER INSERT ON comments BEGIN
	UPDATE meta SET value = NEW.created_at                            WHERE key = 'last_mutation_at';
	UPDATE meta SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT)   WHERE key = 'mutation_count';
END`},
	{"meta_mut_comments_delete", `AFTER DELETE ON comments BEGIN
	UPDATE meta SET value = strftime('%Y-%m-%dT%H:%M:%SZ','now')     WHERE key = 'last_mutation_at';
	UPDATE meta SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT)   WHERE key = 'mutation_count';
END`},
	{"meta_mut_relations_insert", `AFTER INSERT ON relations BEGIN
	UPDATE meta SET value = strftime('%Y-%m-%dT%H:%M:%SZ','now')     WHERE key = 'last_mutation_at';
	UPDATE meta SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT)   WHERE key = 'mutation_count';
END`},
	{"meta_mut_relations_delete", `AFTER DELETE ON relations BEGIN
	UPDATE meta SET value = strftime('%Y-%m-%dT%H:%M:%SZ','now')     WHERE key = 'last_mutation_at';
	UPDATE meta SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT)   WHERE key = 'mutation_count';
END`},
}


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
	if _, err := db.Exec(`UPDATE tasks SET priority=3 WHERE priority=4`); err != nil {
		return nil, fmt.Errorf("migrate priority: %w", err)
	}
	if err := applyTriggers(db); err != nil {
		return nil, fmt.Errorf("apply triggers: %w", err)
	}
	if err := initMeta(db); err != nil {
		return nil, fmt.Errorf("init meta: %w", err)
	}
	return db, nil
}

// applyTriggers drops and recreates all mutation triggers so that the
// definitions in triggerDefs are always canonical regardless of DB age.
func applyTriggers(db *sqlx.DB) error {
	for _, t := range triggerDefs {
		if _, err := db.Exec(`DROP TRIGGER IF EXISTS ` + t.name); err != nil {
			return err
		}
		if _, err := db.Exec(`CREATE TRIGGER ` + t.name + ` ` + t.body); err != nil {
			return err
		}
	}
	return nil
}

func initMeta(db *sqlx.DB) error {
	_, err := db.Exec(`INSERT OR IGNORE INTO meta (key, value) VALUES
		('schema_version',  ?),
		('app_version',     ''),
		('last_mutation_at',''),
		('mutation_count',  '0')`,
		strconv.Itoa(SchemaVersion))
	if err != nil {
		return err
	}
	// Always reflect the current binary's schema version.
	if _, err := db.Exec(`UPDATE meta SET value = ? WHERE key = 'schema_version'`,
		strconv.Itoa(SchemaVersion)); err != nil {
		return err
	}
	// Back-fill last_mutation_at for pre-existing databases that predate this table.
	_, err = db.Exec(`UPDATE meta
		SET value = (SELECT COALESCE(MAX(updated_at), '') FROM tasks)
		WHERE key = 'last_mutation_at' AND value = ''`)
	return err
}

// GetMeta returns the value for key from the meta table.
func GetMeta(db *sqlx.DB, key string) (string, error) {
	var value string
	err := db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
	return value, err
}

// SetMeta upserts a key/value pair in the meta table.
func SetMeta(db *sqlx.DB, key, value string) error {
	_, err := db.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
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
	root, err := repoRoot()
	if err != nil {
		// Not in a git repo — use global fallback
		return "tasks", nil
	}

	// Check for explicit override in git config
	out, err := exec.Command("git", "-C", root, "config", "--local", "tt.db-name").Output()
	if err == nil {
		name := strings.TrimSpace(string(out))
		if name != "" {
			return name, nil
		}
	}

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

func repoRoot() (string, error) {
	root, err := gitRoot(".")
	if err != nil {
		return "", err
	}
	if primaryRoot, err := primaryWorktreeRoot(root); err == nil {
		root = primaryRoot
	}

	for {
		parent := filepath.Dir(root)
		if parent == root {
			return root, nil
		}

		parentRoot, err := gitRoot(parent)
		if err != nil || parentRoot == root {
			return root, nil
		}
		root = parentRoot
	}
}

func primaryWorktreeRoot(root string) (string, error) {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return "", err
	}
	commonDir := strings.TrimSpace(string(out))
	if commonDir == "" {
		return root, nil
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(root, commonDir)
	}
	commonDir = filepath.Clean(commonDir)
	if filepath.Base(commonDir) != ".git" {
		return root, nil
	}
	primaryRoot := filepath.Dir(commonDir)
	if _, err := os.Stat(primaryRoot); err != nil {
		return root, nil
	}
	return primaryRoot, nil
}

func gitRoot(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
