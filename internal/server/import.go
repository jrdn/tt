package server

import (
	"context"
	"fmt"

	"github.com/jrdn/tt/internal/auth"
	ttdb "github.com/jrdn/tt/internal/db"
	"github.com/jrdn/tt/internal/task"
)

// ImportStats counts rows copied by ImportSQLite; rows already present are
// skipped, so re-running an import is safe.
type ImportStats struct {
	Tasks, Comments, Relations, ExternalRefs int
}

// ImportSQLite copies a local tt database into a project, keeping task IDs,
// timestamps and author handles. actor is recorded as the importer in the
// audit log.
func (d *Directory) ImportSQLite(ctx context.Context, project *Project, path string, actor *Principal) (ImportStats, error) {
	var st ImportStats
	src, err := ttdb.OpenPath(path)
	if err != nil {
		return st, err
	}
	defer src.Close()

	var tasks []task.Task
	var comments []task.Comment
	var rels []task.Relation
	var refs []task.ExternalRef
	for _, q := range []struct {
		dst any
		sql string
	}{
		{&tasks, `SELECT * FROM tasks ORDER BY rowid`},
		{&comments, `SELECT * FROM comments ORDER BY rowid`},
		{&rels, `SELECT * FROM relations`},
		{&refs, `SELECT * FROM external_refs`},
	} {
		if err := src.SelectContext(ctx, q.dst, q.sql); err != nil {
			return st, fmt.Errorf("read %s: %w", path, err)
		}
	}

	pdb, err := d.projectDB(ctx, project)
	if err != nil {
		return st, err
	}
	tx, err := pdb.BeginTxx(ctx, nil)
	if err != nil {
		return st, err
	}
	defer tx.Rollback()

	var imported []string
	// Insert tasks without parents first, then link them, so order doesn't
	// matter for the parent foreign key.
	for _, t := range tasks {
		res, err := tx.ExecContext(ctx, `INSERT INTO tasks
			(id, title, status, description, priority, created_by, assignee, due_date, created_at, updated_at, closed_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) ON CONFLICT (id) DO NOTHING`,
			t.ID, t.Title, t.Status, t.Description, t.Priority, t.CreatedBy, t.Assignee, t.DueDate,
			t.CreatedAt, t.UpdatedAt, t.ClosedAt)
		if err != nil {
			return st, fmt.Errorf("task %s: %w", t.ID, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			st.Tasks++
			imported = append(imported, t.ID)
		}
	}
	for _, t := range tasks {
		if t.ParentID != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE tasks SET parent_id = $2 WHERE id = $1 AND parent_id IS NULL`,
				t.ID, *t.ParentID); err != nil {
				return st, fmt.Errorf("task %s parent: %w", t.ID, err)
			}
		}
	}
	for _, c := range comments {
		res, err := tx.ExecContext(ctx, `INSERT INTO comments (id, task_id, author, body, created_at)
			VALUES ($1, $2, $3, $4, $5) ON CONFLICT (id) DO NOTHING`, c.ID, c.TaskID, c.Author, c.Body, c.CreatedAt)
		if err != nil {
			return st, fmt.Errorf("comment %s: %w", c.ID, err)
		}
		n, _ := res.RowsAffected()
		st.Comments += int(n)
	}
	for _, r := range rels {
		res, err := tx.ExecContext(ctx, `INSERT INTO relations (from_id, to_id, type) VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING`, r.FromID, r.ToID, r.Type)
		if err != nil {
			return st, fmt.Errorf("relation %s→%s: %w", r.FromID, r.ToID, err)
		}
		n, _ := res.RowsAffected()
		st.Relations += int(n)
	}
	for _, e := range refs {
		res, err := tx.ExecContext(ctx, `INSERT INTO external_refs (task_id, source, external_id, url)
			VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`, e.TaskID, e.Source, e.ExternalID, e.URL)
		if err != nil {
			return st, fmt.Errorf("external ref %s/%s: %w", e.Source, e.ExternalID, err)
		}
		n, _ := res.RowsAffected()
		st.ExternalRefs += int(n)
	}
	if err := tx.Commit(); err != nil {
		return st, err
	}

	c := &auth.Claims{}
	c.Subject = actor.ID
	for _, id := range imported {
		if err := d.RecordEvent(ctx, project, c, id, "import", map[string]any{"source": path}); err != nil {
			return st, err
		}
	}
	return st, nil
}
