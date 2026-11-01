package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jrdn/tt/internal/api"
	"github.com/jrdn/tt/internal/db"
	"github.com/jrdn/tt/internal/task"
)

func TestTaskDetailDescribesRelatedTasks(t *testing.T) {
	d, err := db.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	store := task.NewSQLStore(d)
	ctx := context.Background()
	a, _ := store.Create(ctx, "first", task.CreateOpts{})
	b, err := store.Create(ctx, "second one", task.CreateOpts{Priority: 1, PrioritySet: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddRelation(ctx, a.ID, "related", b.ID); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	(&api.Server{Resolve: func(*http.Request) (task.Store, error) { return store, nil }}).Register(mux, "/api")
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	for _, c := range []struct{ id, otherID, title string }{
		{a.ID, b.ID, "second one"},
		{b.ID, a.ID, "first"},
	} {
		res, err := http.Get(ts.URL + "/api/tasks/" + c.id)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Related map[string]struct {
				Title    string `json:"title"`
				Status   string `json:"status"`
				Priority int    `json:"priority"`
			} `json:"related_tasks"`
		}
		err = json.NewDecoder(res.Body).Decode(&body)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		got, ok := body.Related[c.otherID]
		if !ok || got.Title != c.title || got.Status == "" {
			t.Errorf("task %s: related_tasks[%s] = %+v, want title %q and a status", c.id, c.otherID, got, c.title)
		}
	}
}
