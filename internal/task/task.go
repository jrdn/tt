package task

import (
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

type Status string

const (
	StatusBacklog    Status = "backlog"
	StatusOpen       Status = "open"
	StatusInProgress Status = "in_progress"
	StatusDone       Status = "done"
	StatusCancelled  Status = "cancelled"
)

type Task struct {
	ID          string  `db:"id"          json:"id"`
	Title       string  `db:"title"        json:"title"`
	Status      Status  `db:"status"       json:"status"`
	ParentID    *string `db:"parent_id"    json:"parent_id"`
	Description *string `db:"description"  json:"description"`
	Priority    int     `db:"priority"     json:"priority"`
	CreatedBy   *string `db:"created_by"   json:"created_by"`
	Assignee    *string `db:"assignee"     json:"assignee"`
	DueDate     *string `db:"due_date"     json:"due_date"`
	CreatedAt   string  `db:"created_at"   json:"created_at"`
	UpdatedAt   string  `db:"updated_at"   json:"updated_at"`
	ClosedAt    *string `db:"closed_at"    json:"closed_at"`
}

type Comment struct {
	ID        string  `db:"id"         json:"id"`
	TaskID    string  `db:"task_id"    json:"task_id"`
	Author    *string `db:"author"     json:"author"`
	Body      string  `db:"body"       json:"body"`
	CreatedAt string  `db:"created_at" json:"created_at"`
}

type Relation struct {
	FromID string `db:"from_id" json:"from_id"`
	ToID   string `db:"to_id"   json:"to_id"`
	Type   string `db:"type"    json:"type"`
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func Create(db *sqlx.DB, title string, opts CreateOpts) (*Task, error) {
	id := NewID()
	ts := now()
	t := &Task{
		ID:          id,
		Title:       title,
		Status:      StatusOpen,
		ParentID:    opts.ParentID,
		Description: opts.Description,
		Priority:    opts.Priority,
		CreatedBy:   opts.CreatedBy,
		Assignee:    opts.Assignee,
		DueDate:     opts.DueDate,
		CreatedAt:   ts,
		UpdatedAt:   ts,
	}
	if t.Priority == 0 && !opts.PrioritySet {
		t.Priority = 2
	}
	_, err := db.NamedExec(`INSERT INTO tasks
		(id,title,status,parent_id,description,priority,created_by,assignee,due_date,created_at,updated_at)
		VALUES
		(:id,:title,:status,:parent_id,:description,:priority,:created_by,:assignee,:due_date,:created_at,:updated_at)`,
		t)
	return t, err
}

type CreateOpts struct {
	ParentID    *string
	Description *string
	Priority    int
	PrioritySet bool
	CreatedBy   *string
	Assignee    *string
	DueDate     *string
}

func Get(db *sqlx.DB, prefix string) (*Task, error) {
	var tasks []Task
	err := db.Select(&tasks, `SELECT * FROM tasks WHERE id LIKE ? || '%'`, prefix)
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("no task matching %q", prefix)
	}
	if len(tasks) > 1 {
		return nil, fmt.Errorf("ambiguous prefix %q matches %d tasks", prefix, len(tasks))
	}
	return &tasks[0], nil
}

type ListOpts struct {
	Status   string
	ParentID string
	Assignee string
	All      bool
	Ready    bool
}

func List(db *sqlx.DB, opts ListOpts) ([]Task, error) {
	query := `SELECT * FROM tasks WHERE 1=1`
	args := []any{}

	if opts.Ready {
		query += ` AND status = 'open'`
		query += ` AND NOT EXISTS (
			SELECT 1 FROM relations r
			JOIN tasks b ON b.id = r.from_id
			WHERE r.to_id = tasks.id AND r.type = 'blocks'
			AND b.status NOT IN ('done', 'cancelled')
		)`
	} else if opts.Status != "" {
		query += ` AND status = ?`
		args = append(args, opts.Status)
	} else if !opts.All {
		query += ` AND status IN ('open', 'in_progress')`
	}
	if opts.Assignee != "" {
		query += ` AND assignee = ?`
		args = append(args, opts.Assignee)
	}
	if opts.ParentID != "" {
		query += ` AND parent_id LIKE ? || '%'`
		args = append(args, opts.ParentID)
	} else {
		query += ` AND parent_id IS NULL`
	}
	query += ` ORDER BY priority ASC, created_at ASC`

	var tasks []Task
	err := db.Select(&tasks, query, args...)
	return tasks, err
}

type UpdateOpts struct {
	Status      *Status
	Title       *string
	Description *string
	Priority    *int
	Assignee    *string
	DueDate     *string
	ParentID    *string
}

func Update(db *sqlx.DB, prefix string, opts UpdateOpts) (*Task, error) {
	t, err := Get(db, prefix)
	if err != nil {
		return nil, err
	}

	if opts.Status != nil {
		t.Status = *opts.Status
		if *opts.Status == StatusDone || *opts.Status == StatusCancelled {
			ts := now()
			t.ClosedAt = &ts
		} else {
			t.ClosedAt = nil
		}
	}
	if opts.Title != nil {
		t.Title = *opts.Title
	}
	if opts.Description != nil {
		t.Description = opts.Description
	}
	if opts.Priority != nil {
		t.Priority = *opts.Priority
	}
	if opts.Assignee != nil {
		t.Assignee = opts.Assignee
	}
	if opts.DueDate != nil {
		t.DueDate = opts.DueDate
	}
	if opts.ParentID != nil {
		t.ParentID = opts.ParentID
	}
	t.UpdatedAt = now()

	_, err = db.NamedExec(`UPDATE tasks SET
		title=:title, status=:status, parent_id=:parent_id, description=:description,
		priority=:priority, assignee=:assignee, due_date=:due_date,
		updated_at=:updated_at, closed_at=:closed_at
		WHERE id=:id`, t)
	return t, err
}

func Save(db *sqlx.DB, t *Task) error {
	_, err := db.NamedExec(`UPDATE tasks SET
		title=:title, status=:status, parent_id=:parent_id, description=:description,
		priority=:priority, assignee=:assignee, due_date=:due_date,
		updated_at=:updated_at, closed_at=:closed_at
		WHERE id=:id`, t)
	return err
}

func Search(db *sqlx.DB, query string) ([]Task, error) {
	q := "%" + query + "%"
	var tasks []Task
	err := db.Select(&tasks, `
		SELECT DISTINCT t.* FROM tasks t
		LEFT JOIN comments c ON c.task_id = t.id
		WHERE t.title LIKE ? OR t.description LIKE ? OR c.body LIKE ?
		ORDER BY t.priority ASC, t.created_at ASC`, q, q, q)
	return tasks, err
}

func AddComment(db *sqlx.DB, taskPrefix, body string, author *string) (*Comment, error) {
	t, err := Get(db, taskPrefix)
	if err != nil {
		return nil, err
	}
	c := &Comment{
		ID:        NewID(),
		TaskID:    t.ID,
		Author:    author,
		Body:      body,
		CreatedAt: now(),
	}
	_, err = db.NamedExec(`INSERT INTO comments (id,task_id,author,body,created_at)
		VALUES (:id,:task_id,:author,:body,:created_at)`, c)
	return c, err
}

func GetComments(db *sqlx.DB, taskID string) ([]Comment, error) {
	var comments []Comment
	err := db.Select(&comments, `SELECT * FROM comments WHERE task_id=? ORDER BY created_at ASC`, taskID)
	return comments, err
}

func AddRelation(db *sqlx.DB, fromPrefix, relType, toPrefix string) error {
	from, err := Get(db, fromPrefix)
	if err != nil {
		return fmt.Errorf("from: %w", err)
	}
	to, err := Get(db, toPrefix)
	if err != nil {
		return fmt.Errorf("to: %w", err)
	}
	_, err = db.Exec(`INSERT OR REPLACE INTO relations (from_id, to_id, type) VALUES (?,?,?)`,
		from.ID, to.ID, relType)
	return err
}

func RemoveRelation(db *sqlx.DB, fromPrefix, relType, toPrefix string) error {
	from, err := Get(db, fromPrefix)
	if err != nil {
		return fmt.Errorf("from: %w", err)
	}
	to, err := Get(db, toPrefix)
	if err != nil {
		return fmt.Errorf("to: %w", err)
	}
	_, err = db.Exec(`DELETE FROM relations WHERE from_id=? AND to_id=? AND type=?`, from.ID, to.ID, relType)
	return err
}

func GetRelations(db *sqlx.DB, taskID string) ([]Relation, error) {
	var rels []Relation
	err := db.Select(&rels, `SELECT * FROM relations WHERE from_id=? OR to_id=?`, taskID, taskID)
	return rels, err
}
