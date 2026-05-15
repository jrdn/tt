package cmd

import (
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
	ttDB "github.com/jrdn/tt/internal/db"
)

// qualifiedTask represents a task ID resolved to a specific database.
type qualifiedTask struct {
	database *sqlx.DB
	id       string
	// opened tracks whether we opened this DB ourselves (and need to close it).
	opened bool
}

// resolveTask parses a task ID argument that may be qualified as <db_name>.<task_id>.
// If qualified, it opens the named database. If not, it returns the global db.
// The caller must call t.Close() when done.
func resolveTask(rawID string) (*qualifiedTask, error) {
	if dot := strings.IndexByte(rawID, '.'); dot > 0 {
		dbName := rawID[:dot]
		taskID := rawID[dot+1:]
		targetDB, err := ttDB.OpenDBByName(dbName)
		if err != nil {
			return nil, fmt.Errorf("open database %q: %w", dbName, err)
		}
		return &qualifiedTask{database: targetDB, id: taskID, opened: true}, nil
	}
	return &qualifiedTask{database: db, id: rawID, opened: false}, nil
}

// Close releases the database connection if we opened it.
func (q *qualifiedTask) Close() {
	if q.opened {
		q.database.Close()
	}
}
