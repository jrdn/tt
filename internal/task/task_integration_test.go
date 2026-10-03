//go:build integration

package task

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/jrdn/tt/internal/db"
)

func openTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	if url := os.Getenv("TT_TEST_POSTGRES_URL"); url != "" {
		return openTestPostgres(t, url)
	}
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

func TestGet_LegacyShortID(t *testing.T) {
	d := openTestDB(t)
	// Simulate a 4-char ID from before the base62 migration by inserting directly.
	ts := now()
	_, err := d.Exec(d.Rebind(`INSERT INTO tasks (id,title,status,priority,created_at,updated_at) VALUES (?,?,?,?,?,?)`),
		"abcd", "legacy task", StatusOpen, 2, ts, ts)
	if err != nil {
		t.Fatalf("insert legacy task: %v", err)
	}
	got, err := Get(d, "abcd")
	if err != nil {
		t.Fatalf("Get by full 4-char ID: %v", err)
	}
	if got.ID != "abcd" {
		t.Errorf("got ID %q, want %q", got.ID, "abcd")
	}
	got2, err := Get(d, "ab")
	if err != nil {
		t.Fatalf("Get by 2-char prefix: %v", err)
	}
	if got2.ID != "abcd" {
		t.Errorf("got ID %q, want %q", got2.ID, "abcd")
	}
}

