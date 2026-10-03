package client

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jrdn/tt/internal/task"
)

// Store implements task.Store against one project on a tt server.
type Store struct {
	c    *Client
	base string // /api/v1/projects/{project}
}

var _ task.Store = (*Store)(nil)

func NewStore(c *Client, project string) *Store {
	return &Store{c: c, base: "/api/v1/projects/" + url.PathEscape(project)}
}

type taskDetail struct {
	Task      *task.Task         `json:"task"`
	Subtasks  []task.Task        `json:"subtasks"`
	Relations []task.Relation    `json:"relations"`
	Comments  []task.Comment     `json:"comments"`
	Refs      []task.ExternalRef `json:"external_refs"`
}

func (s *Store) detail(ctx context.Context, id string) (*taskDetail, error) {
	var d taskDetail
	err := s.c.Do(ctx, "GET", s.base+"/tasks/"+url.PathEscape(id), nil, &d)
	return &d, err
}

func (s *Store) Create(ctx context.Context, title string, opts task.CreateOpts) (*task.Task, error) {
	body := map[string]any{
		"title":       title,
		"description": opts.Description,
		"assignee":    opts.Assignee,
		"parent_id":   opts.ParentID,
		"due_date":    opts.DueDate,
		"created_by":  opts.CreatedBy,
	}
	if opts.PrioritySet {
		body["priority"] = opts.Priority
	}
	var t task.Task
	err := s.c.Do(ctx, "POST", s.base+"/tasks", body, &t)
	return &t, err
}

func (s *Store) Get(ctx context.Context, prefix string) (*task.Task, error) {
	d, err := s.detail(ctx, prefix)
	if err != nil {
		return nil, err
	}
	return d.Task, nil
}

func (s *Store) List(ctx context.Context, opts task.ListOpts) ([]task.Task, error) {
	q := url.Values{}
	if opts.Status != "" {
		q.Set("status", opts.Status)
	}
	if opts.Assignee != "" {
		q.Set("assignee", opts.Assignee)
	}
	if opts.ParentID != "" {
		q.Set("parent", opts.ParentID)
	}
	if opts.All {
		q.Set("all", "1")
	}
	if opts.Ready {
		q.Set("ready", "1")
	}
	var resp struct {
		Tasks []task.Task `json:"tasks"`
	}
	err := s.c.Do(ctx, "GET", s.base+"/tasks?"+q.Encode(), nil, &resp)
	return resp.Tasks, err
}

func (s *Store) Recent(ctx context.Context, limit int) ([]task.Task, error) {
	var resp struct {
		Tasks []task.Task `json:"tasks"`
	}
	err := s.c.Do(ctx, "GET", s.base+"/tasks/recent?limit="+strconv.Itoa(limit), nil, &resp)
	return resp.Tasks, err
}

func (s *Store) Update(ctx context.Context, prefix string, opts task.UpdateOpts) (*task.Task, error) {
	body := map[string]any{}
	if opts.Status != nil {
		body["status"] = *opts.Status
	}
	if opts.Title != nil {
		body["title"] = *opts.Title
	}
	if opts.Description != nil {
		body["description"] = *opts.Description
	}
	if opts.Priority != nil {
		body["priority"] = *opts.Priority
	}
	if opts.Assignee != nil {
		body["assignee"] = *opts.Assignee
	}
	if opts.DueDate != nil {
		body["due_date"] = *opts.DueDate
	}
	if opts.ParentID != nil {
		body["parent_id"] = *opts.ParentID
	}
	var t task.Task
	err := s.c.Do(ctx, "PATCH", s.base+"/tasks/"+url.PathEscape(prefix), body, &t)
	return &t, err
}

// Save sends every editable field; empty strings clear optional ones.
func (s *Store) Save(ctx context.Context, t *task.Task) error {
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	body := map[string]any{
		"status":      t.Status,
		"title":       t.Title,
		"description": str(t.Description),
		"priority":    t.Priority,
		"assignee":    str(t.Assignee),
		"due_date":    str(t.DueDate.StringPtr()),
		"parent_id":   str(t.ParentID),
	}
	return s.c.Do(ctx, "PATCH", s.base+"/tasks/"+url.PathEscape(t.ID), body, nil)
}

