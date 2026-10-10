package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jrdn/tt/internal/api"
	"github.com/jrdn/tt/internal/auth"
	"github.com/jrdn/tt/internal/task"
)

// authzStore wraps a project's task store and checks the request's claims
// before every operation, so every transport enforces the same rules. It
// also sets authorship from the claims and records audit events.
type authzStore struct {
	inner   task.Store
	project *Project
	dir     *Directory
	hub     *hub
}

var (
	_ task.Store      = (*authzStore)(nil)
	_ task.Subscriber = (*authzStore)(nil)
)

// liveRecheck is how often an open event stream re-validates its token, so
// revocation reaches long-lived streams.
var liveRecheck = 30 * time.Second

// Subscribe streams the project's change events to a reader. The stream
// ends when the caller's token expires or is revoked.
func (s *authzStore) Subscribe(ctx context.Context) (<-chan task.Event, error) {
	c, err := s.allow(ctx, auth.ActTaskRead)
	if err != nil {
		return nil, err
	}
	in, cancel := s.hub.subscribe(s.project.ID)
	out := make(chan task.Event, 16)
	go func() {
		defer close(out)
		defer cancel()
		recheck := time.NewTicker(liveRecheck)
		defer recheck.Stop()
		var expired <-chan time.Time
		if c.ExpiresAt != nil {
			expired = time.After(time.Until(c.ExpiresAt.Time))
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-expired:
				return
			case <-recheck.C:
				if s.dir.CheckLive(ctx, c) != nil {
					return
				}
			case ev := <-in:
				select {
				case out <- ev:
				default:
				}
			}
		}
	}()
	return out, nil
}

// allow checks that the caller holds every listed action in this project.
func (s *authzStore) allow(ctx context.Context, actions ...auth.Action) (*auth.Claims, error) {
	c := auth.FromContext(ctx)
	if c == nil {
		return nil, fmt.Errorf("%w: not authenticated", api.ErrForbidden)
	}
	for _, a := range actions {
		if !c.Can(s.project.Slug, a) {
			return nil, fmt.Errorf("%w: %s requires %s on project %s", api.ErrForbidden, c.Handle, a, s.project.Slug)
		}
	}
	return c, nil
}

// inScope checks that task id falls under every task-scope root on the
// claims (the root itself or any descendant).
func (s *authzStore) inScope(ctx context.Context, c *auth.Claims, id string) error {
	for _, root := range c.TaskScope {
		cur := id
		found := false
		for depth := 0; depth < 100 && cur != ""; depth++ {
			if strings.EqualFold(cur, root) {
				found = true
				break
			}
			t, err := s.inner.Get(ctx, cur)
			if err != nil {
				return err
			}
			if t.ParentID == nil {
				break
			}
			cur = *t.ParentID
		}
		if !found {
			return fmt.Errorf("%w: task %s is outside this key's task scope", api.ErrForbidden, id)
		}
	}
	return nil
}

// authorHandle is the name stamped on tasks and comments the caller writes.
func authorHandle(c *auth.Claims) string {
	if c.Label != "" {
		return c.Handle + " (" + c.Label + ")"
	}
	return c.Handle
}

func (s *authzStore) audit(ctx context.Context, c *auth.Claims, taskID, action string, detail map[string]any) {
	if err := s.dir.RecordEvent(ctx, s.project, c, taskID, action, detail); err != nil {
		slog.ErrorContext(ctx, "audit write failed", "action", action, "project", s.project.Slug, "task", taskID, "err", err)
	}
}

// updateActions returns the actions needed to apply these changes.
func updateActions(c *auth.Claims, opts task.UpdateOpts) []auth.Action {
	acts := []auth.Action{}
	if opts.Status != nil {
		acts = append(acts, auth.ActTaskStatus)
	}
	if opts.Assignee != nil {
		if *opts.Assignee == c.Handle {
			acts = append(acts, auth.ActTaskClaim)
		} else {
			acts = append(acts, auth.ActTaskAssign)
		}
	}
	if opts.Title != nil || opts.Description != nil || opts.Priority != nil || opts.DueDate != nil || opts.ParentID != nil {
		acts = append(acts, auth.ActTaskEdit)
	}
	if len(acts) == 0 {
		acts = append(acts, auth.ActTaskEdit)
	}
	return acts
}

// checkWrite authorizes a change to an existing task. extra lists actions
// needed beyond what opts implies.
func (s *authzStore) checkWrite(ctx context.Context, id string, opts task.UpdateOpts, extra ...auth.Action) (*auth.Claims, *task.Task, error) {
	c := auth.FromContext(ctx)
	if c == nil {
		return nil, nil, fmt.Errorf("%w: not authenticated", api.ErrForbidden)
	}
	if _, err := s.allow(ctx, append(updateActions(c, opts), extra...)...); err != nil {
		return nil, nil, err
	}
	t, err := s.inner.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if err := s.inScope(ctx, c, t.ID); err != nil {
		return nil, nil, err
	}
	if opts.ParentID != nil {
		if err := s.inScope(ctx, c, *opts.ParentID); err != nil {
			return nil, nil, err
		}
	}
	return c, t, nil
}