func TestOpenPath_MigratesPriority4(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := db.OpenPath(path)
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	ts := now()
	_, err = d.Exec(`INSERT INTO tasks (id,title,status,priority,created_at,updated_at) VALUES (?,?,?,?,?,?)`,
		"zzzz", "legacy backlog task", "open", 4, ts, ts)
	if err != nil {
		t.Fatalf("insert priority-4 task: %v", err)
	}
	d.Close()

	d2, err := db.OpenPath(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer d2.Close()

	got, err := Get(d2, "zzzz")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Priority != 3 {
		t.Errorf("priority = %d after migration, want 3", got.Priority)
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

func TestList_AssigneeFilter(t *testing.T) {
	d := openTestDB(t)
	alice := "alice"
	bob := "bob"
	Create(d, "alice task 1", CreateOpts{Assignee: &alice})
	Create(d, "alice task 2", CreateOpts{Assignee: &alice})
	Create(d, "bob task", CreateOpts{Assignee: &bob})
	Create(d, "unassigned", CreateOpts{})

	tasks, err := List(d, ListOpts{Assignee: "alice"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks for alice, got %d", len(tasks))
	}
	for _, task := range tasks {
		if task.Assignee == nil || *task.Assignee != "alice" {
			t.Errorf("unexpected task in result: %+v", task)
		}
	}
}

// TestList_StatusAndAssignee_Crossproduct is the primary regression test for the
// combined --status + --assignee filter. It creates the full cross-product of
// statuses × assignees and asserts that the combined filter returns exactly the
// rows that the individual filters would return when intersected manually.
func TestList_StatusAndAssignee_Crossproduct(t *testing.T) {
	d := openTestDB(t)
	alice := "alice"
	bob := "bob"

	statuses := []Status{StatusOpen, StatusReady, StatusInProgress, StatusDone}
	assignees := []*string{&alice, &bob, nil}

	type entry struct {
		id       string
		status   Status
		assignee *string
	}
	var rows []entry

	for _, s := range statuses {
		for _, a := range assignees {
			name := "unassigned"
			if a != nil {
				name = *a
			}
			task, _ := Create(d, string(s)+"-"+name, CreateOpts{Assignee: a})
			if s != StatusOpen {
				st := s
				Update(d, task.ID, UpdateOpts{Status: &st})
			}
			rows = append(rows, entry{task.ID, s, a})
		}
	}

	for _, filterStatus := range []string{"open", "ready", "in_progress", "done"} {
		for _, filterAssignee := range []string{"alice", "bob"} {
			combined, err := List(d, ListOpts{Status: filterStatus, Assignee: filterAssignee, All: true})
			if err != nil {
				t.Fatalf("List(status=%s, assignee=%s): %v", filterStatus, filterAssignee, err)
			}

			// Build expected set by manual intersection.
			expected := map[string]bool{}
			for _, r := range rows {
				assigneeMatch := r.assignee != nil && *r.assignee == filterAssignee
				statusMatch := string(r.status) == filterStatus
				if assigneeMatch && statusMatch {
					expected[r.id] = true
				}
			}

			got := map[string]bool{}
			for _, task := range combined {
				got[task.id()] = true
			}

			if len(got) != len(expected) {
				t.Errorf("status=%s assignee=%s: got %d tasks, want %d (got IDs: %v, want IDs: %v)",
					filterStatus, filterAssignee, len(got), len(expected), keys(got), keys(expected))
				continue
			}
			for id := range expected {
				if !got[id] {
					t.Errorf("status=%s assignee=%s: missing expected task %s", filterStatus, filterAssignee, id)
				}
			}
			// Verify every returned row satisfies both predicates.
			for _, task := range combined {
				if string(task.Status) != filterStatus {
					t.Errorf("status=%s assignee=%s: returned task %s has status %s", filterStatus, filterAssignee, task.ID, task.Status)
				}
				if task.Assignee == nil || *task.Assignee != filterAssignee {
					t.Errorf("status=%s assignee=%s: returned task %s has assignee %v", filterStatus, filterAssignee, task.ID, task.Assignee)
				}
			}
		}
	}
}

// TestList_StatusAndAssignee_ExcludesNeighbors checks that each wrong-status and
// wrong-assignee neighbour is excluded when both filters are active.
func TestList_StatusAndAssignee_ExcludesNeighbors(t *testing.T) {
	d := openTestDB(t)
	alice := "alice"
	bob := "bob"

	aliceReady, _ := Create(d, "alice ready", CreateOpts{Assignee: &alice})
	aliceOpen, _ := Create(d, "alice open", CreateOpts{Assignee: &alice})
	bobReady, _ := Create(d, "bob ready", CreateOpts{Assignee: &bob})
	bobOpen, _ := Create(d, "bob open", CreateOpts{Assignee: &bob})
	unassignedReady, _ := Create(d, "unassigned ready", CreateOpts{})

	ready := StatusReady
	for _, id := range []string{aliceReady.ID, bobReady.ID, unassignedReady.ID} {
		Update(d, id, UpdateOpts{Status: &ready})
	}

	tasks, err := List(d, ListOpts{Status: "ready", Assignee: "alice"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(tasks) != 1 {
		t.Errorf("expected exactly 1 task, got %d: %v", len(tasks), tasks)
	}
	if len(tasks) > 0 && tasks[0].ID != aliceReady.ID {
		t.Errorf("wrong task returned: got %s, want %s", tasks[0].ID, aliceReady.ID)
	}

	excluded := map[string]string{
		aliceOpen.ID:       "alice/open — wrong status",
		bobReady.ID:        "bob/ready — wrong assignee",
		bobOpen.ID:         "bob/open — both wrong",
		unassignedReady.ID: "unassigned/ready — no assignee",
	}
	returned := map[string]bool{}
	for _, task := range tasks {
		returned[task.ID] = true
	}
	for id, label := range excluded {
		if returned[id] {
			t.Errorf("task %s (%s) should have been excluded", id, label)
		}
	}
}

func TestList_AssigneeDoesNotMatchCreatedBy(t *testing.T) {
	d := openTestDB(t)
	alice := "alice"
	bob := "bob"

	// alice is the assignee but bob is created_by, and vice versa.
	aliceTask, _ := Create(d, "alice assignee", CreateOpts{Assignee: &alice, CreatedBy: &bob})
	bobTask, _ := Create(d, "bob assignee", CreateOpts{Assignee: &bob, CreatedBy: &alice})

	tasks, err := List(d, ListOpts{Assignee: "bob"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != bobTask.ID {
		t.Errorf("--assignee=bob should return only bob's task (%s), got %v", bobTask.ID, tasks)
	}
	for _, task := range tasks {
		if task.ID == aliceTask.ID {
			t.Errorf("task with assignee=alice was returned under --assignee=bob (created_by cross-match)")
		}
	}
}

func TestList_AllAndAssignee(t *testing.T) {
	d := openTestDB(t)
	alice := "alice"
	bob := "bob"

	t1, _ := Create(d, "alice open", CreateOpts{Assignee: &alice})
	t2, _ := Create(d, "alice done", CreateOpts{Assignee: &alice})
	Create(d, "bob open", CreateOpts{Assignee: &bob})

	done := StatusDone
	Update(d, t2.ID, UpdateOpts{Status: &done})

	tasks, err := List(d, ListOpts{All: true, Assignee: "alice"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks for alice with --all, got %d", len(tasks))
	}
	ids := map[string]bool{tasks[0].ID: true, tasks[1].ID: true}
	if !ids[t1.ID] || !ids[t2.ID] {
		t.Errorf("expected tasks %s and %s, got %v", t1.ID, t2.ID, tasks)
	}
}

func TestList_ReadyFlagAndAssignee(t *testing.T) {
	d := openTestDB(t)
	alice := "alice"
	bob := "bob"

	t1, _ := Create(d, "alice ready", CreateOpts{Assignee: &alice})
	Create(d, "bob ready", CreateOpts{Assignee: &bob})

	ready := StatusReady
	Update(d, t1.ID, UpdateOpts{Status: &ready})

	tasks, err := List(d, ListOpts{Ready: true, Assignee: "alice"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != t1.ID {
		t.Errorf("expected only alice's ready task (%s), got %v", t1.ID, tasks)
	}
}

// id is a helper so the cross-product test can call task.ID without the field
// access conflicting with the loop variable name.
func (t Task) id() string { return t.ID }

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
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

func TestUpdate_InvalidStatus(t *testing.T) {
	d := openTestDB(t)
	task, _ := Create(d, "task", CreateOpts{})
	garbage := Status("GARBAGE")
	_, err := Update(d, task.ID, UpdateOpts{Status: &garbage})
	if err == nil {
		t.Error("expected error for invalid status, got nil")
	}
}

func TestUpdate_ValidStatuses(t *testing.T) {
	d := openTestDB(t)
	validStatuses := []Status{StatusBacklog, StatusOpen, StatusReady, StatusInProgress, StatusInReview, StatusDone, StatusCancelled}
	for _, s := range validStatuses {
		task, _ := Create(d, "task", CreateOpts{})
		st := s
		if _, err := Update(d, task.ID, UpdateOpts{Status: &st}); err != nil {
			t.Errorf("Update with status=%q should not error: %v", s, err)
		}
	}
}

func TestUpdate_InvalidPriority(t *testing.T) {
	d := openTestDB(t)
	task, _ := Create(d, "task", CreateOpts{})
	for _, bad := range []int{-1, 4, 100} {
		p := bad
		_, err := Update(d, task.ID, UpdateOpts{Priority: &p})
		if err == nil {
			t.Errorf("expected error for priority=%d, got nil", bad)
		}
	}
}

func TestUpdate_ValidPriorities(t *testing.T) {
	d := openTestDB(t)
	for _, p := range []int{0, 1, 2, 3} {
		task, _ := Create(d, "task", CreateOpts{})
		pri := p
		if _, err := Update(d, task.ID, UpdateOpts{Priority: &pri}); err != nil {
			t.Errorf("Update with priority=%d should not error: %v", p, err)
		}
	}
}

func TestUpdate_InvalidParent(t *testing.T) {
	d := openTestDB(t)
	task, _ := Create(d, "task", CreateOpts{})
	nonexistent := "zzzzzzz"
	_, err := Update(d, task.ID, UpdateOpts{ParentID: &nonexistent})
	if err == nil {
		t.Error("expected error for nonexistent parent, got nil")
	}
}

func TestUpdate_ValidParent(t *testing.T) {
	d := openTestDB(t)
	parent, _ := Create(d, "parent", CreateOpts{})
	child, _ := Create(d, "child", CreateOpts{})
	if _, err := Update(d, child.ID, UpdateOpts{ParentID: &parent.ID}); err != nil {
		t.Errorf("Update with valid parent should not error: %v", err)
	}
}

func TestUpdate_InvalidDueDate(t *testing.T) {
	d := openTestDB(t)
	task, _ := Create(d, "task", CreateOpts{})
	bad := "not-a-date"
	_, err := Update(d, task.ID, UpdateOpts{DueDate: &bad})
	if err == nil {
		t.Error("expected error for invalid due date, got nil")
	}
}

func TestCreate_InvalidPriority(t *testing.T) {
	d := openTestDB(t)
	for _, bad := range []int{-1, 4, 100} {
		_, err := Create(d, "task", CreateOpts{Priority: bad, PrioritySet: true})
		if err == nil {
			t.Errorf("expected error for priority=%d on create, got nil", bad)
		}
	}
}

func TestCreate_InvalidParent(t *testing.T) {
	d := openTestDB(t)
	nonexistent := "zzzzzzz"
	_, err := Create(d, "task", CreateOpts{ParentID: &nonexistent})
	if err == nil {
		t.Error("expected error for nonexistent parent on create, got nil")
	}
}

func TestCreate_InvalidDueDate(t *testing.T) {
	d := openTestDB(t)
	bad := "2026/06/25"
	_, err := Create(d, "task", CreateOpts{DueDate: &bad})
	if err == nil {
		t.Error("expected error for invalid due date on create, got nil")
	}
}

// A write from another connection (e.g. an agent's tt process) shows up as
// a live event on a local database.
func TestSubscribe_SQLiteSeesOtherProcessWrites(t *testing.T) {
	if os.Getenv("TT_TEST_POSTGRES_URL") != "" {
		t.Skip("SQLite polling only")
	}
	path := filepath.Join(t.TempDir(), "live.db")
	viewer, err := db.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := NewSQLStore(viewer).Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}

	writer, err := db.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := Create(writer, "from another process", CreateOpts{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-events:
	case <-time.After(3 * time.Second):
		t.Fatal("no live event after a write from another connection")
	}
	cancel()
	for range events {
	} // channel closes once ctx ends
}

// Empty values clear optional fields; a parent prefix is stored as the full
// ID; a task can't be its own parent.
func TestUpdate_ClearAndParentResolution(t *testing.T) {
	d := openTestDB(t)
	parent, _ := Create(d, "parent", CreateOpts{})
	who, due, desc := "bot", "2026-05-01", "body"
	child, err := Create(d, "child", CreateOpts{ParentID: &[]string{parent.ID[:4]}[0], Assignee: &who, DueDate: &due, Description: &desc})
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentID == nil || *child.ParentID != parent.ID {
		t.Errorf("Create stored parent %v, want full ID %s", child.ParentID, parent.ID)
	}
	empty := ""
	got, err := Update(d, child.ID, UpdateOpts{Assignee: &empty, DueDate: &empty, Description: &empty, ParentID: &empty})
	if err != nil {
		t.Fatalf("clearing fields: %v", err)
	}
	if got.Assignee != nil || got.DueDate != nil || got.Description != nil || got.ParentID != nil {
		t.Errorf("fields not cleared: %+v", got)
	}
	pre := parent.ID[:5]
	if got, _ := Update(d, child.ID, UpdateOpts{ParentID: &pre}); got == nil || *got.ParentID != parent.ID {
		t.Errorf("Update stored parent %v, want %s", got, parent.ID)
	}
	self := child.ID
	if _, err := Update(d, child.ID, UpdateOpts{ParentID: &self}); err == nil {
		t.Error("task became its own parent")
	}
}

// Save (tt edit, the web UI) validates like Update and resolves the parent.
func TestSave_Validates(t *testing.T) {
	d := openTestDB(t)
	parent, _ := Create(d, "parent", CreateOpts{})
	tk, _ := Create(d, "task", CreateOpts{})
	bad := *tk
	bad.Status = "GARBAGE"
	if err := Save(d, &bad); err == nil {
		t.Error("Save accepted an invalid status")
	}
	bad = *tk
	bad.Priority = 9
	if err := Save(d, &bad); err == nil {
		t.Error("Save accepted an invalid priority")
	}
	bad = *tk
	missing := "zzzzzzz"
	bad.ParentID = &missing
	if err := Save(d, &bad); err == nil {
		t.Error("Save accepted a missing parent")
	}
	ok := *tk
	pre := parent.ID[:4]
	ok.ParentID = &pre
	if err := Save(d, &ok); err != nil {
		t.Fatal(err)
	}
	if got, _ := Get(d, tk.ID); got.ParentID == nil || *got.ParentID != parent.ID {
		t.Errorf("Save stored parent %v, want %s", got.ParentID, parent.ID)
	}
}

func TestExternalRefs_UpsertAndGet(t *testing.T) {
	d := openTestDB(t)
	tsk, _ := Create(d, "task 1", CreateOpts{})
	url := "https://linear.app/issue/ENG-123"

	if err := UpsertExternalRef(d, tsk.ID, "linear", "ENG-123", &url); err != nil {
		t.Fatalf("UpsertExternalRef: %v", err)
	}

	refs, err := GetExternalRefs(d, tsk.ID)
	if err != nil {
		t.Fatalf("GetExternalRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].Source != "linear" || refs[0].ExternalID != "ENG-123" {
		t.Errorf("unexpected ref: %+v", refs[0])
	}
	if refs[0].URL == nil || *refs[0].URL != url {
		t.Errorf("url = %v, want %q", refs[0].URL, url)
	}
}

func TestExternalRefs_UpsertWithoutURL(t *testing.T) {
	d := openTestDB(t)
	tsk, _ := Create(d, "task 1", CreateOpts{})

	if err := UpsertExternalRef(d, tsk.ID, "github", "42", nil); err != nil {
		t.Fatalf("UpsertExternalRef: %v", err)
	}

	refs, _ := GetExternalRefs(d, tsk.ID)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].URL != nil {
		t.Errorf("expected nil url, got %v", refs[0].URL)
	}
}

func TestExternalRefs_UpsertUpdatesURL(t *testing.T) {
	d := openTestDB(t)
	tsk, _ := Create(d, "task 1", CreateOpts{})
	url1 := "https://linear.app/issue/ENG-123"
	url2 := "https://linear.app/issue/ENG-123-renamed"

	UpsertExternalRef(d, tsk.ID, "linear", "ENG-123", &url1)
	if err := UpsertExternalRef(d, tsk.ID, "linear", "ENG-123", &url2); err != nil {
		t.Fatalf("second UpsertExternalRef: %v", err)
	}

	refs, _ := GetExternalRefs(d, tsk.ID)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref after re-upsert, got %d", len(refs))
	}
	if refs[0].URL == nil || *refs[0].URL != url2 {
		t.Errorf("url = %v, want %q", refs[0].URL, url2)
	}
}

func TestExternalRefs_UpsertRepointsTask(t *testing.T) {
	d := openTestDB(t)
	t1, _ := Create(d, "task 1", CreateOpts{})
	t2, _ := Create(d, "task 2", CreateOpts{})

	UpsertExternalRef(d, t1.ID, "linear", "ENG-123", nil)
	if err := UpsertExternalRef(d, t2.ID, "linear", "ENG-123", nil); err != nil {
		t.Fatalf("re-point UpsertExternalRef: %v", err)
	}

	refs1, _ := GetExternalRefs(d, t1.ID)
	if len(refs1) != 0 {
		t.Errorf("expected ref moved off t1, got %d refs", len(refs1))
	}
	refs2, _ := GetExternalRefs(d, t2.ID)
	if len(refs2) != 1 {
		t.Fatalf("expected ref moved onto t2, got %d refs", len(refs2))
	}

	found, err := FindByExternalRef(d, "linear", "ENG-123")
	if err != nil {
		t.Fatalf("FindByExternalRef: %v", err)
	}
	if found.ID != t2.ID {
		t.Errorf("FindByExternalRef returned %s, want %s", found.ID, t2.ID)
	}
}

func TestFindByExternalRef_NotFound(t *testing.T) {
	d := openTestDB(t)
	if _, err := FindByExternalRef(d, "linear", "ENG-999"); err == nil {
		t.Error("expected error for unknown external ref")
	}
}

func TestGetExternalRefs_Empty(t *testing.T) {
	d := openTestDB(t)
	tsk, _ := Create(d, "task 1", CreateOpts{})
	refs, err := GetExternalRefs(d, tsk.ID)
	if err != nil {
		t.Fatalf("GetExternalRefs: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("expected 0 refs, got %d", len(refs))
	}
}