func (s *Store) Search(ctx context.Context, query string) ([]task.Task, error) {
	var resp struct {
		Tasks []task.Task `json:"tasks"`
	}
	err := s.c.Do(ctx, "GET", s.base+"/search?q="+url.QueryEscape(query), nil, &resp)
	return resp.Tasks, err
}

func (s *Store) Next(ctx context.Context, handle string) (*task.Task, error) {
	var t *task.Task
	err := s.c.Do(ctx, "POST", s.base+"/next", map[string]string{"handle": handle}, &t)
	return t, err
}

func (s *Store) AddComment(ctx context.Context, taskPrefix, body string, author *string) (*task.Comment, error) {
	var cm task.Comment
	err := s.c.Do(ctx, "POST", s.base+"/tasks/"+url.PathEscape(taskPrefix)+"/comments",
		map[string]any{"body": body, "author": author}, &cm)
	return &cm, err
}

func (s *Store) GetComments(ctx context.Context, taskID string) ([]task.Comment, error) {
	d, err := s.detail(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return d.Comments, nil
}

func (s *Store) AddRelation(ctx context.Context, fromPrefix, relType, toPrefix string) error {
	return s.c.Do(ctx, "POST", s.base+"/tasks/"+url.PathEscape(fromPrefix)+"/relations",
		map[string]string{"type": relType, "target_id": toPrefix}, nil)
}

func (s *Store) RemoveRelation(ctx context.Context, fromPrefix, relType, toPrefix string) error {
	return s.c.Do(ctx, "DELETE", s.base+"/tasks/"+url.PathEscape(fromPrefix)+"/relations/"+
		url.PathEscape(relType)+"/"+url.PathEscape(toPrefix), nil, nil)
}

func (s *Store) GetRelations(ctx context.Context, taskID string) ([]task.Relation, error) {
	d, err := s.detail(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return d.Relations, nil
}

func (s *Store) UpsertExternalRef(ctx context.Context, taskID, source, externalID string, u *string) error {
	return s.c.Do(ctx, "PUT", s.base+"/external-refs/"+url.PathEscape(source)+"/"+url.PathEscape(externalID),
		map[string]any{"task_id": taskID, "url": u}, nil)
}

func (s *Store) FindByExternalRef(ctx context.Context, source, externalID string) (*task.Task, error) {
	var t task.Task
	err := s.c.Do(ctx, "GET", s.base+"/external-refs/"+url.PathEscape(source)+"/"+url.PathEscape(externalID), nil, &t)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) GetExternalRefs(ctx context.Context, taskID string) ([]task.ExternalRef, error) {
	d, err := s.detail(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return d.Refs, nil
}

func (s *Store) Close() error { return nil }

var _ task.Subscriber = (*Store)(nil)

// Subscribe streams the project's change events, reconnecting (with a
// fresh token when needed) until ctx ends. After each reconnect it emits an
// empty event, since changes during the gap weren't seen.
func (s *Store) Subscribe(ctx context.Context) (<-chan task.Event, error) {
	resp, err := s.c.Stream(ctx, s.base+"/events")
	if err != nil {
		return nil, err
	}
	ch := make(chan task.Event, 16)
	go func() {
		defer close(ch)
		backoff := time.Second
		for {
			readEvents(resp.Body, ch)
			resp.Body.Close()
			for {
				select {
				case <-ctx.Done():
					return
				case <-time.After(backoff):
				}
				if resp, err = s.c.Stream(ctx, s.base+"/events"); err == nil {
					break
				}
				backoff = min(backoff*2, 30*time.Second)
			}
			backoff = time.Second
			select {
			case ch <- task.Event{}:
			default:
			}
		}
	}()
	return ch, nil
}

// readEvents forwards "data:" payloads from an SSE stream until it ends.
func readEvents(r io.Reader, ch chan<- task.Event) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		var ev task.Event
		if json.Unmarshal([]byte(data), &ev) == nil {
			select {
			case ch <- ev:
			default:
			}
		}
	}
}
