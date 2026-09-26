package cmd

import (
	"fmt"
	"strings"

	ttDB "github.com/jrdn/tt/internal/db"
	"github.com/jrdn/tt/internal/task"
)

// qualifiedTask represents a task ID resolved to a specific store.
type qualifiedTask struct {
	store task.Store
	id    string
	// opened tracks whether we opened this store ourselves (and need to close it).
	opened bool
}

// resolveTask parses a task ID argument that may be qualified as <db_name>.<task_id>.
// If qualified, it opens the named database. If not, it returns the global store.
// The caller must call t.Close() when done.
func resolveTask(rawID string) (*qualifiedTask, error) {
	if dot := strings.IndexByte(rawID, '.'); dot > 0 {
		dbName := rawID[:dot]
		taskID := rawID[dot+1:]
		targetDB, err := ttDB.OpenDBByName(dbName)
		if err != nil {
			return nil, fmt.Errorf("open database %q: %w", dbName, err)
		}
		return &qualifiedTask{store: task.NewSQLStore(targetDB), id: taskID, opened: true}, nil
	}
	return &qualifiedTask{store: store, id: rawID, opened: false}, nil
}

// Close releases the store if we opened it.
func (q *qualifiedTask) Close() {
	if q.opened {
		q.store.Close()
	}
}
