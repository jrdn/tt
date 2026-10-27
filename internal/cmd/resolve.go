package cmd

import (
	"fmt"
	"strings"

	"github.com/jrdn/tt/internal/client"
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

// splitTaskRef splits a task reference that names its project, e.g.
// "tt.1hde6nf" or "tt:1hde6nf", into the project and the bare task ID. A plain
// ID has no qualifier. The separator may be a dot (what tt prints) or a colon
// (what people type), and the qualifier is a tt server project slug or, against
// a local database, the database name.
func splitTaskRef(raw string) (qualifier, id string) {
	if i := strings.IndexAny(raw, ".:"); i > 0 && i < len(raw)-1 {
		return raw[:i], raw[i+1:]
	}
	return "", raw
}

// resolveTask parses a task ID argument that may be qualified as
// <project>.<task_id>. A qualified ID reaches a task outside the current
// project: on a tt server it uses the named project, with a local database it
// opens the named database. An unqualified ID resolves in the current store.
// The caller must call t.Close() when done.
func resolveTask(rawID string) (*qualifiedTask, error) {
	qualifier, taskID := splitTaskRef(rawID)
	if qualifier == "" {
		return &qualifiedTask{store: store, id: rawID, opened: false}, nil
	}
	if remote != nil {
		if qualifier == remote.Project {
			return &qualifiedTask{store: store, id: taskID, opened: false}, nil
		}
		key, err := client.KeyFor(remote.Server)
		if err != nil {
			return nil, fmt.Errorf("project %q: %w", qualifier, err)
		}
		return &qualifiedTask{
			store:  client.NewStore(client.New(remote.Server, key), qualifier),
			id:     taskID,
			opened: true,
		}, nil
	}
	targetDB, err := ttDB.OpenDBByName(qualifier)
	if err != nil {
		return nil, fmt.Errorf("open database %q: %w", qualifier, err)
	}
	return &qualifiedTask{store: task.NewSQLStore(targetDB), id: taskID, opened: true}, nil
}

// Close releases the store if we opened it.
func (q *qualifiedTask) Close() {
	if q.opened {
		q.store.Close()
	}
}