func (s *authzStore) Create(ctx context.Context, title string, opts task.CreateOpts) (*task.Task, error) {
	acts := []auth.Action{auth.ActTaskCreate}
	c := auth.FromContext(ctx)
	if c != nil && opts.Assignee != nil && *opts.Assignee != c.Handle {
		acts = append(acts, auth.ActTaskAssign)
	}
	c, err := s.allow(ctx, acts...)
	if err != nil {
		return nil, err
	}
	if len(c.TaskScope) > 0 {
		if opts.ParentID == nil {
			return nil, fmt.Errorf("%w: task-scoped keys can only create subtasks", api.ErrForbidden)
		}
		if err := s.inScope(ctx, c, *opts.ParentID); err != nil {
			return nil, err
		}
	}
	by := authorHandle(c)
	opts.CreatedBy = &by
	t, err := s.inner.Create(ctx, title, opts)
	if err == nil {
		s.audit(ctx, c, t.ID, "create", map[string]any{"title": title})
	}
	return t, err
}

func (s *authzStore) Get(ctx context.Context, prefix string) (*task.Task, error) {
	if _, err := s.allow(ctx, auth.ActTaskRead); err != nil {
		return nil, err
	}
	return s.inner.Get(ctx, prefix)
}

func (s *authzStore) List(ctx context.Context, opts task.ListOpts) ([]task.Task, error) {
	if _, err := s.allow(ctx, auth.ActTaskRead); err != nil {
		return nil, err
	}
	return s.inner.List(ctx, opts)
}

func (s *authzStore) Recent(ctx context.Context, limit int) ([]task.Task, error) {
	if _, err := s.allow(ctx, auth.ActTaskRead); err != nil {
		return nil, err
	}
	return s.inner.Recent(ctx, limit)
}

func (s *authzStore) Search(ctx context.Context, query string) ([]task.Task, error) {
	if _, err := s.allow(ctx, auth.ActTaskRead); err != nil {
		return nil, err
	}
	return s.inner.Search(ctx, query)
}

func (s *authzStore) Update(ctx context.Context, prefix string, opts task.UpdateOpts) (*task.Task, error) {
	c, _, err := s.checkWrite(ctx, prefix, opts)
	if err != nil {
		return nil, err
	}
	t, err := s.inner.Update(ctx, prefix, opts)
	if err == nil {
		s.audit(ctx, c, t.ID, "update", updateDetail(opts))
	}
	return t, err
}

// Save writes a whole task (web UI edits, tt edit). The required actions
// come from diffing against the stored task.
func (s *authzStore) Save(ctx context.Context, t *task.Task) error {
	if _, err := s.allow(ctx, auth.ActTaskRead); err != nil {
		return err
	}
	cur, err := s.inner.Get(ctx, t.ID)
	if err != nil {
		return err
	}
	opts := diffTask(cur, t)
	var extra []auth.Action
	detached := cur.ParentID != nil && t.ParentID == nil
	if detached {
		// UpdateOpts can't express removing a parent, so require edit here.
		extra = append(extra, auth.ActTaskEdit)
	}
	c, _, err := s.checkWrite(ctx, t.ID, opts, extra...)
	if err != nil {
		return err
	}
	if detached && len(c.TaskScope) > 0 {
		return fmt.Errorf("%w: task-scoped keys can't move tasks out of their scope", api.ErrForbidden)
	}
	if err := s.inner.Save(ctx, t); err != nil {
		return err
	}
	s.audit(ctx, c, t.ID, "update", updateDetail(opts))
	return nil
}

func (s *authzStore) Next(ctx context.Context, handle string) (*task.Task, error) {
	acts := []auth.Action{auth.ActTaskClaim}
	c := auth.FromContext(ctx)
	if c != nil && handle != c.Handle {
		acts = append(acts, auth.ActTaskAssign)
	}
	c, err := s.allow(ctx, acts...)
	if err != nil {
		return nil, err
	}
	if len(c.TaskScope) > 0 {
		return nil, fmt.Errorf("%w: task-scoped keys can't use next; claim a specific task instead", api.ErrForbidden)
	}
	t, err := s.inner.Next(ctx, handle)
	if err == nil && t != nil {
		s.audit(ctx, c, t.ID, "claim", map[string]any{"assignee": handle})
	}
	return t, err
}

func (s *authzStore) AddComment(ctx context.Context, taskPrefix, body string, author *string) (*task.Comment, error) {
	c, err := s.allow(ctx, auth.ActCommentCreate)
	if err != nil {
		return nil, err
	}
	t, err := s.inner.Get(ctx, taskPrefix)
	if err != nil {
		return nil, err
	}
	if err := s.inScope(ctx, c, t.ID); err != nil {
		return nil, err
	}
	by := authorHandle(c) // the client-supplied author is ignored
	cm, err := s.inner.AddComment(ctx, t.ID, body, &by)
	if err == nil {
		s.audit(ctx, c, t.ID, "comment", map[string]any{"comment_id": cm.ID})
	}
	return cm, err
}

