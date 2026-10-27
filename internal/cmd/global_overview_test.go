package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jrdn/tt/internal/client"
	"github.com/jrdn/tt/internal/task"
)

type fakeProjectList []client.Project

func (f fakeProjectList) Projects(context.Context) ([]client.Project, error) { return f, nil }

type fakeLister struct {
	tasks []task.Task
	err   error
}

func (f fakeLister) List(context.Context, task.ListOpts) ([]task.Task, error) {
	return f.tasks, f.err
}

func row(scope, id string, status task.Status, created string) overviewRow {
	return overviewRow{Scope: scope, Task: task.Task{ID: id, Status: status, CreatedAt: created}}
}

func TestOverviewQualifiedID(t *testing.T) {
	got := row("puck", "5qvhema", task.StatusReady, "").qualifiedID()
	if got != "puck.5qvhema" {
		t.Errorf("qualifiedID = %q, want puck.5qvhema", got)
	}
}

func TestSortOverviewRows(t *testing.T) {
	rows := []overviewRow{
		row("tt", "b", task.StatusOpen, "2026-01-02T00:00:00Z"),
		row("life", "a", task.StatusReady, "2026-01-01T00:00:00Z"),
		row("tt", "a", task.StatusOpen, "2026-01-03T00:00:00Z"),
		row("tt", "c", task.StatusDone, "2026-01-01T00:00:00Z"),
		row("tt", "d", task.StatusOpen, "2026-01-01T00:00:00Z"),
	}
	sortOverviewRows(rows)

	want := []string{"life.a", "tt.c", "tt.d", "tt.b", "tt.a"}
	for i, r := range rows {
		if r.qualifiedID() != want[i] {
			t.Fatalf("row %d = %s, want %s (order: %v)", i, r.qualifiedID(), want[i], ids(rows))
		}
	}
}

func ids(rows []overviewRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.qualifiedID()
	}
	return out
}

// Projects are aggregated across the key's projects, ordered by slug, and a
// project that fails to list does not hide the others.
func TestOverviewAcrossProjects(t *testing.T) {
	projects := fakeProjectList{{Slug: "tt"}, {Slug: "life"}, {Slug: "puck"}}
	stores := map[string]fakeLister{
		"tt":   {tasks: []task.Task{{ID: "t2", Status: task.StatusReady, CreatedAt: "2026-01-02T00:00:00Z"}, {ID: "t1", Status: task.StatusOpen, CreatedAt: "2026-01-01T00:00:00Z"}}},
		"life": {err: errors.New("boom")},
		"puck": {tasks: []task.Task{{ID: "p1", Status: task.StatusReady, CreatedAt: "2026-01-01T00:00:00Z"}}},
	}

	rows, errs, err := overviewAcrossProjects(context.Background(), projects,
		func(slug string) taskLister { return stores[slug] }, overviewOpts{all: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(ids(rows), ","), "puck.p1,tt.t1,tt.t2"; got != want {
		t.Errorf("rows = %s, want %s", got, want)
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "life") {
		t.Errorf("errs = %v, want one about list life", errs)
	}
}

func TestOverviewAcrossProjectsFilter(t *testing.T) {
	projects := fakeProjectList{{Slug: "tt"}, {Slug: "life"}}
	stores := map[string]fakeLister{
		"tt":   {tasks: []task.Task{{ID: "t1", Status: task.StatusReady}}},
		"life": {tasks: []task.Task{{ID: "l1", Status: task.StatusReady}}},
	}
	storeFor := func(slug string) taskLister { return stores[slug] }

	rows, _, err := overviewAcrossProjects(context.Background(), projects, storeFor, overviewOpts{}, "life")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(ids(rows), ","), "life.l1"; got != want {
		t.Errorf("rows = %s, want %s", got, want)
	}

	if _, _, err := overviewAcrossProjects(context.Background(), projects, storeFor, overviewOpts{}, "nope"); err == nil ||
		!strings.Contains(err.Error(), `no project named "nope"`) {
		t.Errorf("unknown project: err = %v", err)
	}
}

// JSON keeps "database" for local output and adds project plus the qualified
// id, so existing local consumers keep working.
func TestOverviewJSON(t *testing.T) {
	rows := []overviewRow{row("tt", "abc", task.StatusReady, ""), row("puck", "def", task.StatusOpen, "")}

	local := overviewJSON(rows, false)
	if local[0].Database != "tt" || local[0].Project != "" || local[0].ID != "tt.abc" {
		t.Errorf("local entry = %+v", local[0])
	}
	if local[0].Task == nil || local[0].Task.ID != "abc" {
		t.Errorf("local task = %+v", local[0].Task)
	}

	server := overviewJSON(rows, true)
	if server[1].Project != "puck" || server[1].Database != "" || server[1].ID != "puck.def" {
		t.Errorf("server entry = %+v", server[1])
	}
}
