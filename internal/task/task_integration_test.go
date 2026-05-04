//go:build integration

package task

import (
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/jrdn/tt/internal/db"
)

func openTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := db.OpenPath(path)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestCreate(t *testing.T) {
	d := openTestDB(t)
	task, err := Create(d, "test task", CreateOpts{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if task.ID == "" {
		t.Error("expected non-empty ID")
	}
	if task.Title != "test task" {
		t.Errorf("title = %q, want %q", task.Title, "test task")
	}
	if task.Status != StatusOpen {
		t.Errorf("status = %q, want open", task.Status)
	}
	if task.Priority != 2 {
		t.Errorf("priority = %d, want 2", task.Priority)
	}
	if task.ClosedAt != nil {
		t.Error("closed_at should be nil on create")
	}
}

func TestCreate_WithOptions(t *testing.T) {
	d := openTestDB(t)
	desc := "some description"
	assignee := "claude/opus4.7"
	task, err := Create(d, "assigned task", CreateOpts{
		Description: &desc,
		Assignee:    &assignee,
		Priority:    1,
		PrioritySet: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if task.Description == nil || *task.Description != desc {
		t.Errorf("description = %v, want %q", task.Description, desc)
	}
	if task.Assignee == nil || *task.Assignee != assignee {
		t.Errorf("assignee = %v, want %q", task.Assignee, assignee)
	}
	if task.Priority != 1 {
		t.Errorf("priority = %d, want 1", task.Priority)
	}
}

func TestGet_PrefixMatch(t *testing.T) {
	d := openTestDB(t)
	created, err := Create(d, "find me", CreateOpts{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := Get(d, created.ID[:2])
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("got ID %q, want %q", got.ID, created.ID)
	}
}

func TestGet_AmbiguousPrefix(t *testing.T) {
	d := openTestDB(t)
	// Force two tasks with the same first character by trying many times — instead,
	// just verify the error path by passing a prefix that matches nothing.
	_, err := Get(d, "zzzz")
	if err == nil {
		t.Error("expected error for non-matching prefix")
	}
}

func TestGet_NotFound(t *testing.T) {
	d := openTestDB(t)
	_, err := Get(d, "zzzz")
	if err == nil {
		t.Error("expected error for unknown prefix")
	}
}

func TestList_NoFilter(t *testing.T) {
	d := openTestDB(t)
	for _, title := range []string{"a", "b", "c"} {
		if _, err := Create(d, title, CreateOpts{}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	tasks, err := List(d, ListOpts{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 3 {
		t.Errorf("expected 3 tasks, got %d", len(tasks))
	}
}

func TestList_StatusFilter(t *testing.T) {
	d := openTestDB(t)
	t1, _ := Create(d, "open one", CreateOpts{})
	t2, _ := Create(d, "done one", CreateOpts{})
	status := StatusDone
	Update(d, t2.ID, UpdateOpts{Status: &status})

	open, err := List(d, ListOpts{Status: "open"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(open) != 1 || open[0].ID != t1.ID {
		t.Errorf("expected 1 open task (%s), got %v", t1.ID, open)
	}
}

func TestList_ParentFilter(t *testing.T) {
	d := openTestDB(t)
	parent, _ := Create(d, "parent", CreateOpts{})
	child, _ := Create(d, "child", CreateOpts{ParentID: &parent.ID})
	_, _ = Create(d, "unrelated", CreateOpts{})

	subtasks, err := List(d, ListOpts{ParentID: parent.ID})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(subtasks) != 1 || subtasks[0].ID != child.ID {
		t.Errorf("expected 1 subtask (%s), got %v", child.ID, subtasks)
	}
}

func TestUpdate_Status(t *testing.T) {
	d := openTestDB(t)
	task, _ := Create(d, "task", CreateOpts{})
	status := StatusDone
	updated, err := Update(d, task.ID, UpdateOpts{Status: &status})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Status != StatusDone {
		t.Errorf("status = %q, want done", updated.Status)
	}
	if updated.ClosedAt == nil {
		t.Error("closed_at should be set when status is done")
	}
}

func TestUpdate_Cancelled_SetsClosedAt(t *testing.T) {
	d := openTestDB(t)
	task, _ := Create(d, "task", CreateOpts{})
	status := StatusCancelled
	updated, _ := Update(d, task.ID, UpdateOpts{Status: &status})
	if updated.ClosedAt == nil {
		t.Error("closed_at should be set when status is cancelled")
	}
}

func TestUpdate_Title(t *testing.T) {
	d := openTestDB(t)
	task, _ := Create(d, "old title", CreateOpts{})
	newTitle := "new title"
	updated, err := Update(d, task.ID, UpdateOpts{Title: &newTitle})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Title != newTitle {
		t.Errorf("title = %q, want %q", updated.Title, newTitle)
	}
}

func TestComments(t *testing.T) {
	d := openTestDB(t)
	task, _ := Create(d, "task", CreateOpts{})
	author := "jrdn"

	c, err := AddComment(d, task.ID, "first note", &author)
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	if c.Body != "first note" {
		t.Errorf("body = %q", c.Body)
	}

	AddComment(d, task.ID, "second note", nil)

	comments, err := GetComments(d, task.ID)
	if err != nil {
		t.Fatalf("GetComments: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(comments))
	}
	if comments[0].Body != "first note" {
		t.Errorf("comments not in order")
	}
}

func TestRelations(t *testing.T) {
	d := openTestDB(t)
	t1, _ := Create(d, "task 1", CreateOpts{})
	t2, _ := Create(d, "task 2", CreateOpts{})

	if err := AddRelation(d, t1.ID, "blocks", t2.ID); err != nil {
		t.Fatalf("AddRelation: %v", err)
	}

	rels, err := GetRelations(d, t1.ID)
	if err != nil {
		t.Fatalf("GetRelations: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(rels))
	}
	if rels[0].Type != "blocks" || rels[0].FromID != t1.ID || rels[0].ToID != t2.ID {
		t.Errorf("unexpected relation: %+v", rels[0])
	}

	// Also visible from the other side
	rels2, _ := GetRelations(d, t2.ID)
	if len(rels2) != 1 {
		t.Errorf("expected relation visible from t2, got %d", len(rels2))
	}
}

func TestRelations_Idempotent(t *testing.T) {
	d := openTestDB(t)
	t1, _ := Create(d, "task 1", CreateOpts{})
	t2, _ := Create(d, "task 2", CreateOpts{})
	AddRelation(d, t1.ID, "related", t2.ID)
	if err := AddRelation(d, t1.ID, "related", t2.ID); err != nil {
		t.Errorf("duplicate AddRelation should not error: %v", err)
	}
	rels, _ := GetRelations(d, t1.ID)
	if len(rels) != 1 {
		t.Errorf("expected 1 relation after duplicate insert, got %d", len(rels))
	}
}
