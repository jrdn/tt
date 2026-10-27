package task

import (
	"context"
	"sort"
	"time"

	"github.com/jmoiron/sqlx"
)

// Kinds of entry in a project's activity feed.
const (
	ActivityCreated  = "created"
	ActivityComment  = "comment"
	ActivityStatus   = "status"
	ActivityUpdated  = "updated"
	ActivityAssigned = "assigned"
	ActivityRelated  = "related"
	ActivityImported = "imported"
)

// Activity is one entry in a project's recent history: a comment, a new
// ticket, or a change to one. At is RFC 3339 in UTC.
type Activity struct {
	Kind      string         `json:"kind"`
	TaskID    string         `json:"task_id"`
	TaskTitle string         `json:"task_title"`
	Actor     *string        `json:"actor,omitempty"`
	Body      string         `json:"body,omitempty"`
	Detail    map[string]any `json:"detail,omitempty"`
	At        string         `json:"at"`
}

// ActivityReader is implemented by stores that can report recent activity.
// The server store reads its audit log, which knows what each change was; a
// local database keeps no history, so it derives the feed from what the task
// and comment tables still carry (see DerivedActivity).
type ActivityReader interface {
	Activity(ctx context.Context, limit int) ([]Activity, error)
}

// Activity of a local database: every recent comment, plus the creation and
// last update of each ticket. Without a change log an update can only be
// reported as "updated", not as the field that moved.
func (s *SQLStore) Activity(ctx context.Context, limit int) ([]Activity, error) {
	return DerivedActivity(s.db, limit)
}

// DerivedActivity builds an activity feed from the task and comment tables.
func DerivedActivity(db *sqlx.DB, limit int) ([]Activity, error) {
	type commentRow struct {
		TaskID string  `db:"task_id"`
		Title  string  `db:"title"`
		Author *string `db:"author"`
		Body   string  `db:"body"`
		At     string  `db:"at"`
	}
	var comments []commentRow
	err := db.Select(&comments, db.Rebind(`SELECT c.task_id, t.title, c.author, c.body, c.created_at AS at
		FROM comments c JOIN tasks t ON t.id = c.task_id
		ORDER BY c.created_at DESC LIMIT ?`), limit)
	if err != nil {
		return nil, err
	}

	type taskRow struct {
		ID        string  `db:"id"`
		Title     string  `db:"title"`
		CreatedBy *string `db:"created_by"`
		CreatedAt string  `db:"created_at"`
		UpdatedAt string  `db:"updated_at"`
	}
	var tasks []taskRow
	err = db.Select(&tasks, db.Rebind(`SELECT id, title, created_by, created_at, updated_at
		FROM tasks ORDER BY updated_at DESC LIMIT ?`), limit)
	if err != nil {
		return nil, err
	}

	out := make([]Activity, 0, len(comments)+len(tasks)*2)
	for _, c := range comments {
		out = append(out, Activity{
			Kind:      ActivityComment,
			TaskID:    c.TaskID,
			TaskTitle: c.Title,
			Actor:     c.Author,
			Body:      c.Body,
			At:        c.At,
		})
	}
	for _, t := range tasks {
		out = append(out, Activity{
			Kind:      ActivityCreated,
			TaskID:    t.ID,
			TaskTitle: t.Title,
			Actor:     t.CreatedBy,
			At:        t.CreatedAt,
		})
		if t.UpdatedAt != t.CreatedAt {
			out = append(out, Activity{
				Kind:      ActivityUpdated,
				TaskID:    t.ID,
				TaskTitle: t.Title,
				At:        t.UpdatedAt,
			})
		}
	}

	SortActivity(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// SortActivity orders entries newest first, breaking ties by task and kind so
// repeated renders keep a stable order.
func SortActivity(acts []Activity) {
	sort.SliceStable(acts, func(i, j int) bool {
		ti, tj := ParseActivityTime(acts[i].At), ParseActivityTime(acts[j].At)
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		if acts[i].TaskID != acts[j].TaskID {
			return acts[i].TaskID < acts[j].TaskID
		}
		return acts[i].Kind < acts[j].Kind
	})
}

// ParseActivityTime reads the timestamp formats the two backends hand back:
// RFC 3339 from a local SQLite database, and the text rendering of a Postgres
// timestamp. Unparseable input sorts to the end rather than failing a feed.
func ParseActivityTime(s string) time.Time {
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07",
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05-07",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
