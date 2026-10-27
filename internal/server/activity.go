package server

import (
	"context"
	"encoding/json"

	"github.com/jrdn/tt/internal/auth"
	"github.com/jrdn/tt/internal/task"
)

// Activity reads the project's audit log: unlike a local database, the
// server records what each change was (a new status, an assignee, a
// relation), so the feed can say what happened rather than only that
// something did. Timestamps are rendered in SQL as RFC 3339 so the JSON is
// the same shape a local database returns.
func (s *authzStore) Activity(ctx context.Context, limit int) ([]task.Activity, error) {
	if _, err := s.allow(ctx, auth.ActTaskRead); err != nil {
		return nil, err
	}
	type eventRow struct {
		TaskID string  `db:"task_id"`
		Actor  *string `db:"actor_label"`
		Action string  `db:"action"`
		Detail *string `db:"detail"`
		At     string  `db:"at"`
	}
	var rows []eventRow
	err := s.dir.db.SelectContext(ctx, &rows, `SELECT task_id, actor_label, action,
			detail::text AS detail,
			to_char(at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS at
		FROM task_events WHERE project_id = $1
		ORDER BY task_events.at DESC, id DESC LIMIT $2`, s.project.ID, limit)
	if err != nil {
		return nil, err
	}

	titles := map[string]string{}
	comments := map[string]map[string]task.Comment{}
	out := make([]task.Activity, 0, len(rows))
	for _, r := range rows {
		a := task.Activity{TaskID: r.TaskID, Actor: r.Actor, At: r.At}
		var detail map[string]any
		if r.Detail != nil && *r.Detail != "" && *r.Detail != "null" {
			if err := json.Unmarshal([]byte(*r.Detail), &detail); err != nil {
				detail = nil
			}
		}
		switch r.Action {
		case "create":
			a.Kind = task.ActivityCreated
		case "comment":
			a.Kind = task.ActivityComment
		case "claim":
			a.Kind = task.ActivityAssigned
		case "relate", "unrelate":
			a.Kind = task.ActivityRelated
		case "import":
			a.Kind = task.ActivityImported
		case "update":
			if _, ok := detail["status"]; ok {
				a.Kind = task.ActivityStatus
			} else {
				a.Kind = task.ActivityUpdated
			}
		default:
			a.Kind = task.ActivityUpdated
		}
		a.Detail = detail

		title, seen := titles[r.TaskID]
		if !seen {
			if t, err := s.inner.Get(ctx, r.TaskID); err == nil {
				title = t.Title
			}
			titles[r.TaskID] = title
		}
		a.TaskTitle = title

		// A comment event carries the comment's ID; the body lives with the
		// task, so fetch the task's comments once and reuse them.
		if a.Kind == task.ActivityComment {
			if id, ok := detail["comment_id"].(string); ok && id != "" {
				if _, loaded := comments[r.TaskID]; !loaded {
					byID := map[string]task.Comment{}
					if cs, err := s.inner.GetComments(ctx, r.TaskID); err == nil {
						for _, c := range cs {
							byID[c.ID] = c
						}
					}
					comments[r.TaskID] = byID
				}
				if c, ok := comments[r.TaskID][id]; ok {
					a.Body = c.Body
				}
			}
		}
		out = append(out, a)
	}
	return out, nil
}