func (s *authzStore) GetComments(ctx context.Context, taskID string) ([]task.Comment, error) {
	if _, err := s.allow(ctx, auth.ActTaskRead); err != nil {
		return nil, err
	}
	return s.inner.GetComments(ctx, taskID)
}

func (s *authzStore) relationWrite(ctx context.Context, fromPrefix, toPrefix string) (*auth.Claims, string, string, error) {
	c, err := s.allow(ctx, auth.ActRelationWrite)
	if err != nil {
		return nil, "", "", err
	}
	from, err := s.inner.Get(ctx, fromPrefix)
	if err != nil {
		return nil, "", "", err
	}
	to, err := s.inner.Get(ctx, toPrefix)
	if err != nil {
		return nil, "", "", err
	}
	for _, id := range []string{from.ID, to.ID} {
		if err := s.inScope(ctx, c, id); err != nil {
			return nil, "", "", err
		}
	}
	return c, from.ID, to.ID, nil
}

func (s *authzStore) AddRelation(ctx context.Context, fromPrefix, relType, toPrefix string) error {
	c, from, to, err := s.relationWrite(ctx, fromPrefix, toPrefix)
	if err != nil {
		return err
	}
	if err := s.inner.AddRelation(ctx, from, relType, to); err != nil {
		return err
	}
	s.audit(ctx, c, from, "relate", map[string]any{"type": relType, "to": to})
	return nil
}

func (s *authzStore) RemoveRelation(ctx context.Context, fromPrefix, relType, toPrefix string) error {
	c, from, to, err := s.relationWrite(ctx, fromPrefix, toPrefix)
	if err != nil {
		return err
	}
	if err := s.inner.RemoveRelation(ctx, from, relType, to); err != nil {
		return err
	}
	s.audit(ctx, c, from, "unrelate", map[string]any{"type": relType, "to": to})
	return nil
}

func (s *authzStore) GetRelations(ctx context.Context, taskID string) ([]task.Relation, error) {
	if _, err := s.allow(ctx, auth.ActTaskRead); err != nil {
		return nil, err
	}
	return s.inner.GetRelations(ctx, taskID)
}

func (s *authzStore) UpsertExternalRef(ctx context.Context, taskID, source, externalID string, url *string) error {
	if _, err := s.allow(ctx, auth.ActImport); err != nil {
		return err
	}
	return s.inner.UpsertExternalRef(ctx, taskID, source, externalID, url)
}

func (s *authzStore) FindByExternalRef(ctx context.Context, source, externalID string) (*task.Task, error) {
	if _, err := s.allow(ctx, auth.ActImport); err != nil {
		return nil, err
	}
	return s.inner.FindByExternalRef(ctx, source, externalID)
}

func (s *authzStore) GetExternalRefs(ctx context.Context, taskID string) ([]task.ExternalRef, error) {
	if _, err := s.allow(ctx, auth.ActTaskRead); err != nil {
		return nil, err
	}
	return s.inner.GetExternalRefs(ctx, taskID)
}

// Close is a no-op: the underlying pool is shared and owned by Directory.
func (s *authzStore) Close() error { return nil }

// diffTask expresses the difference between two versions of a task as
// UpdateOpts, so Save can be authorized like Update.
func diffTask(cur, next *task.Task) task.UpdateOpts {
	var o task.UpdateOpts
	if next.Status != cur.Status {
		o.Status = &next.Status
	}
	if next.Title != cur.Title {
		o.Title = &next.Title
	}
	if !eqPtr(next.Description, cur.Description) {
		o.Description = orEmpty(next.Description)
	}
	if next.Priority != cur.Priority {
		o.Priority = &next.Priority
	}
	if !eqPtr(next.Assignee, cur.Assignee) {
		o.Assignee = orEmpty(next.Assignee)
	}
	if !eqPtr(next.DueDate.StringPtr(), cur.DueDate.StringPtr()) {
		o.DueDate = orEmpty(next.DueDate.StringPtr())
	}
	if !eqPtr(next.ParentID, cur.ParentID) && next.ParentID != nil {
		o.ParentID = next.ParentID
	}
	return o
}

func eqPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func orEmpty(p *string) *string {
	if p == nil {
		s := ""
		return &s
	}
	return p
}

func updateDetail(o task.UpdateOpts) map[string]any {
	d := map[string]any{}
	if o.Status != nil {
		d["status"] = *o.Status
	}
	if o.Title != nil {
		d["title"] = *o.Title
	}
	if o.Description != nil {
		d["description"] = "(changed)"
	}
	if o.Priority != nil {
		d["priority"] = *o.Priority
	}
	if o.Assignee != nil {
		d["assignee"] = *o.Assignee
	}
	if o.DueDate != nil {
		d["due_date"] = *o.DueDate
	}
	if o.ParentID != nil {
		d["parent_id"] = *o.ParentID
	}
	return d
}
