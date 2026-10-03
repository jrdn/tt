package task

import (
	"database/sql/driver"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

var validStatuses = map[Status]bool{
	StatusBacklog:    true,
	StatusOpen:       true,
	StatusReady:      true,
	StatusInProgress: true,
	StatusInReview:   true,
	StatusDone:       true,
	StatusCancelled:  true,
}

func validateStatus(s Status) error {
	if !validStatuses[s] {
		return fmt.Errorf("invalid status %q: must be one of backlog, open, ready, in_progress, in_review, done, cancelled", s)
	}
	return nil
}

func validatePriority(p int) error {
	if p < 0 || p > 3 {
		return fmt.Errorf("invalid priority %d: must be 0–3 (0=critical, 3=low)", p)
	}
	return nil
}

func validateDueDate(d string) error {
	if _, err := time.Parse("2006-01-02", d); err != nil {
		return fmt.Errorf("invalid due date %q: must be YYYY-MM-DD", d)
	}
	return nil
}

// Validate checks a whole task's fields, for callers that write with Save.
func Validate(t *Task) error {
	if err := validateStatus(t.Status); err != nil {
		return err
	}
	if err := validatePriority(t.Priority); err != nil {
		return err
	}
	if t.DueDate != nil {
		return validateDueDate(string(*t.DueDate))
	}
	return nil
}

// resolveParent turns a parent ID or prefix into the parent's full ID.
// self is the task being reparented ("" when creating).
func resolveParent(db *sqlx.DB, parentID, self string) (*string, error) {
	p, err := Get(db, parentID)
	if err != nil {
		return nil, fmt.Errorf("parent task %q not found", parentID)
	}
	if p.ID == self {
		return nil, fmt.Errorf("a task can't be its own parent")
	}
	return &p.ID, nil
}

func nonEmpty(s *string) *string {
	if s == nil || *s == "" {
		return nil
	}
	return s
}

type Status string

const (
	StatusBacklog    Status = "backlog"
	StatusOpen       Status = "open"
	StatusReady      Status = "ready"
	StatusInProgress Status = "in_progress"
	StatusInReview   Status = "in_review"
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
	DueDate     *Date   `db:"due_date"     json:"due_date"`
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

// Date is a calendar date, YYYY-MM-DD. It scans from SQLite TEXT and
// Postgres DATE alike, and marshals to JSON as the same string.
type Date string

func (d *Date) Scan(v any) error {
	switch v := v.(type) {
	case string:
		*d = Date(v)
	case []byte:
		*d = Date(v)
	case time.Time:
		*d = Date(v.Format("2006-01-02"))
	default:
		return fmt.Errorf("scan date: unexpected %T", v)
	}
	return nil
}

func (d Date) Value() (driver.Value, error) { return string(d), nil }

// DatePtr converts an optional string to an optional Date.
func DatePtr(s *string) *Date {
	if s == nil {
		return nil
	}
	d := Date(*s)
	return &d
}

// StringPtr converts an optional Date to an optional string.
func (d *Date) StringPtr() *string {
	if d == nil {
		return nil
	}
	s := string(*d)
	return &s
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// prefixPattern builds a case-insensitive LIKE pattern for an ID prefix,
// matched against lower(id). SQLite's LIKE ignores ASCII case but Postgres's
// doesn't, so both sides are lowered explicitly.
func prefixPattern(prefix string) string {
	return strings.ToLower(prefix) + "%"
}

// insertOrder names the column that orders rows by insertion, used to break
// ties between equal one-second timestamps: rowid on SQLite, seq on Postgres.
func insertOrder(db *sqlx.DB) string {
	if db.DriverName() == "pgx" {
		return "seq"
	}
	return "rowid"
}

// forUpdateSkipLocked returns a row-locking clause on Postgres so concurrent
// Next calls never claim the same task. SQLite serializes writers instead.
func forUpdateSkipLocked(db *sqlx.DB) string {
	if db.DriverName() == "pgx" {
		return "\n\t\tFOR UPDATE SKIP LOCKED"
	}
	return ""
}

func touchTask(db *sqlx.DB, id string) {
	db.Exec(db.Rebind(`UPDATE tasks SET updated_at=? WHERE id=?`), now(), id)
}

func Create(db *sqlx.DB, title string, opts CreateOpts) (*Task, error) {
	if opts.PrioritySet {
		if err := validatePriority(opts.Priority); err != nil {
			return nil, err
		}
	}
	if opts.ParentID != nil {
		p, err := resolveParent(db, *opts.ParentID, "")
		if err != nil {
			return nil, err
		}
		opts.ParentID = p
	}
	if opts.DueDate != nil {
		if err := validateDueDate(*opts.DueDate); err != nil {
			return nil, err
		}
	}

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
		DueDate:     DatePtr(opts.DueDate),
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
	err := db.Select(&tasks, db.Rebind(`SELECT * FROM tasks WHERE lower(id) LIKE ?`), prefixPattern(prefix))
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
		query += ` AND status IN ('open', 'ready')`
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
		query += ` AND status IN ('open', 'ready', 'in_progress', 'in_review')`
	}
	if opts.Assignee != "" {
		query += ` AND assignee = ?`
		args = append(args, opts.Assignee)
	}
	if opts.ParentID != "" {
		query += ` AND lower(parent_id) LIKE ?`
		args = append(args, prefixPattern(opts.ParentID))
	}
	query += ` ORDER BY priority ASC, created_at ASC, ` + insertOrder(db)

	var tasks []Task
	err := db.Select(&tasks, db.Rebind(query), args...)
	return tasks, err
}

// Recent returns the most recently updated tasks, newest first.
func Recent(db *sqlx.DB, limit int) ([]Task, error) {
	var tasks []Task
	err := db.Select(&tasks, db.Rebind(`SELECT * FROM tasks ORDER BY updated_at DESC, `+insertOrder(db)+` DESC LIMIT ?`), limit)
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
	if opts.Status != nil {
		if err := validateStatus(*opts.Status); err != nil {
			return nil, err
		}
	}
	if opts.Priority != nil {
		if err := validatePriority(*opts.Priority); err != nil {
			return nil, err
		}
	}
	if opts.DueDate != nil && *opts.DueDate != "" {
		if err := validateDueDate(*opts.DueDate); err != nil {
			return nil, err
		}
	}

	t, err := Get(db, prefix)
	if err != nil {
		return nil, err
	}
	var parent *string
	if opts.ParentID != nil && *opts.ParentID != "" {
		if parent, err = resolveParent(db, *opts.ParentID, t.ID); err != nil {
			return nil, err
		}
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
	// An empty string clears an optional field.
	if opts.Description != nil {
		t.Description = nonEmpty(opts.Description)
	}
	if opts.Priority != nil {
		t.Priority = *opts.Priority
	}
	if opts.Assignee != nil {
		t.Assignee = nonEmpty(opts.Assignee)
	}
	if opts.DueDate != nil {
		t.DueDate = DatePtr(nonEmpty(opts.DueDate))
	}
	if opts.ParentID != nil {
		t.ParentID = parent
	}
	t.UpdatedAt = now()

	_, err = db.NamedExec(`UPDATE tasks SET
		title=:title, status=:status, parent_id=:parent_id, description=:description,
		priority=:priority, assignee=:assignee, due_date=:due_date,
		updated_at=:updated_at, closed_at=:closed_at
		WHERE id=:id`, t)
	return t, err
}

// Save writes a whole task, as edited by tt edit or the web UI, after
// validating it and resolving a parent prefix.
func Save(db *sqlx.DB, t *Task) error {
	if err := Validate(t); err != nil {
		return err
	}
	if t.ParentID != nil {
		p, err := resolveParent(db, *t.ParentID, t.ID)
		if err != nil {
			return err
		}
		t.ParentID = p
	}
	_, err := db.NamedExec(`UPDATE tasks SET
		title=:title, status=:status, parent_id=:parent_id, description=:description,
		priority=:priority, assignee=:assignee, due_date=:due_date,
		updated_at=:updated_at, closed_at=:closed_at
		WHERE id=:id`, t)
	return err
}

// fuzzyScore returns (matched, score). Higher score = better match.
// A substring match outscores a fuzzy subsequence match; gaps between
// matched characters reduce the score.
func fuzzyScore(pattern, text string) (bool, int) {
	p := strings.ToLower(pattern)
	t := strings.ToLower(text)
	if strings.Contains(t, p) {
		return true, len(p)*10 + 100
	}
	pr := []rune(p)
	pi := 0
	score := 0
	prev := -1
	for i, c := range t {
		if pi < len(pr) && c == pr[pi] {
			pi++
			if prev >= 0 {
				score -= i - prev - 1
			}
			prev = i
		}
	}
	if pi == len(pr) {
		return true, score
	}
	return false, 0
}

// subsequencePattern turns a query into a LIKE pattern that matches exactly
// the texts fuzzyScore accepts: the query's characters in order, anything in
// between ("brsk" -> "%b%r%s%k%"). LIKE metacharacters are escaped with \.
func subsequencePattern(query string) string {
	var b strings.Builder
	b.WriteByte('%')
	for _, r := range strings.ToLower(query) {
		if r == '%' || r == '_' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
		b.WriteByte('%')
	}
	return b.String()
}

// Search ranks tasks whose title, description, assignee, or any comment
// matches query (substring or fuzzy subsequence). The database narrows the
// candidates with a LIKE pattern; only those are loaded and scored. On
// SQLite this needs the tt_lower function registered by the db package.
func Search(db *sqlx.DB, query string) ([]Task, error) {
	lower := "tt_lower"
	if db.DriverName() == "pgx" {
		lower = "lower"
	}
	like := func(col string) string { return lower + "(" + col + ") LIKE ? ESCAPE '\\'" }
	q := `SELECT * FROM tasks WHERE ` + like("title") + ` OR ` + like("description") + ` OR ` + like("assignee") +
		// Uncorrelated, so comments are scanned once rather than per task.
		` OR id IN (SELECT task_id FROM comments WHERE ` + like("body") + ` OR ` + like("author") + `)
		ORDER BY ` + insertOrder(db)
	pat := subsequencePattern(query)
	var all []Task
	if err := db.Select(&all, db.Rebind(q), pat, pat, pat, pat, pat); err != nil {
		return nil, err
	}
	commentsByTask, err := commentsFor(db, all)
	if err != nil {
		return nil, err
	}

	type entry struct {
		task  Task
		score int
	}
	var results []entry

	for _, t := range all {
		matched := false
		score := 0

		if ok, s := fuzzyScore(query, t.Title); ok {
			s += 100
			if !matched || s > score {
				score = s
			}
			matched = true
		}
		if t.Description != nil {
			if ok, s := fuzzyScore(query, *t.Description); ok {
				s += 50
				if !matched || s > score {
					score = s
				}
				matched = true
			}
		}

		if t.Assignee != nil {
			if ok, s := fuzzyScore(query, *t.Assignee); ok {
				s += 80
				if !matched || s > score {
					score = s
				}
				matched = true
			}
		}

		for _, c := range commentsByTask[t.ID] {
			if ok, s := fuzzyScore(query, c.Body); ok {
				if !matched || s > score {
					score = s
				}
				matched = true
			}
			if c.Author != nil {
				if ok, s := fuzzyScore(query, *c.Author); ok {
					if !matched || s > score {
						score = s
					}
					matched = true
				}
			}
		}

		if matched {
			results = append(results, entry{t, score})
		}
	}

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		if results[i].task.Priority != results[j].task.Priority {
			return results[i].task.Priority < results[j].task.Priority
		}
		return results[i].task.CreatedAt < results[j].task.CreatedAt
	})

	out := make([]Task, len(results))
	for i, r := range results {
		out[i] = r.task
	}
	return out, nil
}

// commentsFor loads the comments of the given tasks, keyed by task ID, in
// batches small enough for any database's parameter limit.
func commentsFor(db *sqlx.DB, tasks []Task) (map[string][]Comment, error) {
	byTask := map[string][]Comment{}
	const batch = 500
	for i := 0; i < len(tasks); i += batch {
		ids := make([]string, 0, batch)
		for _, t := range tasks[i:min(i+batch, len(tasks))] {
			ids = append(ids, t.ID)
		}
		q, args, err := sqlx.In(`SELECT * FROM comments WHERE task_id IN (?)`, ids)
		if err != nil {
			return nil, err
		}
		var cs []Comment
		if err := db.Select(&cs, db.Rebind(q), args...); err != nil {
			return nil, err
		}
		for _, c := range cs {
			byTask[c.TaskID] = append(byTask[c.TaskID], c)
		}
	}
	return byTask, nil
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
	if err == nil {
		touchTask(db, t.ID)
	}
	return c, err
}

func GetComments(db *sqlx.DB, taskID string) ([]Comment, error) {
	var comments []Comment
	err := db.Select(&comments, db.Rebind(`SELECT * FROM comments WHERE task_id=? ORDER BY created_at ASC, `+insertOrder(db)), taskID)
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
	_, err = db.Exec(db.Rebind(`INSERT INTO relations (from_id, to_id, type) VALUES (?,?,?) ON CONFLICT DO NOTHING`),
		from.ID, to.ID, relType)
	if err == nil {
		touchTask(db, from.ID)
		touchTask(db, to.ID)
	}
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
	_, err = db.Exec(db.Rebind(`DELETE FROM relations WHERE from_id=? AND to_id=? AND type=?`), from.ID, to.ID, relType)
	if err == nil {
		touchTask(db, from.ID)
		touchTask(db, to.ID)
	}
	return err
}

func GetRelations(db *sqlx.DB, taskID string) ([]Relation, error) {
	var rels []Relation
	err := db.Select(&rels, db.Rebind(`SELECT * FROM relations WHERE from_id=? OR to_id=?`), taskID, taskID)
	return rels, err
}

// Next atomically finds and claims the highest-priority ready task assigned to
// handle, returning nil if no such task exists.
func Next(db *sqlx.DB, handle string) (*Task, error) {
	tx, err := db.Beginx()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var t Task
	err = tx.Get(&t, tx.Rebind(`SELECT * FROM tasks
		WHERE assignee = ? AND status IN ('open', 'ready')
		AND NOT EXISTS (
			SELECT 1 FROM relations r
			JOIN tasks b ON b.id = r.from_id
			WHERE r.to_id = tasks.id AND r.type = 'blocks'
			AND b.status NOT IN ('done', 'cancelled')
		)
		ORDER BY priority ASC, created_at ASC, tasks.`+insertOrder(db)+`
		LIMIT 1`+forUpdateSkipLocked(db)), handle)
	if err != nil {
		return nil, nil // no matching task
	}

	ts := now()
	t.Status = StatusInProgress
	t.UpdatedAt = ts
	if _, err := tx.Exec(tx.Rebind(`UPDATE tasks SET status='in_progress', updated_at=? WHERE id=?`), ts, t.ID); err != nil {
		return nil, err
	}
	return &t, tx.Commit()
}

type ExternalRef struct {
	TaskID     string  `db:"task_id"     json:"task_id"`
	Source     string  `db:"source"      json:"source"`
	ExternalID string  `db:"external_id" json:"external_id"`
	URL        *string `db:"url"         json:"url,omitempty"`
}

func UpsertExternalRef(db *sqlx.DB, taskID, source, externalID string, url *string) error {
	_, err := db.Exec(db.Rebind(`INSERT INTO external_refs (task_id, source, external_id, url)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(source, external_id) DO UPDATE SET task_id=excluded.task_id, url=excluded.url`),
		taskID, source, externalID, url)
	return err
}

func FindByExternalRef(db *sqlx.DB, source, externalID string) (*Task, error) {
	var t Task
	err := db.Get(&t, db.Rebind(`SELECT tasks.* FROM tasks
		JOIN external_refs ON external_refs.task_id = tasks.id
		WHERE external_refs.source = ? AND external_refs.external_id = ?`),
		source, externalID)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func GetExternalRefs(db *sqlx.DB, taskID string) ([]ExternalRef, error) {
	var refs []ExternalRef
	err := db.Select(&refs, db.Rebind(`SELECT * FROM external_refs WHERE task_id = ? ORDER BY source, external_id`), taskID)
	return refs, err
}
